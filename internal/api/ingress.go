// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"crypto/subtle"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// withIngress strips an optional X-Ingress-Path prefix (sent by Home Assistant Supervisor)
// so all routes resolve cleanly both standalone and behind /api/hassio_ingress/<token>/.
func (s *Server) withIngress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ingressPath := strings.TrimRight(strings.TrimSpace(r.Header.Get("X-Ingress-Path")), "/")
		if ingressPath != "" && strings.HasPrefix(r.URL.Path, ingressPath) {
			r2 := r.Clone(r.Context())
			trimmed := strings.TrimPrefix(r.URL.Path, ingressPath)
			if trimmed == "" {
				trimmed = "/"
			}
			r2.URL.Path = trimmed
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withAuth enforces bearer token authentication on /api/v1/* when APIToken is configured.
// Health checks remain readable; Home Assistant Ingress requests are allowed when TrustIngressProxy is enabled.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.APIToken == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		if s.opts.TrustIngressProxy && r.Header.Get("X-Ingress-Path") != "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if auth == token || subtle.ConstantTimeCompare([]byte(token), []byte(s.opts.APIToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "valid bearer token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown API endpoint")
		return
	}
	if s.opts.WebFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>netplane</title></head><body><h1>netplane</h1><p>API server running. OpenAPI contract available at <a href="./api/v1/openapi.json">./api/v1/openapi.json</a>.</p></body></html>`))
		return
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if cleaned == "" {
		cleaned = "index.html"
	}

	// Serve static asset directly if present in WebFS.
	if cleaned != "index.html" {
		if f, err := s.opts.WebFS.Open(cleaned); err == nil {
			_ = f.Close()
			http.FileServerFS(s.opts.WebFS).ServeHTTP(w, r)
			return
		}
	}

	// Client-side SPA fallback to index.html with Ingress-safe <base href="...">.
	indexBytes, err := fs.ReadFile(s.opts.WebFS, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "UI bundle missing index.html")
		return
	}
	baseHref := "./"
	if ingressPath := strings.TrimRight(strings.TrimSpace(r.Header.Get("X-Ingress-Path")), "/"); ingressPath != "" {
		baseHref = html.EscapeString(ingressPath) + "/"
	}
	body := strings.Replace(string(indexBytes), "<head>", `<head><base href="`+baseHref+`">`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
