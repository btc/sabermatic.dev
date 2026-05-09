package user_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/btc/drill/internal/patches"
	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	if err := patches.InitPatches(); err != nil {
		fmt.Fprintf(os.Stderr, "patches.InitPatches: %v\n", err)
		os.Exit(1)
	}
	pg = testutil.SharedPostgres()
	pg.RunTests(m)
}
