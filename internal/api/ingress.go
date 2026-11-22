// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"crypto/subtle"
	"html"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"
)

// DefaultTrustedIngressCIDRs matches Home Assistant Supervisor (172.30.32.2) and loopback.
var DefaultTrustedIngressCIDRs = []string{
	"172.30.32.2/32",
	"127.0.0.0/8",
	"::1/128",
}

func parseTrustedCIDRs(raw []string) []netip.Prefix {
	if len(raw) == 0 {
		raw = DefaultTrustedIngressCIDRs
	}
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
			continue
		}
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func (s *Server) isTrustedIngressPeer(remoteAddr string) bool {
	if !s.opts.TrustIngressProxy || len(s.trustedIngress) == 0 {
		return false
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range s.trustedIngress {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// sanitizeIngressPath validates X-Ingress-Path as a safe, single-leading-slash relative path
// (rejecting protocol-relative "//evil", full URLs, path traversal, or HTML/control chars).
func sanitizeIngressPath(raw string) string {
	p := strings.TrimRight(strings.TrimSpace(raw), "/")
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	if strings.ContainsAny(p, " \t\r\n\"'<>\\?:;#") || strings.Contains(p, "..") {
		return ""
	}
	return p
}

// withIngress strips a validated X-Ingress-Path prefix when the request arrives from a
// trusted Home Assistant Supervisor / loopback peer.
func (s *Server) withIngress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isTrustedIngressPeer(r.RemoteAddr) {
			if ingressPath := sanitizeIngressPath(r.Header.Get("X-Ingress-Path")); ingressPath != "" &&
				(r.URL.Path == ingressPath || strings.HasPrefix(r.URL.Path, ingressPath+"/")) {
				r2 := r.Clone(r.Context())
				trimmed := strings.TrimPrefix(r.URL.Path, ingressPath)
				if trimmed == "" {
					trimmed = "/"
				}
				r2.URL.Path = trimmed
				r2.URL.RawPath = ""
				next.ServeHTTP(w, r2)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withAuth enforces bearer token authentication on /api/v1/* when APIToken is configured.
// Health checks remain readable; Home Assistant Ingress requests are trusted only when both
// the peer IP matches trustedIngress CIDRs AND a valid X-Ingress-Path header is present.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.APIToken == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		if s.isTrustedIngressPeer(r.RemoteAddr) && sanitizeIngressPath(r.Header.Get("X-Ingress-Path")) != "" {
			next.ServeHTTP(w, r)
			return
		}
		scheme, token, hasSpace := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		if !hasSpace || !strings.EqualFold(scheme, "bearer") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.opts.APIToken)) != 1 {
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

	if cleaned != "index.html" {
		if f, err := s.opts.WebFS.Open(cleaned); err == nil {
			_ = f.Close()
			http.FileServerFS(s.opts.WebFS).ServeHTTP(w, r)
			return
		}
	}

	indexBytes, err := fs.ReadFile(s.opts.WebFS, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "UI bundle missing index.html")
		return
	}
	baseHref := "./"
	if s.isTrustedIngressPeer(r.RemoteAddr) {
		if ingressPath := sanitizeIngressPath(r.Header.Get("X-Ingress-Path")); ingressPath != "" {
			baseHref = html.EscapeString(ingressPath) + "/"
		}
	}
	body := strings.Replace(string(indexBytes), "<head>", `<head><base href="`+baseHref+`">`, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
