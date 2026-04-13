package question_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/question"
	"github.com/btc/drill/internal/testutil"
)

// authedClient creates a Connect QuestionServiceClient with the session
// cookie set via a cookie jar.
func authedClient(t *testing.T, srvURL string, rawToken string) drillv1connect.QuestionServiceClient {
	t.Helper()
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  auth.SessionCookieName,
		Value: rawToken,
	}})
	return drillv1connect.NewQuestionServiceClient(
		&http.Client{Jar: jar},
		srvURL,
	)
}

// countQuestions returns the current number of questions in the database
// (including any inserted by migrations).
func countQuestions(t *testing.T, b *backend.Backend) int {
	t.Helper()
	var n int
	err := b.Pool().QueryRow(context.Background(), "SELECT count(*) FROM questions").Scan(&n)
	require.NoError(t, err)
	return n
}

// seedQuestions inserts n seed questions (source='seed', user_id=NULL) into the
// database so ListQuestions has data to return.
func seedQuestions(t *testing.T, b *backend.Backend, n int) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(b.Pool())
	for i := 0; i < n; i++ {
		difficulty := "medium"
		if i%2 == 1 {
			difficulty = "hard"
		}
		_, err := queries.InsertQuestion(ctx, db.InsertQuestionParams{
			UserID:         pgtype.UUID{},   // NULL — seed question
			Title:          "Seed Question " + strings.Repeat("A", i),
			Prompt:         "Explain concept " + strings.Repeat("B", i),
			Difficulty:     difficulty,
			Tags:           []string{"go", "testing"},
			Source:         "seed",
			CoachRationale: pgtype.Text{}, // NULL — not applicable for seed questions
		})
		require.NoError(t, err)
	}
}

// startQuestionServer creates the Connect handler with auth interceptor,
// starts an httptest.Server, and returns its URL.
func startQuestionServer(t *testing.T, b *backend.Backend) string {
	t.Helper()
	_, h := drillv1connect.NewQuestionServiceHandler(
		question.NewServer(b),
		connect.WithInterceptors(rpc.AuthInterceptor(b)),
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestListQuestions_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewQuestionServiceClient(&http.Client{}, srvURL)
	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestListQuestions_Authenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	baseline := countQuestions(t, b)
	seedQuestions(t, b, 3)

	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Questions)
	require.Len(t, resp.Msg.Questions, baseline+3)
}

func TestListQuestions_Pagination(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	seedQuestions(t, b, 3)

	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// First page: page_size=1
	resp1, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageSize: 1,
	}))
	require.NoError(t, err)
	require.Len(t, resp1.Msg.Questions, 1)
	require.NotEmpty(t, resp1.Msg.NextPageToken, "expected next_page_token for page 1")

	page1ID := resp1.Msg.Questions[0].Id

	// Second page: use the token from first page.
	resp2, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageSize:  1,
		PageToken: resp1.Msg.NextPageToken,
	}))
	require.NoError(t, err)
	require.Len(t, resp2.Msg.Questions, 1)

	page2ID := resp2.Msg.Questions[0].Id

	// No overlap between pages.
	require.NotEqual(t, page1ID, page2ID, "page 1 and page 2 should not overlap")
}

func TestListQuestions_InvalidPageToken(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageToken: "not-valid-base64-!@#$",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestListQuestions_EnumMapping(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	baseline := countQuestions(t, b)
	seedQuestions(t, b, 4)

	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Questions, baseline+4)

	for _, q := range resp.Msg.Questions {
		require.NotEqual(t, drillv1.Difficulty_DIFFICULTY_UNSPECIFIED, q.Difficulty,
			"question %s has UNSPECIFIED difficulty", q.Id)
		require.NotEqual(t, drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED, q.Source,
			"question %s has UNSPECIFIED source", q.Id)
	}
}

func TestCreateQuestion_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:      "Design a rate limiter",
			Prompt:     "Design a distributed rate limiter for an API gateway.",
			Difficulty: drillv1.Difficulty_DIFFICULTY_HARD,
			Tags:       []string{"distributed-systems", "scaling"},
		},
	}))
	require.NoError(t, err)

	q := resp.Msg
	require.NotEmpty(t, q.Id, "server should assign an ID")
	require.NotNil(t, q.UserId, "server should assign user_id")
	require.Equal(t, "Design a rate limiter", q.Title)
	require.Equal(t, "Design a distributed rate limiter for an API gateway.", q.Prompt)
	require.Equal(t, drillv1.Difficulty_DIFFICULTY_HARD, q.Difficulty)
	require.Equal(t, []string{"distributed-systems", "scaling"}, q.Tags)
	require.Equal(t, drillv1.QuestionSource_QUESTION_SOURCE_CUSTOM, q.Source)
	require.NotNil(t, q.CreateTime, "server should assign create_time")
}

func TestCreateQuestion_DefaultDifficulty(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a cache",
			Prompt: "Design a distributed cache.",
		},
	}))
	require.NoError(t, err)
	require.Equal(t, drillv1.Difficulty_DIFFICULTY_MEDIUM, resp.Msg.Difficulty)
}

func TestCreateQuestion_AppearsInList(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	baseline, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	beforeCount := len(baseline.Msg.Questions)

	_, err = client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a queue",
			Prompt: "Design a message queue system.",
		},
	}))
	require.NoError(t, err)

	after, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.Len(t, after.Msg.Questions, beforeCount+1)
}

func TestCreateQuestion_NilQuestion(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_EmptyTitle(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "",
			Prompt: "Some prompt",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_EmptyPrompt(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design something",
			Prompt: "",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_InvalidDifficulty(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:      "Design a cache",
			Prompt:     "Design a distributed cache.",
			Difficulty: drillv1.Difficulty(99),
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateQuestion_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startQuestionServer(t, b)

	client := drillv1connect.NewQuestionServiceClient(&http.Client{}, srvURL)
	_, err := client.CreateQuestion(context.Background(), connect.NewRequest(&drillv1.CreateQuestionRequest{
		Question: &drillv1.Question{
			Title:  "Design a cache",
			Prompt: "Design a distributed cache.",
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
