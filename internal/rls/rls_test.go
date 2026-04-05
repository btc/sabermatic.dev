package rls_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/rls"
	migrations "github.com/btc/drill/sql/migrations"
)

// startPostgresWithRLS starts a Postgres 16 container, runs migrations
// (including 007_rls), creates the drill_app role and grants, and returns
// two connection strings: one for the superuser (for setup) and one for
// drill_app (for RLS-scoped queries).
func startPostgresWithRLS(t *testing.T) (superConnStr, appConnStr string) {
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
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "get connection string")

	// Run migrations (includes 007_rls which creates drill_app role + policies).
	d, err := iofs.New(migrations.FS, ".")
	require.NoError(t, err, "create migration source")

	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	require.NoError(t, err, "create migrate")

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	require.NoError(t, srcErr, "close migration source")
	require.NoError(t, dbErr, "close migration db")

	// Set a password for drill_app so we can connect as that role.
	superPool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err, "create super pool")
	defer superPool.Close()

	_, err = superPool.Exec(ctx, "ALTER ROLE drill_app PASSWORD 'drill_app_pass'")
	require.NoError(t, err, "set drill_app password")

	// Build app connection string by replacing user:pass in the superuser URL.
	appConn := strings.Replace(connStr, "test:test@", "drill_app:drill_app_pass@", 1)

	return connStr, appConn
}

// seedUser creates a user directly via the superuser pool and returns
// the user's UUID.
func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES ($1, $2, 'hash') RETURNING id`,
		email, "User "+email,
	).Scan(&id)
	require.NoError(t, err, "seed user %s", email)
	return id
}

// seedQuestion inserts a question. If userID is uuid.Nil, user_id is NULL
// (shared/seed question).
func seedQuestion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var uid *uuid.UUID
	if userID != uuid.Nil {
		uid = &userID
	}
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO questions (user_id, title, prompt, difficulty, source)
		 VALUES ($1, $2, 'prompt', 'medium', 'seed') RETURNING id`,
		uid, title,
	).Scan(&id)
	require.NoError(t, err, "seed question %s", title)
	return id
}

// seedSession inserts an interview_sessions row.
func seedSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, questionID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO interview_sessions (user_id, question_id, config_duration_minutes)
		 VALUES ($1, $2, 30) RETURNING id`,
		userID, questionID,
	).Scan(&id)
	require.NoError(t, err, "seed session")
	return id
}

func TestRLSIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	superConnStr, appConnStr := startPostgresWithRLS(t)

	// Super pool for seeding data (bypasses RLS as table owner).
	superPool, err := pgxpool.New(ctx, superConnStr)
	require.NoError(t, err)
	defer superPool.Close()

	// App pool connects as drill_app (subject to RLS).
	appPool, err := pgxpool.New(ctx, appConnStr)
	require.NoError(t, err)
	defer appPool.Close()

	// Seed two users and data.
	userA := seedUser(t, ctx, superPool, "alice@example.com")
	userB := seedUser(t, ctx, superPool, "bob@example.com")

	qShared := seedQuestion(t, ctx, superPool, uuid.Nil, "Shared Question")
	qA := seedQuestion(t, ctx, superPool, userA, "Alice Question")
	_ = seedQuestion(t, ctx, superPool, userB, "Bob Question")

	sessA := seedSession(t, ctx, superPool, userA, qShared)
	sessB := seedSession(t, ctx, superPool, userB, qShared)

	t.Run("user_A_sees_only_own_sessions", func(t *testing.T) {
		err := rls.WithUser(ctx, appPool, userA, func(ctx context.Context, db rls.DBTX) error {
			rows, err := db.Query(ctx, "SELECT id FROM interview_sessions")
			require.NoError(t, err)
			defer rows.Close()

			var ids []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				require.NoError(t, rows.Scan(&id))
				ids = append(ids, id)
			}
			require.NoError(t, rows.Err())

			assert.Equal(t, []uuid.UUID{sessA}, ids, "user A should see only their session")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("user_B_sees_only_own_sessions", func(t *testing.T) {
		err := rls.WithUser(ctx, appPool, userB, func(ctx context.Context, db rls.DBTX) error {
			rows, err := db.Query(ctx, "SELECT id FROM interview_sessions")
			require.NoError(t, err)
			defer rows.Close()

			var ids []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				require.NoError(t, rows.Scan(&id))
				ids = append(ids, id)
			}
			require.NoError(t, rows.Err())

			assert.Equal(t, []uuid.UUID{sessB}, ids, "user B should see only their session")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("shared_questions_visible_to_all", func(t *testing.T) {
		err := rls.WithUser(ctx, appPool, userA, func(ctx context.Context, db rls.DBTX) error {
			rows, err := db.Query(ctx, "SELECT id, title FROM questions ORDER BY title")
			require.NoError(t, err)
			defer rows.Close()

			var titles []string
			for rows.Next() {
				var id uuid.UUID
				var title string
				require.NoError(t, rows.Scan(&id, &title))
				titles = append(titles, title)
			}
			require.NoError(t, rows.Err())

			// User A should see the shared question and their own, but NOT Bob's.
			assert.Equal(t, []string{"Alice Question", "Shared Question"}, titles)
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("no_user_id_set_returns_zero_rows_for_user_tables", func(t *testing.T) {
		// Connect as drill_app but do NOT call set_config.
		conn, err := appPool.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()

		// Sessions: should be zero rows (fail-closed).
		var sessionCount int
		err = conn.QueryRow(ctx, "SELECT count(*) FROM interview_sessions").Scan(&sessionCount)
		require.NoError(t, err)
		assert.Equal(t, 0, sessionCount, "no user_id set → zero sessions (fail-closed)")

		// Questions with user_id (non-shared): zero rows for user-owned questions.
		var userQuestionCount int
		err = conn.QueryRow(ctx, "SELECT count(*) FROM questions WHERE user_id IS NOT NULL").Scan(&userQuestionCount)
		require.NoError(t, err)
		assert.Equal(t, 0, userQuestionCount, "no user_id set → zero user-owned questions (fail-closed)")

		// Shared questions (user_id IS NULL) remain visible by design — the
		// questions_isolation policy explicitly allows NULL user_id rows.
		var sharedCount int
		err = conn.QueryRow(ctx, "SELECT count(*) FROM questions WHERE user_id IS NULL").Scan(&sharedCount)
		require.NoError(t, err)
		assert.Equal(t, 1, sharedCount, "shared questions visible even without user_id set")
	})

	t.Run("questions_user_A_sees_shared_and_own", func(t *testing.T) {
		err := rls.WithUser(ctx, appPool, userA, func(ctx context.Context, db rls.DBTX) error {
			var count int
			err := db.QueryRow(ctx, "SELECT count(*) FROM questions").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 2, count, "user A sees shared + own question")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("questions_user_B_sees_shared_and_own", func(t *testing.T) {
		err := rls.WithUser(ctx, appPool, userB, func(ctx context.Context, db rls.DBTX) error {
			var count int
			err := db.QueryRow(ctx, "SELECT count(*) FROM questions").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 2, count, "user B sees shared + own question")
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("with_check_prevents_cross_user_insert", func(t *testing.T) {
		// Alice should NOT be able to insert a session for Bob (WITH CHECK violation).
		err := rls.WithUser(ctx, appPool, userA, func(ctx context.Context, db rls.DBTX) error {
			_, err := db.Exec(ctx,
				`INSERT INTO interview_sessions (user_id, question_id, status, config_duration_minutes) VALUES ($1, $2, 'active', 45)`,
				userB, qShared,
			)
			require.Error(t, err, "Alice should not be able to insert a session for Bob")
			return nil
		})
		require.NoError(t, err)
	})

	_ = qA // used to verify A's own question shows up
}

func TestRLSMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	superConnStr, appConnStr := startPostgresWithRLS(t)

	superPool, err := pgxpool.New(ctx, superConnStr)
	require.NoError(t, err)
	defer superPool.Close()

	appPool, err := pgxpool.New(ctx, appConnStr)
	require.NoError(t, err)
	defer appPool.Close()

	userA := seedUser(t, ctx, superPool, "carol@example.com")
	userB := seedUser(t, ctx, superPool, "dave@example.com")

	qShared := seedQuestion(t, ctx, superPool, uuid.Nil, "Shared")
	seedSession(t, ctx, superPool, userA, qShared)
	seedSession(t, ctx, superPool, userB, qShared)

	middleware := rls.Middleware(appPool)

	t.Run("middleware_scopes_to_authenticated_user", func(t *testing.T) {
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			db := rls.DBFromContext(r.Context(), appPool)
			var count int
			err := db.QueryRow(r.Context(), "SELECT count(*) FROM interview_sessions").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "middleware should scope to user A's sessions")
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(auth.WithUser(req.Context(), &auth.AuthUser{ID: userA}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("middleware_skips_when_no_user", func(t *testing.T) {
		var dbFromCtx rls.DBTX
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dbFromCtx = rls.DBFromContext(r.Context(), appPool)
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		// When no user, DBFromContext returns the fallback (pool itself).
		assert.Equal(t, appPool, dbFromCtx, "should return fallback pool when no user")
	})
}

func TestDBFromContextFallback(t *testing.T) {
	// Unit test: DBFromContext with no connection in context returns fallback.
	ctx := context.Background()
	var fallback rls.DBTX // nil is a valid fallback in this test
	result := rls.DBFromContext(ctx, fallback)
	assert.Nil(t, result, "should return nil fallback when context has no connection")
}
