// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/events"
	"github.com/andrebrait/netplane/internal/model"
	"github.com/andrebrait/netplane/internal/poller"
	"github.com/andrebrait/netplane/internal/store"
)

type apiMockDriver struct{}

func (apiMockDriver) Kind() string                             { return "mock" }
func (apiMockDriver) ValidateConfig(driver.TargetConfig) error { return nil }
func (apiMockDriver) Poll(_ context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	now := time.Now().UTC()
	return &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
		Devices: []model.Device{{
			ID: "mock:02:51:0a:00:00:01", Driver: "mock", TargetID: cfg.ID,
			Name: "LG510S", Role: model.RoleSwitch, PrimaryMAC: "02:51:0a:00:00:01",
			Online: true, LastSeen: now, Capabilities: []model.Capability{model.CapLinks, model.CapFDB, model.CapDiagPing},
		}},
		Links: []model.Link{{
			DeviceID: "mock:02:51:0a:00:00:01", Index: 1, IfName: "lan1",
			Media: model.Media25GE, AdminUp: true, LinkUp: true, SpeedMbps: 2500,
			Counters: &model.PortCounters{RxBytes: 4000, TxBytes: 8000},
		}},
		FDB: []model.FdbEntry{{
			DeviceID: "mock:02:51:0a:00:00:01", LinkName: "lan1", MAC: "de:ad:be:ef:00:01", VLAN: 1,
		}},
		Neighbors: []model.NeighborEntry{{
			DeviceID: "mock:02:51:0a:00:00:01", Family: model.IPv4, IP: "192.0.2.55", MAC: "de:ad:be:ef:00:01", State: "reachable",
		}},
		Clients: []model.Client{{
			MAC: "de:ad:be:ef:00:01", IPs: []string{"192.0.2.55"}, Hostname: "nas",
			Online: true, ConnectionType: "wired", AttachedDeviceID: "mock:02:51:0a:00:00:01", AttachedLink: "lan1",
		}},
	}, nil
}

func (apiMockDriver) RunDiagnostic(_ context.Context, _ driver.TargetConfig, req model.DiagnosticRequest) (*model.DiagnosticResult, error) {
	return &model.DiagnosticResult{
		Kind:       req.Kind,
		DeviceID:   req.DeviceID,
		Target:     req.Target,
		OK:         true,
		DurationMs: 7,
		Summary:    "3/3 received",
	}, nil
}

func newTestServer(t *testing.T, token string) (*Server, *events.Bus) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := driver.NewRegistry()
	_ = reg.Register(apiMockDriver{})
	bus := events.NewBus()
	sched := poller.New(reg, st, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	target := driver.TargetConfig{
		ID:              "t1",
		Name:            "Lab Switch",
		Driver:          "mock",
		Endpoint:        "http://192.0.2.10",
		Enabled:         true,
		PollInterval:    15 * time.Second,
		ResolvedSecrets: map[string]string{"password": "never-leak-this-secret"},
	}
	if err := sched.PollOnce(ctx, target); err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	webFS := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><head><title>netplane</title></head><body>ok</body></html>")},
		"app.js":     {Data: []byte("console.log('ok');")},
	}

	srv := New(Options{
		Version:           "0.1.0-test",
		Store:             st,
		Registry:          reg,
		Poller:            sched,
		Bus:               bus,
		Targets:           []driver.TargetConfig{target},
		APIToken:          token,
		TrustIngressProxy: true,
		WebFS:             webFS,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, bus
}

func TestAPIEndpointsAndIngressAndAuth(t *testing.T) {
	srv, _ := newTestServer(t, "secret-api-tok")

	// Health is open without token.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}

	// Protected route rejects missing token with 401.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/devices", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// Authorized bearer token succeeds.
	req := httptest.NewRequest("GET", "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer secret-api-tok")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "LG510S") {
		t.Fatalf("devices = %d %s", rec.Code, rec.Body.String())
	}

	// Home Assistant Ingress prefix works and injects <base href> for SPA.
	req = httptest.NewRequest("GET", "/api/hassio_ingress/abc123/api/v1/devices/mock:02:51:0a:00:00:01", nil)
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc123")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"if_name":"lan1"`) {
		t.Fatalf("ingress device detail = %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/hassio_ingress/abc123/topology", nil)
	req.Header.Set("X-Ingress-Path", "/api/hassio_ingress/abc123")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<base href="/api/hassio_ingress/abc123/">`) {
		t.Fatalf("ingress SPA = %d %s", rec.Code, rec.Body.String())
	}

	// Targets endpoint MUST NOT leak ResolvedSecrets.
	req = httptest.NewRequest("GET", "/api/v1/targets", nil)
	req.Header.Set("Authorization", "Bearer secret-api-tok")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "never-leak-this-secret") {
		t.Fatalf("targets leaked secret or failed: %d %s", rec.Code, rec.Body.String())
	}

	// OpenAPI, state, topology, clients, link samples, manual poll.
	for _, path := range []string{
		"/api/v1/openapi.json",
		"/api/v1/state",
		"/api/v1/topology",
		"/api/v1/clients?online=true&q=nas",
		"/api/v1/devices/mock:02:51:0a:00:00:01/links/lan1/samples?since=1h",
	} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer secret-api-tok")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body.String())
		}
	}

	// Trigger immediate poll.
	r := httptest.NewRequest("POST", "/api/v1/targets/t1/poll", nil)
	r.Header.Set("Authorization", "Bearer secret-api-tok")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("POST poll = %d %s", w.Code, w.Body.String())
	}
}

func TestDiagnosticsFdbLookupAndDeviceRPC(t *testing.T) {
	srv, _ := newTestServer(t, "")

	// Cross-vendor built-in fdb_lookup by IP resolves to MAC + switch port.
	body := bytes.NewBufferString(`{"kind":"fdb_lookup","target":"192.0.2.55"}`)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/diagnostics", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("fdb_lookup = %d %s", rec.Code, rec.Body.String())
	}
	var res model.DiagnosticResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !res.OK {
		t.Fatalf("expected OK fdb_lookup result, got (%+v, %v)", res, err)
	}

	// Device-side ping delegates to DiagnosticDriver.
	body = bytes.NewBufferString(`{"kind":"ping","device_id":"mock:02:51:0a:00:00:01","target":"1.1.1.1"}`)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/diagnostics", body))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "3/3 received") {
		t.Fatalf("ping diag = %d %s", rec.Code, rec.Body.String())
	}

	// History lists both diagnostic runs.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/diagnostics?limit=10", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fdb_lookup") {
		t.Fatalf("list diagnostics = %d %s", rec.Code, rec.Body.String())
	}

	// Error & edge paths: static asset, unknown API route, 404 device, 404 target, bad since, bad JSON, missing fdb_lookup.
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/app.js", "", http.StatusOK},
		{"GET", "/api/v1/nope", "", http.StatusNotFound},
		{"GET", "/api/v1/devices/missing", "", http.StatusNotFound},
		{"POST", "/api/v1/targets/missing/poll", "", http.StatusNotFound},
		{"GET", "/api/v1/devices/mock:02:51:0a:00:00:01/links/lan1/samples?since=notaduration", "", http.StatusBadRequest},
		{"POST", "/api/v1/diagnostics", `{bad json`, http.StatusBadRequest},
		{"POST", "/api/v1/diagnostics", `{"kind":"ping","target":"1.1.1.1"}`, http.StatusBadRequest},
		{"POST", "/api/v1/diagnostics", `{"kind":"ping","device_id":"missing","target":"1.1.1.1"}`, http.StatusNotFound},
		{"POST", "/api/v1/diagnostics", `{"kind":"fdb_lookup","target":"nas"}`, http.StatusOK},
		{"POST", "/api/v1/diagnostics", `{"kind":"fdb_lookup","target":"00:00:00:00:00:99"}`, http.StatusOK},
	} {
		var rBody io.Reader
		if tc.body != "" {
			rBody = strings.NewReader(tc.body)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, rBody))
		if w.Code != tc.want {
			t.Errorf("%s %s = %d, want %d (body: %s)", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestSSEEventStream(t *testing.T) {
	srv, bus := newTestServer(t, "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/events/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect SSE: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	// Read initial ": connected" line.
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatalf("initial SSE line = (%q, %v)", line, err)
	}

	// Publish an event and verify it streams through.
	bus.Publish(events.Event{Type: events.LinkDown, Severity: events.SeverityWarn, Summary: "lan1 down"})

	gotEvent := false
	for i := 0; i < 6; i++ {
		l, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE: %v", err)
		}
		if strings.Contains(l, "lan1 down") {
			gotEvent = true
			break
		}
	}
	if !gotEvent {
		t.Fatal("did not receive published SSE event")
	}
}
