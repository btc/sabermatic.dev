package backend

import (
	"context"
	"sync"

	pgx "github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Jobs is the interface for enqueueing and stopping background jobs.
// Satisfied by *river.Client in production, RecordingJobs in tests.
type Jobs interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	Stop(ctx context.Context) error
}

// RecordingJobs records inserted jobs for test assertions. Also satisfies Jobs.
type RecordingJobs struct {
	mu       sync.Mutex
	inserted []river.JobArgs
}

func (r *RecordingJobs) Insert(_ context.Context, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inserted = append(r.inserted, args)
	return &rivertype.JobInsertResult{}, nil
}

func (r *RecordingJobs) InsertTx(_ context.Context, _ pgx.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inserted = append(r.inserted, args)
	return &rivertype.JobInsertResult{}, nil
}

func (r *RecordingJobs) Stop(context.Context) error { return nil }

// Inserted returns all recorded job args.
func (r *RecordingJobs) Inserted() []river.JobArgs {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]river.JobArgs, len(r.inserted))
	copy(cp, r.inserted)
	return cp
}
