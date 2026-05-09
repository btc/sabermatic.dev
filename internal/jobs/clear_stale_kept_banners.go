package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

// ClearStaleKeptBannersArgs are the arguments for the ClearStaleKeptBanners
// periodic job. The query (sql/queries/users.sql ClearStaleKeptBanners) is a
// single UPDATE — no parameters are needed.
type ClearStaleKeptBannersArgs struct{}

// Kind returns the River job kind.
func (ClearStaleKeptBannersArgs) Kind() string { return "clear_stale_kept_banners" }

// ClearStaleKeptBannersWorker is a daily hygiene worker that turns off
// pending_kept_banner for any user whose subscription_kept event is older
// than 14 days. Implements spec §4.5: the kept-banner is a transient
// post-reverse welcome-back UX — long-stale rows are cleared so the banner
// never lingers if the frontend forgets to dismiss it.
type ClearStaleKeptBannersWorker struct {
	river.WorkerDefaults[ClearStaleKeptBannersArgs]
	Pool *pgxpool.Pool
}

// Work executes the ClearStaleKeptBanners SQL query against the pool.
func (w *ClearStaleKeptBannersWorker) Work(ctx context.Context, _ *river.Job[ClearStaleKeptBannersArgs]) error {
	q := db.New(w.Pool)
	if err := q.ClearStaleKeptBanners(ctx); err != nil {
		return fmt.Errorf("clear stale kept banners: %w", err)
	}
	slog.Debug("clear_stale_kept_banners ran")
	return nil
}
