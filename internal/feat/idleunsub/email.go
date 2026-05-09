package idleunsub

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"

	texttemplate "text/template"

	"github.com/btc/drill/internal/email"
)

//go:embed templates/*
var templatesFS embed.FS

// Embedded templates are validated by package tests; ParseFS cannot fail at
// runtime. CLAUDE.md "never panic at init time" allows template.Must here
// because the FS is fixed at compile time and parse errors fail `go test`.
var (
	cancelHTMLTpl = template.Must(template.ParseFS(templatesFS, "templates/cancel.html.tmpl"))
	cancelTextTpl = texttemplate.Must(texttemplate.ParseFS(templatesFS, "templates/cancel.txt.tmpl"))
	keptHTMLTpl   = template.Must(template.ParseFS(templatesFS, "templates/kept.html.tmpl"))
	keptTextTpl   = texttemplate.Must(texttemplate.ParseFS(templatesFS, "templates/kept.txt.tmpl"))
)

type cancelEmailData struct {
	DisplayName      string
	CurrentPeriodEnd string
	KeepLink         string
}

type keptEmailData struct {
	DisplayName     string
	NextRenewalDate string
}

func composeCancelEmail(toEmail, displayName, keepURL string, periodEnd time.Time) (email.Message, error) {
	data := cancelEmailData{
		DisplayName:      displayName,
		CurrentPeriodEnd: periodEnd.UTC().Format("January 2, 2006"),
		KeepLink:         keepURL,
	}
	return renderEmail(cancelHTMLTpl, cancelTextTpl, toEmail,
		"We won't charge you for the next period", data)
}

func composeKeptEmail(toEmail, displayName string, nextRenewal time.Time) (email.Message, error) {
	data := keptEmailData{
		DisplayName:     displayName,
		NextRenewalDate: nextRenewal.UTC().Format("January 2, 2006"),
	}
	return renderEmail(keptHTMLTpl, keptTextTpl, toEmail,
		"Your subscription is still active", data)
}

// renderEmail executes pre-parsed templates against data. Templates are
// loaded once at package init (see cancelHTMLTpl/keptHTMLTpl/etc above) so
// the per-Send cost is just two Execute calls and a couple of buffer copies.
func renderEmail(htmlTpl *template.Template, txtTpl *texttemplate.Template, to, subject string, data interface{}) (email.Message, error) {
	var htmlBuf, txtBuf bytes.Buffer
	if err := htmlTpl.Execute(&htmlBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute html: %w", err)
	}
	if err := txtTpl.Execute(&txtBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute text: %w", err)
	}
	return email.Message{
		To:      to,
		Subject: subject,
		HTML:    htmlBuf.String(),
		Text:    txtBuf.String(),
	}, nil
}
