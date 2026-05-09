package idleunsub_test

import "github.com/btc/drill/internal/feat/idleunsub/idleunsubtest"

// Type aliases so existing test code in this package can keep using the
// short names fakeStripe, updateCall, nullMailer, recordingMailer.
// All implementations now live in idleunsubtest.

type (
	fakeStripe      = idleunsubtest.FakeStripe
	updateCall      = idleunsubtest.UpdateCall
	nullMailer      = idleunsubtest.NullMailer
	recordingMailer = idleunsubtest.RecordingMailer
)
