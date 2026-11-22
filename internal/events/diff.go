// SPDX-License-Identifier: AGPL-3.0-or-later
package events

import (
	"fmt"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

// DiffSnapshots compares prev and curr snapshots for the same target and returns
// all significant state transition events. When prev is nil (first poll), no spurious
// transition flood is emitted except active critical SFP alerts.
func DiffSnapshots(prev, curr *driver.Snapshot) []Event {
	if curr == nil {
		return nil
	}
	ts := curr.CollectedAt.UTC()
	var out []Event

	if prev == nil {
		for _, l := range curr.Links {
			if l.SFP != nil && l.SFP.Present && (l.SFP.AlertState == model.AlertWarn || l.SFP.AlertState == model.AlertCrit) {
				sev := SeverityWarn
				if l.SFP.AlertState == model.AlertCrit {
					sev = SeverityCrit
				}
				out = append(out, Event{
					Type:      SfpAlert,
					Severity:  sev,
					Timestamp: ts,
					TargetID:  curr.TargetID,
					DeviceID:  l.DeviceID,
					LinkName:  l.IfName,
					Summary:   fmt.Sprintf("SFP optical alert (%s) on %s/%s", l.SFP.AlertState, l.DeviceID, l.IfName),
				})
			}
		}
		return out
	}

	// 1. Device online/offline transitions.
	prevDevs := make(map[string]model.Device, len(prev.Devices))
	for _, d := range prev.Devices {
		prevDevs[d.ID] = d
	}
	for _, d := range curr.Devices {
		pd, ok := prevDevs[d.ID]
		if !ok {
			if d.Online {
				out = append(out, Event{
					Type: DeviceOnline, Severity: SeverityInfo, Timestamp: ts,
					TargetID: curr.TargetID, DeviceID: d.ID,
					Summary: fmt.Sprintf("Device %s came online", d.Name),
				})
			}
			continue
		}
		if pd.Online != d.Online {
			if d.Online {
				out = append(out, Event{
					Type: DeviceOnline, Severity: SeverityInfo, Timestamp: ts,
					TargetID: curr.TargetID, DeviceID: d.ID,
					Summary: fmt.Sprintf("Device %s came online", d.Name),
				})
			} else {
				out = append(out, Event{
					Type: DeviceOffline, Severity: SeverityCrit, Timestamp: ts,
					TargetID: curr.TargetID, DeviceID: d.ID,
					Summary: fmt.Sprintf("Device %s went offline", d.Name),
				})
			}
		}
	}

	// 2. Link up/down, STP state, and SFP optical alert transitions.
	type linkKey struct{ devID, ifName string }
	prevLinks := make(map[linkKey]model.Link, len(prev.Links))
	for _, l := range prev.Links {
		prevLinks[linkKey{l.DeviceID, l.IfName}] = l
	}
	for _, l := range curr.Links {
		pl, ok := prevLinks[linkKey{l.DeviceID, l.IfName}]
		if !ok {
			continue
		}
		if pl.LinkUp != l.LinkUp {
			if l.LinkUp {
				out = append(out, Event{
					Type: LinkUp, Severity: SeverityInfo, Timestamp: ts,
					TargetID: curr.TargetID, DeviceID: l.DeviceID, LinkName: l.IfName,
					Summary: fmt.Sprintf("Link %s/%s is up (%d Mbps)", l.DeviceID, l.IfName, l.SpeedMbps),
				})
			} else {
				out = append(out, Event{
					Type: LinkDown, Severity: SeverityWarn, Timestamp: ts,
					TargetID: curr.TargetID, DeviceID: l.DeviceID, LinkName: l.IfName,
					Summary: fmt.Sprintf("Link %s/%s went down", l.DeviceID, l.IfName),
				})
			}
		}
		if pl.StpState != l.StpState && l.StpState != "" {
			sev := SeverityInfo
			if l.StpState == model.StpBlocking || l.StpState == model.StpDiscarding {
				sev = SeverityWarn
			}
			out = append(out, Event{
				Type: LinkStpChanged, Severity: sev, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: l.DeviceID, LinkName: l.IfName,
				Summary: fmt.Sprintf("STP state changed on %s/%s: %s -> %s", l.DeviceID, l.IfName, pl.StpState, l.StpState),
				Details: map[string]any{"old": pl.StpState, "new": l.StpState},
			})
		}
		prevAlert := model.AlertOK
		if pl.SFP != nil {
			prevAlert = pl.SFP.AlertState
		}
		currAlert := model.AlertOK
		if l.SFP != nil {
			currAlert = l.SFP.AlertState
		}
		if prevAlert != currAlert && (currAlert == model.AlertWarn || currAlert == model.AlertCrit) {
			sev := SeverityWarn
			if currAlert == model.AlertCrit {
				sev = SeverityCrit
			}
			out = append(out, Event{
				Type: SfpAlert, Severity: sev, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: l.DeviceID, LinkName: l.IfName,
				Summary: fmt.Sprintf("SFP optical alert (%s) on %s/%s", currAlert, l.DeviceID, l.IfName),
			})
		}
	}

	// 3. VPN tunnel transitions.
	type idKey struct{ devID, id string }
	prevVPNs := make(map[idKey]model.VpnTunnel, len(prev.VPNs))
	for _, v := range prev.VPNs {
		prevVPNs[idKey{v.DeviceID, v.ID}] = v
	}
	for _, v := range curr.VPNs {
		pv, ok := prevVPNs[idKey{v.DeviceID, v.ID}]
		if !ok || pv.Up == v.Up {
			continue
		}
		if v.Up {
			out = append(out, Event{
				Type: VpnUp, Severity: SeverityInfo, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: v.DeviceID,
				Summary: fmt.Sprintf("VPN tunnel %s came up", v.Name),
			})
		} else {
			out = append(out, Event{
				Type: VpnDown, Severity: SeverityWarn, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: v.DeviceID,
				Summary: fmt.Sprintf("VPN tunnel %s went down", v.Name),
			})
		}
	}

	// 4. Gateway transitions.
	prevGWs := make(map[idKey]model.GatewayStatus, len(prev.Gateways))
	for _, g := range prev.Gateways {
		prevGWs[idKey{g.DeviceID, g.Name}] = g
	}
	for _, g := range curr.Gateways {
		pg, ok := prevGWs[idKey{g.DeviceID, g.Name}]
		if !ok || pg.Online == g.Online {
			continue
		}
		if g.Online {
			out = append(out, Event{
				Type: GatewayUp, Severity: SeverityInfo, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: g.DeviceID,
				Summary: fmt.Sprintf("Gateway %s is back online", g.Name),
			})
		} else {
			out = append(out, Event{
				Type: GatewayDown, Severity: SeverityCrit, Timestamp: ts,
				TargetID: curr.TargetID, DeviceID: g.DeviceID,
				Summary: fmt.Sprintf("Gateway %s went offline", g.Name),
			})
		}
	}

	return out
}
