package main

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPackBuyRejectsInvalidMinutes(t *testing.T) {
	// Construct a Runner without touching the DB — packBuy must reject
	// invalid minute counts before any Stripe or DB interaction.
	r := &Runner{}
	err := r.packBuy(context.Background(), uuid.New(), 999)
	if err == nil {
		t.Fatal("expected error for invalid minute count, got nil")
	}
	if !strings.Contains(err.Error(), "invalid pack size") {
		t.Fatalf("expected 'invalid pack size' in error, got %q", err.Error())
	}
}
