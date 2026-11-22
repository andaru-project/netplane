// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/events"
	"github.com/andrebrait/netplane/internal/model"
	"github.com/andrebrait/netplane/internal/poller"
	"github.com/andrebrait/netplane/internal/store"
	"github.com/andrebrait/netplane/internal/topology"
	"github.com/andrebrait/netplane/schema"
)

// Options configures the HTTP/SSE API server.
type Options struct {
	Version           string
	Store             *store.Store
	Registry          *driver.Registry
	Poller            *poller.Scheduler
	Bus               *events.Bus
	Targets           []driver.TargetConfig
	APIToken          string
	TrustIngressProxy bool
	WebFS             fs.FS // optional embedded SPA root containing index.html
	Logger            *slog.Logger
}

// DeviceDetail aggregates a device with all its associated observed entities.
type DeviceDetail struct {
	Device            model.Device             `json:"device"`
	Links             []model.Link             `json:"links"`
	LagGroups         []model.LagGroup         `json:"lag_groups"`
	Segments          []model.Segment          `json:"segments"`
	Zones             []model.Zone             `json:"zones"`
	Gateways          []model.GatewayStatus    `json:"gateways"`
	Routes            []model.Route            `json:"routes"`
	VPNs              []model.VpnTunnel        `json:"vpns"`
	Firewall          *model.FirewallState     `json:"firewall,omitempty"`
	Radios            []model.WirelessRadio    `json:"radios"`
	WirelessUplinks   []model.WirelessUplink   `json:"wireless_uplinks"`
	Clients           []model.Client           `json:"clients"`
	RecentDiagnostics []model.DiagnosticResult `json:"recent_diagnostics"`
}

// Server serves the netplane REST API, SSE stream, OpenAPI contract, and embedded SPA.
type Server struct {
	opts      Options
	mux       *http.ServeMux
	handler   http.Handler
	targetsMu sync.RWMutex
	targets   map[string]driver.TargetConfig
}

// New constructs a Server ready to mount on an http.Server.
func New(opts Options) *Server {
	if opts.Version == "" {
		opts.Version = "0.1.0-dev"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	s := &Server{
		opts:    opts,
		mux:     http.NewServeMux(),
		targets: make(map[string]driver.TargetConfig, len(opts.Targets)),
	}
	for _, t := range opts.Targets {
		s.targets[t.ID] = t
	}

	s.registerRoutes()
	s.handler = s.withIngress(s.withAuth(s.mux))
	return s
}

// ServeHTTP delegates to the wrapped middleware chain.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/openapi.json", s.handleOpenAPI)
	s.mux.HandleFunc("GET /api/v1/state", s.handleState)
	s.mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	s.mux.HandleFunc("GET /api/v1/devices/{id}", s.handleGetDevice)
	s.mux.HandleFunc("GET /api/v1/devices/{id}/links/{ifName}/samples", s.handleLinkSamples)
	s.mux.HandleFunc("GET /api/v1/topology", s.handleTopology)
	s.mux.HandleFunc("GET /api/v1/clients", s.handleClients)
	s.mux.HandleFunc("GET /api/v1/targets", s.handleListTargets)
	s.mux.HandleFunc("POST /api/v1/targets/{id}/poll", s.handlePollTarget)
	s.mux.HandleFunc("GET /api/v1/diagnostics", s.handleListDiagnostics)
	s.mux.HandleFunc("POST /api/v1/diagnostics", s.handleRunDiagnostic)
	s.mux.HandleFunc("GET /api/v1/events/stream", s.handleEventStream)
	s.mux.HandleFunc("/", s.handleSPA)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	statuses := map[string]poller.TargetStatus{}
	if s.opts.Poller != nil {
		statuses = s.opts.Poller.Status()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": s.opts.Version,
		"targets": statuses,
	})
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(schema.OpenAPIJSON)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	state, err := s.opts.Store.LoadState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	state, err := s.opts.Store.LoadState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": state.Devices})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	state, err := s.opts.Store.LoadState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	var found *model.Device
	for i := range state.Devices {
		if state.Devices[i].ID == id {
			found = &state.Devices[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("device %q not found", id))
		return
	}

	detail := DeviceDetail{
		Device:            *found,
		Links:             []model.Link{},
		LagGroups:         []model.LagGroup{},
		Segments:          []model.Segment{},
		Zones:             []model.Zone{},
		Gateways:          []model.GatewayStatus{},
		Routes:            []model.Route{},
		VPNs:              []model.VpnTunnel{},
		Radios:            []model.WirelessRadio{},
		WirelessUplinks:   []model.WirelessUplink{},
		Clients:           []model.Client{},
		RecentDiagnostics: []model.DiagnosticResult{},
	}
	for _, l := range state.Links {
		if l.DeviceID == id {
			detail.Links = append(detail.Links, l)
		}
	}
	for _, g := range state.LagGroups {
		if g.DeviceID == id {
			detail.LagGroups = append(detail.LagGroups, g)
		}
	}
	for _, seg := range state.Segments {
		if seg.DeviceID == id {
			detail.Segments = append(detail.Segments, seg)
		}
	}
	for _, z := range state.Zones {
		if z.DeviceID == id {
			detail.Zones = append(detail.Zones, z)
		}
	}
	for _, gw := range state.Gateways {
		if gw.DeviceID == id {
			detail.Gateways = append(detail.Gateways, gw)
		}
	}
	for _, rt := range state.Routes {
		if rt.DeviceID == id {
			detail.Routes = append(detail.Routes, rt)
		}
	}
	for _, v := range state.VPNs {
		if v.DeviceID == id {
			detail.VPNs = append(detail.VPNs, v)
		}
	}
	for i := range state.Firewalls {
		if state.Firewalls[i].DeviceID == id {
			fw := state.Firewalls[i]
			detail.Firewall = &fw
			break
		}
	}
	for _, rad := range state.Radios {
		if rad.DeviceID == id {
			detail.Radios = append(detail.Radios, rad)
		}
	}
	for _, u := range state.WirelessUplinks {
		if u.DeviceID == id {
			detail.WirelessUplinks = append(detail.WirelessUplinks, u)
		}
	}
	for _, c := range state.Clients {
		if c.AttachedDeviceID == id {
			detail.Clients = append(detail.Clients, c)
		}
	}
	if runs, err := s.opts.Store.ListDiagnosticRuns(r.Context(), id, 10); err == nil {
		detail.RecentDiagnostics = runs
	}

	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleLinkSamples(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	ifName := r.PathValue("ifName")
	since := time.Now().UTC().Add(-1 * time.Hour)
	if raw := r.URL.Query().Get("since"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			since = time.Now().UTC().Add(-d)
		} else if ts, err := time.Parse(time.RFC3339, raw); err == nil {
			since = ts.UTC()
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_PARAM", "since must be a duration (e.g. 1h) or RFC3339 timestamp")
			return
		}
	}

	samples, err := s.opts.Store.QueryLinkSamples(r.Context(), deviceID, ifName, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": samples})
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	if raw, ok, err := s.opts.Store.LoadTopologyRaw(r.Context()); err == nil && ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	state, err := s.opts.Store.LoadState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, topology.Solve(state, time.Now().UTC()))
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	state, err := s.opts.Store.LoadState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	onlineOnly := r.URL.Query().Get("online") == "true"
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	out := []model.Client{}
	for _, c := range state.Clients {
		if onlineOnly && !c.Online {
			continue
		}
		if q != "" {
			match := strings.Contains(strings.ToLower(c.MAC), q) ||
				strings.Contains(strings.ToLower(c.Hostname), q) ||
				strings.Contains(strings.ToLower(c.SSID), q)
			for _, ip := range c.IPs {
				if strings.Contains(strings.ToLower(ip), q) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handleListTargets(w http.ResponseWriter, _ *http.Request) {
	s.targetsMu.RLock()
	items := make([]driver.TargetConfig, 0, len(s.targets))
	for _, t := range s.targets {
		t.ResolvedSecrets = nil // belt-and-suspenders redaction
		items = append(items, t)
	}
	s.targetsMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handlePollTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.targetsMu.RLock()
	cfg, ok := s.targets[id]
	s.targetsMu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("target %q not found", id))
		return
	}
	if s.opts.Poller == nil {
		writeError(w, http.StatusServiceUnavailable, "POLLER_UNAVAILABLE", "poller not initialized")
		return
	}
	if err := s.opts.Poller.PollOnce(r.Context(), cfg); err != nil {
		writeError(w, http.StatusBadGateway, "POLL_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Poller.Status()[id])
}

func (s *Server) handleListDiagnostics(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	runs, err := s.opts.Store.ListDiagnosticRuns(r.Context(), deviceID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (s *Server) handleRunDiagnostic(w http.ResponseWriter, r *http.Request) {
	var req model.DiagnosticRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}
	if req.Kind == "" || strings.TrimSpace(req.Target) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "kind and target are required")
		return
	}

	started := time.Now().UTC()

	// Built-in cross-vendor fdb_lookup resolves any MAC or IP against merged state.
	if req.Kind == model.DiagFdbLookup {
		res, err := s.runFdbLookup(r.Context(), req, started)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "DIAG_ERROR", err.Error())
			return
		}
		_ = s.opts.Store.SaveDiagnosticRun(r.Context(), *res)
		writeJSON(w, http.StatusOK, res)
		return
	}

	if req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "device_id is required for device-side diagnostics")
		return
	}
	dev, ok, err := s.opts.Store.GetDevice(r.Context(), req.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("device %q not found", req.DeviceID))
		return
	}
	s.targetsMu.RLock()
	cfg, hasTarget := s.targets[dev.TargetID]
	s.targetsMu.RUnlock()
	if !hasTarget {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("target %q for device %q not found", dev.TargetID, req.DeviceID))
		return
	}
	drv, hasDrv := s.opts.Registry.Get(cfg.Driver)
	if !hasDrv {
		writeError(w, http.StatusNotImplemented, "DRIVER_UNAVAILABLE", fmt.Sprintf("driver %q not registered", cfg.Driver))
		return
	}
	diagDrv, ok := drv.(driver.DiagnosticDriver)
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNSUPPORTED", fmt.Sprintf("driver %q does not support %s", cfg.Driver, req.Kind))
		return
	}

	res, err := diagDrv.RunDiagnostic(r.Context(), cfg, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "DIAG_FAILED", err.Error())
		return
	}
	if res.ID == "" {
		res.ID = fmt.Sprintf("diag-%d", started.UnixNano())
	}
	if res.StartedAt.IsZero() {
		res.StartedAt = started
	}
	if res.DurationMs == 0 {
		res.DurationMs = time.Since(started).Milliseconds()
	}
	_ = s.opts.Store.SaveDiagnosticRun(r.Context(), *res)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) runFdbLookup(ctx context.Context, req model.DiagnosticRequest, started time.Time) (*model.DiagnosticResult, error) {
	state, err := s.opts.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}

	query := strings.TrimSpace(req.Target)
	targetMAC, macErr := model.NormalizeMAC(query)
	matchedIPs := []string{}
	if macErr != nil {
		// Treat query as IP or hostname; resolve MAC from Neighbors, DHCP, or Clients.
		qLower := strings.ToLower(query)
		for _, n := range state.Neighbors {
			if strings.EqualFold(n.IP, query) && n.MAC != "" {
				targetMAC = n.MAC
				matchedIPs = append(matchedIPs, n.IP)
				break
			}
		}
		if targetMAC == "" {
			for _, l := range state.DHCP {
				if (strings.EqualFold(l.IP, query) || strings.EqualFold(l.Hostname, qLower)) && l.MAC != "" {
					targetMAC = l.MAC
					matchedIPs = append(matchedIPs, l.IP)
					break
				}
			}
		}
		if targetMAC == "" {
			for _, c := range state.Clients {
				if strings.EqualFold(c.Hostname, qLower) {
					targetMAC = c.MAC
					matchedIPs = append(matchedIPs, c.IPs...)
					break
				}
				for _, ip := range c.IPs {
					if strings.EqualFold(ip, query) {
						targetMAC = c.MAC
						matchedIPs = append(matchedIPs, ip)
						break
					}
				}
			}
		}
	}

	fdbHits := []model.FdbEntry{}
	clientHits := []model.Client{}
	if targetMAC != "" {
		for _, f := range state.FDB {
			if f.MAC == targetMAC {
				fdbHits = append(fdbHits, f)
			}
		}
		for _, c := range state.Clients {
			if c.MAC == targetMAC {
				clientHits = append(clientHits, c)
			}
		}
	}

	found := targetMAC != "" && (len(fdbHits) > 0 || len(clientHits) > 0)
	summary := fmt.Sprintf("No FDB/client match found for %q", query)
	if found {
		summary = fmt.Sprintf("MAC %s matched %d FDB entries and %d client records", targetMAC, len(fdbHits), len(clientHits))
	}

	return &model.DiagnosticResult{
		ID:         fmt.Sprintf("diag-%d", started.UnixNano()),
		Kind:       model.DiagFdbLookup,
		DeviceID:   req.DeviceID,
		Target:     query,
		OK:         found,
		StartedAt:  started,
		DurationMs: time.Since(started).Milliseconds(),
		Summary:    summary,
		Structured: map[string]any{
			"mac":     targetMAC,
			"ips":     matchedIPs,
			"fdb":     fdbHits,
			"clients": clientHits,
		},
	}, nil
}
