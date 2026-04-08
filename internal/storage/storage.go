package storage

import "context"

// Bucket provides operations on a single storage bucket.
type Bucket interface {
	// Put writes data and returns the canonical URL of the stored object.
	Put(ctx context.Context, key string, data []byte, contentType string) (url string, err error)

	// Delete removes a single object by key.
	Delete(ctx context.Context, key string) error

	// DeletePrefix removes all objects whose keys match the given string prefix.
	// Pure string prefix match — not path-segment-aware.
	// Callers should include trailing "/" for path-segment-aligned deletes.
	DeletePrefix(ctx context.Context, prefix string) error
}

// Store holds all application storage buckets. Callers must explicitly
// choose which bucket to use via Audio() or Public().
type Store interface {
	Audio() Bucket
	Public() Bucket
	Close() error
}
