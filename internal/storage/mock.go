package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MockStore implements Store with separate mock buckets for testing.
type MockStore struct {
	AudioStore  *MockBucket
	PublicStore *MockBucket
}

func NewMockStore() *MockStore {
	return &MockStore{
		AudioStore:  NewMockBucket(),
		PublicStore: NewMockBucket(),
	}
}

func (m *MockStore) Audio() Bucket  { return m.AudioStore }
func (m *MockStore) Public() Bucket { return m.PublicStore }
func (m *MockStore) Close() error   { return nil }

// MockBucket records all calls for test assertions.
type MockBucket struct {
	mu      sync.Mutex
	Objects map[string][]byte
	PutErr  error // if set, Put returns this error
}

func NewMockBucket() *MockBucket {
	return &MockBucket{Objects: make(map[string][]byte)}
}

func (m *MockBucket) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	if m.PutErr != nil {
		return "", m.PutErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Objects[key] = data
	return fmt.Sprintf("mock://%s", key), nil
}

func (m *MockBucket) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objects, key)
	return nil
}

func (m *MockBucket) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.Objects {
		if strings.HasPrefix(k, prefix) {
			delete(m.Objects, k)
		}
	}
	return nil
}

// HasKey returns true if the given key exists in the bucket.
func (m *MockBucket) HasKey(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Objects[key]
	return ok
}

// Count returns the number of stored objects.
func (m *MockBucket) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Objects)
}
