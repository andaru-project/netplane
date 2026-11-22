// SPDX-License-Identifier: AGPL-3.0-or-later
package events

import (
	"testing"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestBusPublishAndSlowSubscriberDrop(t *testing.T) {
	bus := NewBus()
	fastCh, unsubFast := bus.Subscribe(4)
	defer unsubFast()
	slowCh, unsubSlow := bus.Subscribe(1)
	defer unsubSlow()

	// Publish 3 events without reading slowCh -> slowCh buffers 1, drops 2, never blocks.
	for i := 0; i < 3; i++ {
		bus.Publish(Event{Type: TopologyUpdated, Severity: SeverityInfo, Summary: "updated"})
	}

	if bus.DroppedCount() != 2 {
		t.Fatalf("DroppedCount = %d, want 2", bus.DroppedCount())
	}
	if len(fastCh) != 3 {
		t.Fatalf("fastCh len = %d, want 3", len(fastCh))
	}
	ev := <-fastCh
	if ev.ID == "" || ev.Timestamp.IsZero() {
		t.Fatalf("expected auto-populated ID and Timestamp, got %+v", ev)
	}
	<-slowCh
	unsubSlow()
	// Calling unsub twice must be safe.
	unsubSlow()
}

func TestDiffSnapshots(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 40, 0, 0, time.UTC)

	// First poll emits active SFP warning only.
	first := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: now,
		Devices:     []model.Device{{ID: "sw1", Name: "LG510S", Online: true}},
		Links: []model.Link{{
			DeviceID: "sw1", IfName: "sfp9", LinkUp: true, StpState: model.StpForwarding,
			SFP: &model.SfpDom{Present: true, AlertState: model.AlertWarn},
		}},
		VPNs:     []model.VpnTunnel{{DeviceID: "fw1", ID: "wg0", Name: "wg-home", Up: true}},
		Gateways: []model.GatewayStatus{{DeviceID: "fw1", Name: "WAN_DHCP", Online: true}},
	}
	evs0 := DiffSnapshots(nil, first)
	if len(evs0) != 1 || evs0[0].Type != SfpAlert || evs0[0].Severity != SeverityWarn {
		t.Fatalf("unexpected initial diff events: %+v", evs0)
	}

	// Second poll triggers offline, link down, STP blocking, SFP crit, VPN down, gateway down.
	second := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: now.Add(15 * time.Second),
		Devices: []model.Device{
			{ID: "sw1", Name: "LG510S", Online: false},
			{ID: "ap1", Name: "AP-New", Online: true},
		},
		Links: []model.Link{{
			DeviceID: "sw1", IfName: "sfp9", LinkUp: false, StpState: model.StpBlocking,
			SFP: &model.SfpDom{Present: true, AlertState: model.AlertCrit},
		}},
		VPNs:     []model.VpnTunnel{{DeviceID: "fw1", ID: "wg0", Name: "wg-home", Up: false}},
		Gateways: []model.GatewayStatus{{DeviceID: "fw1", Name: "WAN_DHCP", Online: false}},
	}
	evs := DiffSnapshots(first, second)
	seen := make(map[Type]bool)
	for _, e := range evs {
		seen[e.Type] = true
	}
	for _, want := range []Type{DeviceOffline, DeviceOnline, LinkDown, LinkStpChanged, SfpAlert, VpnDown, GatewayDown} {
		if !seen[want] {
			t.Errorf("missing expected event %q in %+v", want, evs)
		}
	}

	// Recovery poll triggers online, link up, VPN up, gateway up.
	third := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: now.Add(30 * time.Second),
		Devices:     []model.Device{{ID: "sw1", Name: "LG510S", Online: true}},
		Links:       []model.Link{{DeviceID: "sw1", IfName: "sfp9", LinkUp: true, StpState: model.StpForwarding}},
		VPNs:        []model.VpnTunnel{{DeviceID: "fw1", ID: "wg0", Name: "wg-home", Up: true}},
		Gateways:    []model.GatewayStatus{{DeviceID: "fw1", Name: "WAN_DHCP", Online: true}},
	}
	evs3 := DiffSnapshots(second, third)
	seen3 := make(map[Type]bool)
	for _, e := range evs3 {
		seen3[e.Type] = true
	}
	for _, want := range []Type{DeviceOnline, LinkUp, LinkStpChanged, VpnUp, GatewayUp} {
		if !seen3[want] {
			t.Errorf("missing recovery event %q in %+v", want, evs3)
		}
	}
}
