package storage

import (
	"context"
	"fmt"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/btc/drill/internal/drilotel"
)

var tracer = drilotel.Tracer("storage")

// GCSStore stores objects in Google Cloud Storage.
type GCSStore struct {
	client *gcs.Client
	bucket string
}

// NewGCS creates a GCSStore using Application Default Credentials.
func NewGCS(ctx context.Context, bucket string) (*GCSStore, error) {
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &GCSStore{client: client, bucket: bucket}, nil
}

func (s *GCSStore) Put(ctx context.Context, key string, data []byte, contentType string) (_ string, err error) {
	ctx, span := tracer.Start(ctx, "GCSStore.Put")
	defer func() { drilotel.End(span, err) }()

	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		w.Close()
		return "", fmt.Errorf("gcs write %s: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("gcs close writer %s: %w", key, err)
	}
	return fmt.Sprintf("gs://%s/%s", s.bucket, key), nil
}

func (s *GCSStore) Delete(ctx context.Context, key string) (err error) {
	ctx, span := tracer.Start(ctx, "GCSStore.Delete")
	defer func() { drilotel.End(span, err) }()

	err = s.client.Bucket(s.bucket).Object(key).Delete(ctx)
	if err != nil && err != gcs.ErrObjectNotExist {
		return fmt.Errorf("gcs delete %s: %w", key, err)
	}
	return nil
}

func (s *GCSStore) DeletePrefix(ctx context.Context, prefix string) (err error) {
	ctx, span := tracer.Start(ctx, "GCSStore.DeletePrefix")
	defer func() { drilotel.End(span, err) }()

	it := s.client.Bucket(s.bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("gcs list prefix %s: %w", prefix, err)
		}
		if err := s.client.Bucket(s.bucket).Object(attrs.Name).Delete(ctx); err != nil && err != gcs.ErrObjectNotExist {
			return fmt.Errorf("gcs delete %s: %w", attrs.Name, err)
		}
	}
	return nil
}

// ForBucket returns a new GCSStore sharing the same client but targeting a
// different bucket. This avoids constructing multiple clients when the
// application writes to more than one bucket.
func (s *GCSStore) ForBucket(bucket string) *GCSStore {
	return &GCSStore{client: s.client, bucket: bucket}
}

// Close closes the underlying GCS client.
func (s *GCSStore) Close() error {
	return s.client.Close()
}
