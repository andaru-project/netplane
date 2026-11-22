// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"context"
	"fmt"
	"log/slog"
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
func (s *Snapshot) Normalize() error {
	th := model.DefaultSfpThresholds()
	for i := range s.Devices {
		d := &s.Devices[i]
		if d.PrimaryMAC != "" {
			mac, err := model.NormalizeMAC(d.PrimaryMAC)
			if err != nil {
				return fmt.Errorf("device %s primary_mac: %w", d.ID, err)
			}
			d.PrimaryMAC = mac
		}
		for j, raw := range d.MACs {
			mac, err := model.NormalizeMAC(raw)
			if err != nil {
				return fmt.Errorf("device %s mac[%d]: %w", d.ID, j, err)
			}
			d.MACs[j] = mac
		}
		d.NormalizeSlices()
	}
	for i := range s.Links {
		l := &s.Links[i]
		if l.MAC != "" {
			mac, err := model.NormalizeMAC(l.MAC)
			if err != nil {
				return fmt.Errorf("link %s/%s mac: %w", l.DeviceID, l.IfName, err)
			}
			l.MAC = mac
		}
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
		mac, err := model.NormalizeMAC(s.FDB[i].MAC)
		if err != nil {
			return fmt.Errorf("fdb %s/%s: %w", s.FDB[i].DeviceID, s.FDB[i].LinkName, err)
		}
		s.FDB[i].MAC = mac
	}
	for i := range s.LLDP {
		if s.LLDP[i].ChassisID != "" {
			if mac, err := model.NormalizeMAC(s.LLDP[i].ChassisID); err == nil {
				s.LLDP[i].ChassisID = mac
			}
		}
	}
	for i := range s.Neighbors {
		if s.Neighbors[i].MAC != "" {
			mac, err := model.NormalizeMAC(s.Neighbors[i].MAC)
			if err != nil {
				return fmt.Errorf("neighbor %s/%s: %w", s.Neighbors[i].DeviceID, s.Neighbors[i].IP, err)
			}
			s.Neighbors[i].MAC = mac
		}
	}
	for i := range s.DHCP {
		if s.DHCP[i].MAC != "" {
			mac, err := model.NormalizeMAC(s.DHCP[i].MAC)
			if err != nil {
				return fmt.Errorf("dhcp %s/%s: %w", s.DHCP[i].DeviceID, s.DHCP[i].IP, err)
			}
			s.DHCP[i].MAC = mac
		}
	}
	for i := range s.Firewalls {
		s.Firewalls[i].NormalizeSlices()
	}
	for i := range s.Radios {
		s.Radios[i].NormalizeSlices()
	}
	for i := range s.WirelessUplinks {
		if s.WirelessUplinks[i].PeerMAC != "" {
			mac, err := model.NormalizeMAC(s.WirelessUplinks[i].PeerMAC)
			if err != nil {
				return fmt.Errorf("wireless_uplink %s: %w", s.WirelessUplinks[i].DeviceID, err)
			}
			s.WirelessUplinks[i].PeerMAC = mac
		}
	}
	for i := range s.Clients {
		mac, err := model.NormalizeMAC(s.Clients[i].MAC)
		if err != nil {
			return fmt.Errorf("client[%d]: %w", i, err)
		}
		s.Clients[i].MAC = mac
		s.Clients[i].NormalizeSlices()
	}
	return nil
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
