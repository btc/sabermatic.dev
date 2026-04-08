package storage

import (
	"context"
	"fmt"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/btc/drill/internal/drilotel"
)

var tracer = drilotel.Tracer("storage")

// GCSStore stores objects in Google Cloud Storage with separate audio and public buckets.
type GCSStore struct {
	client       *gcs.Client
	audioBucket  string
	publicBucket string
}

// NewGCS creates a GCSStore using Application Default Credentials.
func NewGCS(ctx context.Context, audioBucket, publicBucket string) (*GCSStore, error) {
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &GCSStore{client: client, audioBucket: audioBucket, publicBucket: publicBucket}, nil
}

func (s *GCSStore) Audio() Bucket  { return &gcsBucket{client: s.client, bucket: s.audioBucket} }
func (s *GCSStore) Public() Bucket { return &gcsBucket{client: s.client, bucket: s.publicBucket} }

// Close closes the underlying GCS client.
func (s *GCSStore) Close() error { return s.client.Close() }

// gcsBucket operates on a single GCS bucket.
type gcsBucket struct {
	client *gcs.Client
	bucket string
}

func (b *gcsBucket) Put(ctx context.Context, key string, data []byte, contentType string) (_ string, err error) {
	ctx, span := tracer.Start(ctx, "gcsBucket.Put")
	defer func() { drilotel.End(span, err) }()

	w := b.client.Bucket(b.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		w.Close()
		return "", fmt.Errorf("gcs write %s: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("gcs close writer %s: %w", key, err)
	}
	return fmt.Sprintf("gs://%s/%s", b.bucket, key), nil
}

func (b *gcsBucket) Delete(ctx context.Context, key string) (err error) {
	ctx, span := tracer.Start(ctx, "gcsBucket.Delete")
	defer func() { drilotel.End(span, err) }()

	err = b.client.Bucket(b.bucket).Object(key).Delete(ctx)
	if err != nil && err != gcs.ErrObjectNotExist {
		return fmt.Errorf("gcs delete %s: %w", key, err)
	}
	return nil
}

func (b *gcsBucket) DeletePrefix(ctx context.Context, prefix string) (err error) {
	ctx, span := tracer.Start(ctx, "gcsBucket.DeletePrefix")
	defer func() { drilotel.End(span, err) }()

	it := b.client.Bucket(b.bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("gcs list prefix %s: %w", prefix, err)
		}
		if err := b.client.Bucket(b.bucket).Object(attrs.Name).Delete(ctx); err != nil && err != gcs.ErrObjectNotExist {
			return fmt.Errorf("gcs delete %s: %w", attrs.Name, err)
		}
	}
	return nil
}
