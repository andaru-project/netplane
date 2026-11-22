// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/andrebrait/netplane/internal/model"
)

// TargetConfig describes one configured device or controller target to poll.
// ResolvedSecrets holds runtime-resolved secret values and is never serialized to JSON or logged.
type TargetConfig struct {
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Driver          string                     `json:"driver"`
	Endpoint        string                     `json:"endpoint"`
	Enabled         bool                       `json:"enabled"`
	PollInterval    time.Duration              `json:"poll_interval"`
	Settings        map[string]any             `json:"settings,omitempty"`
	SecretRefs      map[string]model.SecretRef `json:"secret_refs,omitempty"`
	ResolvedSecrets map[string]string          `json:"-"`
}

// LogValue implements slog.LogValuer, redacting all resolved secret values.
func (c TargetConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", c.ID),
		slog.String("name", c.Name),
		slog.String("driver", c.Driver),
		slog.String("endpoint", c.Endpoint),
		slog.Bool("enabled", c.Enabled),
		slog.Duration("poll_interval", c.PollInterval),
		slog.Int("secret_count", len(c.ResolvedSecrets)),
	)
}

// Snapshot is an atomic point-in-time observed/live read returned by a Driver.
// One TargetID (for example, a UniFi controller) may yield multiple devices.
type Snapshot struct {
	TargetID        string                 `json:"target_id"`
	CollectedAt     time.Time              `json:"collected_at"`
	Devices         []model.Device         `json:"devices"`
	Links           []model.Link           `json:"links"`
	LagGroups       []model.LagGroup       `json:"lag_groups"`
	Segments        []model.Segment        `json:"segments"`
	Zones           []model.Zone           `json:"zones"`
	FDB             []model.FdbEntry       `json:"fdb"`
	LLDP            []model.LldpNeighbor   `json:"lldp"`
	Neighbors       []model.NeighborEntry  `json:"neighbors"`
	DHCP            []model.DhcpLease      `json:"dhcp"`
	Gateways        []model.GatewayStatus  `json:"gateways"`
	Routes          []model.Route          `json:"routes"`
	VPNs            []model.VpnTunnel      `json:"vpns"`
	Firewalls       []model.FirewallState  `json:"firewalls"`
	Radios          []model.WirelessRadio  `json:"radios"`
	WirelessUplinks []model.WirelessUplink `json:"wireless_uplinks"`
	Clients         []model.Client         `json:"clients"`
}

// Normalize canonicalizes MAC addresses, SFP alert defaults, and non-nil slices across the snapshot.
// A malformed MAC (e.g. a 4-byte tunnel address) never fails the poll: optional MAC fields are
// cleared, and FDB/client entries, which are keyed by MAC, are dropped.
func (s *Snapshot) Normalize() {
	th := model.DefaultSfpThresholds()
	for i := range s.Devices {
		d := &s.Devices[i]
		normMAC(&d.PrimaryMAC)
		for j := range d.MACs {
			normMAC(&d.MACs[j])
		}
		d.MACs = slices.DeleteFunc(d.MACs, func(m string) bool { return m == "" })
		d.NormalizeSlices()
	}
	for i := range s.Links {
		l := &s.Links[i]
		normMAC(&l.MAC)
		if l.SFP != nil {
			l.SFP.AlertState = model.EvaluateSfpAlert(l.SFP, th)
		}
		l.NormalizeSlices()
	}
	for i := range s.LagGroups {
		s.LagGroups[i].NormalizeSlices()
	}
	for i := range s.Segments {
		s.Segments[i].NormalizeSlices()
	}
	for i := range s.Zones {
		s.Zones[i].NormalizeSlices()
	}
	for i := range s.FDB {
		normMAC(&s.FDB[i].MAC)
	}
	s.FDB = slices.DeleteFunc(s.FDB, func(f model.FdbEntry) bool { return f.MAC == "" })
	for i := range s.LLDP {
		// Chassis IDs may legitimately be non-MAC strings; keep them as reported.
		if mac, err := model.NormalizeMAC(s.LLDP[i].ChassisID); err == nil {
			s.LLDP[i].ChassisID = mac
		}
	}
	for i := range s.Neighbors {
		normMAC(&s.Neighbors[i].MAC)
	}
	for i := range s.DHCP {
		normMAC(&s.DHCP[i].MAC)
	}
	for i := range s.Firewalls {
		s.Firewalls[i].NormalizeSlices()
	}
	for i := range s.Radios {
		s.Radios[i].NormalizeSlices()
	}
	for i := range s.WirelessUplinks {
		normMAC(&s.WirelessUplinks[i].PeerMAC)
	}
	for i := range s.Clients {
		normMAC(&s.Clients[i].MAC)
		s.Clients[i].NormalizeSlices()
	}
	s.Clients = slices.DeleteFunc(s.Clients, func(c model.Client) bool { return c.MAC == "" })
}

// normMAC canonicalizes *p in place and clears it when it is not a valid MAC.
func normMAC(p *string) {
	if *p == "" {
		return
	}
	mac, err := model.NormalizeMAC(*p)
	if err != nil {
		mac = ""
	}
	*p = mac
}

// MinIntervalDriver is implemented by drivers whose devices must not be polled more often
// than a floor; the poller raises a target's configured interval to it.
type MinIntervalDriver interface {
	MinPollInterval() time.Duration
}

// Driver is implemented by every platform integration (OpenWrt, RTLPlayground, Andaru, pfSense, OPNsense, UniFi, SNMP).
type Driver interface {
	Kind() string
	ValidateConfig(cfg TargetConfig) error
	Poll(ctx context.Context, cfg TargetConfig) (*Snapshot, error)
}

// DiagnosticDriver is an optional interface for drivers supporting on-demand device diagnostics.
type DiagnosticDriver interface {
	RunDiagnostic(ctx context.Context, cfg TargetConfig, req model.DiagnosticRequest) (*model.DiagnosticResult, error)
}

// ConfigDriver is the Phase 2 contract for safe save -> preview -> apply -> confirm/rollback flows.
type ConfigDriver interface {
	PreviewChange(ctx context.Context, cfg TargetConfig, change model.ChangeRequest) (*model.ChangePreview, error)
	ApplyChange(ctx context.Context, cfg TargetConfig, change model.ChangeRequest) (*model.ApplySession, error)
	ConfirmApply(ctx context.Context, cfg TargetConfig, sessionID string) error
	RollbackApply(ctx context.Context, cfg TargetConfig, sessionID string) error
}
