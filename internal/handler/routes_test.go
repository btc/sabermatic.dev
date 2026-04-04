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

	h := handler.SPAHandler(fsys)

	tests := []struct {
		path    string
		wantTag string
	}{
		{"/", `og:title" content="Sabermetric"`},
		{"/sample", `og:title" content="Sabermetric — sample evaluation"`},
		{"/login", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			body := w.Body.String()
			if tt.wantTag != "" && !strings.Contains(body, tt.wantTag) {
				t.Errorf("expected OG tag %q in body", tt.wantTag)
			}
			if tt.wantTag == "" && strings.Contains(body, "og:title") {
				t.Errorf("unexpected OG tag in body")
			}
		})
	}
}
