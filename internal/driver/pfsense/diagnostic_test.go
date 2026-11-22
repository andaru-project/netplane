// SPDX-License-Identifier: AGPL-3.0-or-later
package pfsense

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestPfSensePing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/diagnostics/ping" || r.Header.Get("X-API-Key") != "k" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["host"] != "192.0.2.1" || body["count"] != float64(3) {
			t.Errorf("body = %v", body)
		}
		_, _ = w.Write([]byte(`{"code":200,"status":"ok","data":{"output":"3 packets transmitted, 3 packets received, 0.0% packet loss\nround-trip min/avg/max/stddev = 0.2/0.3/0.4/0.1 ms\n","result_code":0}}`))
	}))
	defer srv.Close()
	cfg := driver.TargetConfig{ID: "pf", Endpoint: srv.URL, Settings: map[string]any{"allow_insecure_http": true}, ResolvedSecrets: map[string]string{"api_key": "k"}}
	d := New()
	res, err := d.RunDiagnostic(context.Background(), cfg, model.DiagnosticRequest{Kind: model.DiagPing, Target: "192.0.2.1"})
	if err != nil || !res.OK || res.Summary != "3/3 replies from 192.0.2.1 (0% loss), avg 0.3 ms" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := d.RunDiagnostic(context.Background(), cfg, model.DiagnosticRequest{Kind: model.DiagTraceroute, Target: "192.0.2.1"}); !errors.Is(err, driver.ErrDiagnosticUnsupported) {
		t.Errorf("traceroute err = %v", err)
	}
	// A REST API older than v2.7.0 has no ping route.
	cfg.ResolvedSecrets = map[string]string{"api_key": "other"}
	if _, err := d.RunDiagnostic(context.Background(), cfg, model.DiagnosticRequest{Kind: model.DiagPing, Target: "192.0.2.1"}); err == nil {
		t.Error("expected an error for HTTP 404")
	}
}
