package aippatch

import (
	"fmt"

	"connectrpc.com/connect"
)

// All three helpers accept fmt.Errorf-style arguments including %w, so
// callers can wrap underlying errors in the chain (e.g.
// connectInternal("query: %w", err)).
func connectInvalidArg(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}

func connectNotFound(format string, args ...any) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf(format, args...))
}

func connectInternal(format string, args ...any) error {
	return connect.NewError(connect.CodeInternal, fmt.Errorf(format, args...))
}
