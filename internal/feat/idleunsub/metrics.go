package idleunsub

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// otel meter.Int64Counter returns (counter, error) but the contract is that
// errors are reserved for invalid arguments — name/description constants
// here are valid by construction. On error, the API returns a non-nil
// no-op counter, so the discard is safe and Add() never panics. This
// satisfies the project rule "never panic at init time" without forcing
// boilerplate error propagation through main().
var (
	meter            = otel.Meter("idleunsub")
	cancelFired, _   = meter.Int64Counter("idleunsub.cancel.fired")
	cancelSkipped, _ = meter.Int64Counter("idleunsub.cancel.skipped")
	cancelError, _   = meter.Int64Counter("idleunsub.cancel.error")
	//nolint:unused // wired up by AutoReverse in Task 8
	cacheDrift, _ = meter.Int64Counter("idleunsub.cancel.cache_drift_corrected")
	//nolint:unused // wired up by KeepSubscription in Task 8
	reverseLink, _ = meter.Int64Counter("idleunsub.reverse.link")
	//nolint:unused // wired up by AutoReverse in Task 8
	reverseAct, _ = meter.Int64Counter("idleunsub.reverse.activity")
	emailEnqueue, _  = meter.Int64Counter("idleunsub.email.enqueue")
)

func mCancelFired(ctx context.Context, subID string) {
	cancelFired.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}

func mCancelSkipped(ctx context.Context, reason string) {
	cancelSkipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

func mCancelError(ctx context.Context, reason string) {
	cancelError.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

//nolint:unused // wired up in Task 8
func mCacheDriftCorrected(ctx context.Context, subID string) {
	cacheDrift.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}

//nolint:unused // wired up in Task 8
func mReverseLink(ctx context.Context, subID string) {
	reverseLink.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}

//nolint:unused // wired up in Task 8
func mReverseActivity(ctx context.Context, subID string) {
	reverseAct.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}

func mEmailEnqueue(ctx context.Context, kind, status string) {
	emailEnqueue.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("status", status)))
}
