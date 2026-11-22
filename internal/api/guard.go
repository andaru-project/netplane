// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// privateSuffixes are name suffixes no public DNS zone can serve, so a malicious site cannot
// make a browser send them as Host (DNS rebinding).
var privateSuffixes = []string{".local", ".lan", ".home.arpa", ".internal"}

// withBrowserGuards protects against a malicious web page in the operator's browser:
//   - a write (any method but GET, HEAD, OPTIONS) whose Origin names another host is refused
//     (cross-site request forgery: text/plain POSTs need no preflight);
//   - without an API token, a request whose Host is a public DNS name is refused, since only a
//     DNS-rebinding page would reach this server under such a name.
//
// Requests from the trusted ingress proxy are exempt: Supervisor sets their headers.
func (s *Server) withBrowserGuards(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isTrustedIngressPeer(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "CROSS_ORIGIN", "cross-origin request refused")
					return
				}
			}
		}
		if s.opts.APIToken == "" && !s.allowedHost(r.Host) {
			writeError(w, http.StatusForbidden, "HOST_NOT_ALLOWED",
				"host name not allowed without an API token; add it to server.allowed_hosts or set a token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHost accepts IP literals, single-label names, private suffixes and allowed_hosts.
func (s *Server) allowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil || !strings.Contains(host, ".") {
		return true
	}
	for _, suf := range privateSuffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	for _, h := range s.opts.AllowedHosts {
		if strings.EqualFold(strings.TrimSuffix(h, "."), host) {
			return true
		}
	}
	return false
}
