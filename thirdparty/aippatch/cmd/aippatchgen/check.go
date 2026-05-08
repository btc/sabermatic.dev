package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func checkOutputs(dir string, outputs map[string]string) error {
	// Iterate in sorted order so a CI failure always reports the same file
	// first when multiple are out of date.
	keys := make([]string, 0, len(outputs))
	for k := range outputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, fname := range keys {
		path := filepath.Join(dir, fname)
		got, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("aippatchgen --check: %s: %w", path, err)
		}
		if !bytes.Equal(got, []byte(outputs[fname])) {
			return fmt.Errorf("aippatchgen --check: %s is out of date; rerun `make generate`", path)
		}
	}
	return nil
}

func writeOutputs(dir string, outputs map[string]string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for fname, src := range outputs {
		path := filepath.Join(dir, fname)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return nil
}
