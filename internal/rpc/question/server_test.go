package question_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc"
	"github.com/btc/drill/internal/rpc/question"
)

// startPostgres starts a Postgres 16 container, runs app migrations, and
// returns the connection string. The container is terminated on test cleanup.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("drill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	// Run migrations (path from internal/rpc/question/ to sql/migrations/).
	d, err := iofs.New(os.DirFS("../../../sql/migrations"), ".")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}

	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		t.Fatalf("create migrate: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}

	return connStr
}

// newTestBackend creates a real Backend (with pool + River) for integration
// tests. The Backend is closed on test cleanup.
func newTestBackend(t *testing.T) *backend.Backend {
	t.Helper()
	connStr := startPostgres(t)

	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("AUTH_BCRYPT_COST", "4")

	cfg, err := config.Load()
	require.NoError(t, err)

	b, err := backend.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })

	return b
}

// signupAndLogin creates a user via HTTP signup+login endpoints and returns
// the raw session token string.
func signupAndLogin(t *testing.T, b *backend.Backend) string {
	t.Helper()
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	body := `{"email":"testuser@example.com","password":"securepass123","display_name":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	loginBody := `{"email":"testuser@example.com","password":"securepass123"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("session cookie not found after login")
	return ""
}

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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	srvURL := startQuestionServer(t, b)

	// No cookie — plain HTTP client.
	client := drillv1connect.NewQuestionServiceClient(&http.Client{}, srvURL)
	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestListQuestions_Authenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	seedQuestions(t, b, 3)

	srvURL := startQuestionServer(t, b)
	token := signupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Questions)
	require.Len(t, resp.Msg.Questions, 3)
}

func TestListQuestions_Pagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	seedQuestions(t, b, 3)

	srvURL := startQuestionServer(t, b)
	token := signupAndLogin(t, b)
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
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	srvURL := startQuestionServer(t, b)
	token := signupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{
		PageToken: "not-valid-base64-!@#$",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestListQuestions_EnumMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := newTestBackend(t)
	seedQuestions(t, b, 4)

	srvURL := startQuestionServer(t, b)
	token := signupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.ListQuestions(context.Background(), connect.NewRequest(&drillv1.ListQuestionsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Questions, 4)

	for _, q := range resp.Msg.Questions {
		require.NotEqual(t, drillv1.Difficulty_DIFFICULTY_UNSPECIFIED, q.Difficulty,
			"question %s has UNSPECIFIED difficulty", q.Id)
		require.NotEqual(t, drillv1.QuestionSource_QUESTION_SOURCE_UNSPECIFIED, q.Source,
			"question %s has UNSPECIFIED source", q.Id)
	}
}
