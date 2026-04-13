package main

import (
	"context"
	"strings"
	"testing"
)

func TestNewRunnerRejectsLiveKey(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_ABC")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for live key, got nil")
	}
	if !strings.Contains(err.Error(), "not a test key") {
		t.Fatalf("expected 'not a test key' in error, got %q", err.Error())
	}
}

func TestNewRunnerRejectsMissingKey(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "")
	t.Setenv("DATABASE_URL", "postgres://example/db")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

func TestNewRunnerRejectsMissingDatabaseURL(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_ABC")
	t.Setenv("DATABASE_URL", "")
	_, err := newRunner(context.Background())
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL in error, got %q", err.Error())
	}
}
