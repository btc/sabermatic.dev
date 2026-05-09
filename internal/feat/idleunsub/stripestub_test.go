package idleunsub_test

import "github.com/btc/drill/internal/feat/idleunsub/idleunsubtest"

// Type aliases so existing test code in this package can keep using the
// short names fakeStripe and recordingEnqueuer. All implementations live
// in idleunsubtest.

type (
	fakeStripe        = idleunsubtest.FakeStripe
	recordingEnqueuer = idleunsubtest.RecordingEnqueuer
)
