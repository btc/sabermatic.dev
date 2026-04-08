package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalStore stores objects on the local filesystem with separate audio and public directories.
type LocalStore struct {
	baseDir string
}

// NewLocal creates a LocalStore rooted at baseDir, creating it if needed.
func NewLocal(baseDir string) (*LocalStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &LocalStore{baseDir: baseDir}, nil
}

func (s *LocalStore) Audio() Bucket  { return &localBucket{dir: filepath.Join(s.baseDir, "audio")} }
func (s *LocalStore) Public() Bucket { return &localBucket{dir: filepath.Join(s.baseDir, "public")} }

// Close is a no-op for local filesystem storage.
func (s *LocalStore) Close() error { return nil }

// localBucket operates on a single directory within the local filesystem.
type localBucket struct {
	dir string
}

// guard validates that key does not escape the bucket root and returns the
// absolute path for the key.
func (b *localBucket) guard(key string) (string, error) {
	p := filepath.Join(b.dir, filepath.FromSlash(key))
	if !strings.HasPrefix(p, b.dir+string(filepath.Separator)) && p != b.dir {
		return "", fmt.Errorf("key %q escapes storage root", key)
	}
	return p, nil
}

func (b *localBucket) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	// Ensure the bucket directory exists on first Put.
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return "", fmt.Errorf("create bucket dir: %w", err)
	}
	p, err := b.guard(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("create parent dirs: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return "file://" + p, nil
}

func (b *localBucket) Delete(_ context.Context, key string) error {
	p, err := b.guard(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (b *localBucket) DeletePrefix(_ context.Context, prefix string) error {
	// Ensure the bucket directory exists before walking.
	if _, err := os.Stat(b.dir); os.IsNotExist(err) {
		return nil
	}

	// parentDirs collects unique parent directories of removed files, keyed by
	// their depth (number of path separators) so we can remove deepest-first.
	type dirDepth struct {
		path  string
		depth int
	}
	seen := map[string]int{} // path -> depth

	if err := filepath.WalkDir(b.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for key comparison (matches GCS semantics).
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, prefix) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete %s: %w", rel, err)
			}
			// Record the parent directory and its depth for cleanup below.
			parent := filepath.Dir(path)
			depth := strings.Count(parent, string(filepath.Separator))
			if _, exists := seen[parent]; !exists {
				seen[parent] = depth
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Build a slice sorted deepest-first so we remove children before parents.
	dirs := make([]dirDepth, 0, len(seen))
	for p, d := range seen {
		dirs = append(dirs, dirDepth{path: p, depth: d})
	}
	// Sort in-place: higher depth first.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].depth > dirs[j].depth })

	for _, dd := range dirs {
		// Ignore the error: Remove returns ENOTEMPTY for non-empty dirs,
		// which is expected when siblings outside the prefix remain.
		_ = os.Remove(dd.path)
	}
	return nil
}
