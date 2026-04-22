package handler

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/btc/drill/internal/branding"
)

// ogRoute defines OG meta tag content for a public route.
type ogRoute struct {
	title       string
	description string
	image       string // path relative to base URL
}

var ogRoutes = map[string]ogRoute{
	"/": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/about": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/sample": {
		title:       branding.AppName + " — sample evaluation",
		description: "See a real system design interview evaluated across 5 dimensions",
		image:       "/og-sample.png",
	},
}

// SPAHandler serves the embedded SPA. Static assets served directly.
// All other paths return index.html for client-side routing.
// For paths with OG tags defined, the tags are injected before </head>.
// baseURL is the public URL (e.g., "https://sabermatic.dev") used for
// absolute og:url and og:image values. Pass "" for tests.
func SPAHandler(fsys fs.FS, baseURL string) (http.Handler, error) {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("embed sub: %w", err)
	}

	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}
	indexHTML := string(indexBytes)

	if !strings.Contains(indexHTML, "</head>") {
		slog.Warn("index.html missing </head> — OG tags will not be injected")
	}

	// Pre-compute OG-injected HTML at init time
	ogPages := make(map[string][]byte, len(ogRoutes))
	for path, og := range ogRoutes {
		tags := fmt.Sprintf(
			`<meta property="og:title" content="%s">`+
				`<meta property="og:description" content="%s">`+
				`<meta property="og:type" content="website">`+
				`<meta property="og:url" content="%s%s">`+
				`<meta property="og:image" content="%s%s">`+
				`<meta name="twitter:card" content="summary_large_image">`+
				`<meta name="twitter:title" content="%s">`+
				`<meta name="twitter:description" content="%s">`+
				`<meta name="twitter:image" content="%s%s">`,
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(path),
			html.EscapeString(baseURL), html.EscapeString(og.image),
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(og.image),
		)
		ogPages[path] = []byte(strings.Replace(indexHTML, "</head>", tags+"</head>", 1))
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
				// Hashed asset files are immutable and can be cached forever
				if strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Serve pre-computed OG-injected HTML if this path has OG tags
		if body, ok := ogPages[path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}), nil
}
