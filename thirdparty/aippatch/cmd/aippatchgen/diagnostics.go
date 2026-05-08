package main

import (
	"fmt"
	"strings"
)

// Diagnostic is a structured codegen error with a message and optional hint.
type Diagnostic struct {
	Resource string
	Message  string
	Hint     string
}

func (d Diagnostic) Error() string {
	var b strings.Builder
	if d.Resource != "" {
		fmt.Fprintf(&b, "aippatchgen: %s: ", d.Resource)
	} else {
		b.WriteString("aippatchgen: ")
	}
	b.WriteString(d.Message)
	if d.Hint != "" {
		b.WriteString("\n  hint: ")
		b.WriteString(d.Hint)
	}
	return b.String()
}

// Diagnostics is a collection.
type Diagnostics []Diagnostic

func (ds Diagnostics) Err() error {
	if len(ds) == 0 {
		return nil
	}
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.Error()
	}
	return fmt.Errorf("%s", strings.Join(parts, "\n\n"))
}
