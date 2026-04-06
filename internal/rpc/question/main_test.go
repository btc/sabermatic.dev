package question_test

import (
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	pg.RunTests(m)
}
