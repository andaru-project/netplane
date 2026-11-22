// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// NewHTTPClient returns a per-poll HTTP client honouring the insecure_skip_verify setting.
// It refuses redirects to another host so API key headers and login bodies stay on the target.
// Callers must call CloseIdleConnections when done.
func NewHTTPClient(cfg TargetConfig, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.BoolSetting("insecure_skip_verify", false)}, //nolint:gosec // user-configurable for local self-signed devices
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing redirect from %s to different host %s", via[0].URL.Host, req.URL.Host)
			}
			return nil
		},
	}
}
