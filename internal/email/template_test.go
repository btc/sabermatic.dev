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
	const logoURL = "https://example.com/mark-256.png"
	html, err := RenderEmail(template.HTML("<p>Hello world</p>"), "Test footer", logoURL)
	require.NoError(t, err)
	require.Contains(t, html, "Sabermatic[.DEV]")
	require.Contains(t, html, "Hello world")
	require.Contains(t, html, "Test footer")
	require.Contains(t, html, "#fffbf5") // warm background
}

func TestRenderEmail_WithLogo(t *testing.T) {
	const logoURL = "https://example.com/mark-256.png"
	html, err := RenderEmail(template.HTML("<p>body</p>"), "footer", logoURL)
	require.NoError(t, err)
	require.Contains(t, html, `src="`+logoURL+`"`)
	// Width/height/alt in order guards against matching an unrelated <img> with alt="".
	require.Contains(t, html, `width="28" height="28" alt=""`)
}

func TestRenderEmail_WithoutLogo(t *testing.T) {
	html, err := RenderEmail(template.HTML("<p>body</p>"), "footer", "")
	require.NoError(t, err)
	require.NotContains(t, html, "<img ")
	// The logo branch uses an inner `<table role="presentation">` for layout;
	// when the else branch renders, that inner layout table must be absent.
	// Match on the attributes that identify THIS table specifically (the inner
	// layout-only one), not `><tr>` suffix — attributes are stable, element
	// ordering/whitespace is not.
	require.NotContains(t, html, `role="presentation" cellpadding="0" cellspacing="0"`)
	// App name still appears in the plain-text branch.
	require.Contains(t, html, "Sabermatic[.DEV]")
}
