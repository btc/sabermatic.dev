package session_test

import (
	"os"
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}
