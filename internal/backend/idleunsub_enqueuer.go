package backend

import (
	"context"
	"fmt"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/jobs"
)

// idleunsubEnqueuer adapts the Backend's River client to idleunsub.EmailEnqueuer.
// The webhook and AutoReverse paths must not block on Mailgun delivery, so
// idleunsub queues SendEmailJobs through River and lets the notifications
// worker pool deliver them out-of-band (spec §4.1, §4.3, §9).
//
// The enqueuer holds the Jobs interface (not *river.Client directly) so unit
// tests of the Backend can swap the implementation without standing up a real
// River client.
type idleunsubEnqueuer struct {
	jobs Jobs
	cfg  *config.Email
}

// EnqueueSendEmail inserts a SendEmailJob via River, applying the project's
// shared notifications-queue insert options.
func (e *idleunsubEnqueuer) EnqueueSendEmail(ctx context.Context, args jobs.SendEmailArgs) error {
	if _, err := e.jobs.Insert(ctx, args, jobs.SendEmailInsertOpts(e.cfg)); err != nil {
		return fmt.Errorf("insert send_email job: %w", err)
	}
	return nil
}

var _ idleunsub.EmailEnqueuer = (*idleunsubEnqueuer)(nil)
