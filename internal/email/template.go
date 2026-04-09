package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"sync"
	"time"

	"github.com/btc/drill/internal/branding"
)

//go:embed templates/*.html
var templateFS embed.FS

var (
	emailTmpl     *template.Template
	emailTmplOnce sync.Once
	emailTmplErr  error
)

func getTemplate() (*template.Template, error) {
	emailTmplOnce.Do(func() {
		emailTmpl, emailTmplErr = template.ParseFS(templateFS, "templates/wrapper.html")
	})
	return emailTmpl, emailTmplErr
}

// TemplateData holds the data for the shared email wrapper template.
type TemplateData struct {
	AppName string
	Body    template.HTML // Pre-rendered inner HTML
	Footer  string
}

// RenderEmail renders the shared email wrapper with the given body HTML and footer text.
func RenderEmail(bodyHTML template.HTML, footer string) (string, error) {
	tmpl, err := getTemplate()
	if err != nil {
		return "", fmt.Errorf("parse email template: %w", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, TemplateData{
		AppName: branding.AppName,
		Body:    bodyHTML,
		Footer:  footer,
	})
	if err != nil {
		return "", fmt.Errorf("render email template: %w", err)
	}
	return buf.String(), nil
}

// FormatDurationHuman formats a duration as a human-readable string
// like "1 hour", "30 minutes". For durations under 1 minute, returns "1 minute".
func FormatDurationHuman(d time.Duration) string {
	if d < time.Minute {
		return "1 minute"
	}
	hours := d.Hours()
	minutes := d.Minutes()

	if hours >= 1 && math.Mod(hours, 1) == 0 {
		h := int(hours)
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	m := int(minutes)
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}
