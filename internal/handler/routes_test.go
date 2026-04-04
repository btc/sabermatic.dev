package handler_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/btc/drill/internal/handler"
)

func TestSPAHandlerOGTags(t *testing.T) {
	indexHTML := `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body></body></html>`
	fsys := fstest.MapFS{
		"web/dist/index.html": &fstest.MapFile{Data: []byte(indexHTML)},
	}

	h := handler.SPAHandler(fsys, "https://sabermetric.dev")

	tests := []struct {
		path     string
		wantTags []string // all must be present; empty means no OG tags
	}{
		{"/", []string{
			`og:title" content="Sabermetric"`,
			`og:description" content="data-driven system design prep"`,
			`og:url" content="https://sabermetric.dev/"`,
			`og:image" content="https://sabermetric.dev/og-landing.png"`,
		}},
		{"/sample", []string{
			`og:title" content="Sabermetric — sample evaluation"`,
			`og:url" content="https://sabermetric.dev/sample"`,
			`og:image" content="https://sabermetric.dev/og-sample.png"`,
		}},
		{"/login", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			body := w.Body.String()

			if len(tt.wantTags) > 0 {
				// Verify OG tags appear before </head>
				headClose := strings.Index(body, "</head>")
				for _, tag := range tt.wantTags {
					pos := strings.Index(body, tag)
					if pos < 0 {
						t.Errorf("expected OG tag %q in body", tag)
					} else if pos > headClose {
						t.Errorf("OG tag %q appears after </head>", tag)
					}
				}
			} else {
				if strings.Contains(body, "og:title") {
					t.Errorf("unexpected OG tag in body")
				}
			}
		})
	}
}
