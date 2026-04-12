// Package handler_test contains integration tests for the HTTP layer.
//
// These tests verify HTTP-specific concerns: status codes, cookies, JSON
// response shape, and middleware wiring. They intentionally do NOT test
// business logic, validation edge cases, or database state — those belong
// in package backend's tests (internal/backend/*_test.go).
//
// If you're adding a new backend method or business rule, write the test in
// internal/backend/. Only add a handler test if you need to verify something
// specific to the HTTP contract (e.g. a new status code mapping, cookie
// behavior, or middleware interaction).
package handler_test

import (
	"testing"

	"github.com/btc/drill/internal/testutil"
)

var pg testutil.PG

func TestMain(m *testing.M) {
	pg = testutil.SharedPostgres()
	pg.RunTests(m)
}
