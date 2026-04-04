package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MockStore records all calls for test assertions.
type MockStore struct {
	mu      sync.Mutex
	Objects map[string][]byte
	PutErr  error // if set, Put returns this error
}

func NewMockStore() *MockStore {
	return &MockStore{Objects: make(map[string][]byte)}
}

func (m *MockStore) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	if m.PutErr != nil {
		return "", m.PutErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Objects[key] = data
	return fmt.Sprintf("mock://%s", key), nil
}

func (m *MockStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objects, key)
	return nil
}

func (m *MockStore) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.Objects {
		if strings.HasPrefix(k, prefix) {
			delete(m.Objects, k)
		}
	}
	return nil
}

// HasKey returns true if the given key exists in the store.
func (m *MockStore) HasKey(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Objects[key]
	return ok
}

// Count returns the number of stored objects.
func (m *MockStore) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Objects)
}

// Close is a no-op for the mock store.
func (m *MockStore) Close() error { return nil }
