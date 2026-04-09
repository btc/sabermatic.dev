package email

import (
	"html/template"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatDurationHuman(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{30 * time.Minute, "30 minutes"},
		{1 * time.Minute, "1 minute"},
		{90 * time.Minute, "90 minutes"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			require.Equal(t, tt.want, FormatDurationHuman(tt.d))
		})
	}
}

func TestFormatDurationHuman_SubMinute(t *testing.T) {
	require.Equal(t, "1 minute", FormatDurationHuman(0))
	require.Equal(t, "1 minute", FormatDurationHuman(30*time.Second))
}

func TestRenderEmail(t *testing.T) {
	html, err := RenderEmail(template.HTML("<p>Hello world</p>"), "Test footer")
	require.NoError(t, err)
	require.Contains(t, html, "Sabermatic[.DEV]")
	require.Contains(t, html, "Hello world")
	require.Contains(t, html, "Test footer")
	require.Contains(t, html, "#fffbf5") // warm background
}
