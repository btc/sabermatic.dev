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
		CurrentPeriodEnd: periodEnd.Format("January 2, 2006"),
		KeepLink:         keepURL,
	}
	return renderEmail("cancel", toEmail, "We won't charge you for the next period", data)
}

func composeKeptEmail(toEmail, displayName string, nextRenewal time.Time) (email.Message, error) {
	data := keptEmailData{
		DisplayName:     displayName,
		NextRenewalDate: nextRenewal.Format("January 2, 2006"),
	}
	return renderEmail("kept", toEmail, "Your subscription is still active", data)
}

func renderEmail(name, to, subject string, data interface{}) (email.Message, error) {
	htmlT, err := template.ParseFS(templatesFS, "templates/"+name+".html.tmpl")
	if err != nil {
		return email.Message{}, fmt.Errorf("parse %s.html: %w", name, err)
	}
	txtT, err := texttemplate.ParseFS(templatesFS, "templates/"+name+".txt.tmpl")
	if err != nil {
		return email.Message{}, fmt.Errorf("parse %s.txt: %w", name, err)
	}
	var htmlBuf, txtBuf bytes.Buffer
	if err := htmlT.Execute(&htmlBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute %s.html: %w", name, err)
	}
	if err := txtT.Execute(&txtBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute %s.txt: %w", name, err)
	}
	return email.Message{
		To:      to,
		Subject: subject,
		HTML:    htmlBuf.String(),
		Text:    txtBuf.String(),
	}, nil
}
