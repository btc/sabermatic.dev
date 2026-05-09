package aippatch

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

func TestConnectErrorHelpers(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code connect.Code
	}{
		{"invalid arg", connectInvalidArg("bad %q", "x"), connect.CodeInvalidArgument},
		{"not found", connectNotFound("not here"), connect.CodeNotFound},
		{"internal", connectInternal("boom"), connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.code, connect.CodeOf(tc.err))
		})
	}
}
