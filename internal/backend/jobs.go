package backend

import (
	"context"

	pgx "github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Jobs is the interface for enqueueing and stopping background jobs.
// Satisfied by *river.Client in production.
type Jobs interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	Stop(ctx context.Context) error
}
