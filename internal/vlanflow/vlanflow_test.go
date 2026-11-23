// SPDX-License-Identifier: AGPL-3.0-or-later
package vlanflow

import (
	"fmt"
	"slices"
	"testing"

	"github.com/andaru-project/netplane/internal/model"
	"github.com/andaru-project/netplane/internal/store"
	"github.com/andaru-project/netplane/internal/topology"
)

func ip(v int) *int { return &v }

// fw ==bond0/lag1== core; core:lan1 -> office:lan1 (VLAN 40 missing on core);
// core:lan2 -> poe:port1 (PVID 10 vs 20); office ~~wireless~~ ap.
func fixture() (*store.NetworkState, topology.Graph) {
	state := &store.NetworkState{}
	state.Devices = []model.Device{
		{ID: "fw", Name: "fw-core"}, {ID: "core", Name: "sw-core"}, {ID: "office", Name: "sw-office"},
		{ID: "poe", Name: "sw-poe"}, {ID: "ap", Name: "ap-mesh"},
	}
	state.LagGroups = []model.LagGroup{
		{DeviceID: "fw", ID: "bond0", Name: "bond0", MemberLinks: []string{"sfp0", "sfp1"}},
		{DeviceID: "core", ID: "lag1", Name: "lag1", MemberLinks: []string{"sfp9", "sfp10"}},
	}
	state.Segments = []model.Segment{
		{DeviceID: "fw", ID: "lan", Name: "LAN", ParentLink: "bond0", VLANID: ip(10), ZoneID: "z-lan", Addresses: []model.IPAddress{{Address: "10.10.10.1", PrefixLength: 24}}},
		{DeviceID: "fw", ID: "iot", Name: "IoT", ParentLink: "bond0", VLANID: ip(20), Addresses: []model.IPAddress{{Address: "10.10.20.1", PrefixLength: 24}}},
		{DeviceID: "fw", ID: "lab", Name: "Lab", ParentLink: "bond0", VLANID: ip(40), Addresses: []model.IPAddress{{Address: "10.10.40.1", PrefixLength: 24}}},
	}
	state.Zones = []model.Zone{{DeviceID: "fw", ID: "z-lan", Name: "lan"}}
	state.Links = []model.Link{
		{DeviceID: "fw", IfName: "sfp0"},
		{DeviceID: "core", IfName: "sfp9", PVID: ip(1), TaggedVLANs: []int{10, 20, 40}},
		{DeviceID: "core", IfName: "lan1", PVID: ip(1), TaggedVLANs: []int{10, 20}},
		{DeviceID: "core", IfName: "lan2", PVID: ip(10), TaggedVLANs: []int{20}},
		{DeviceID: "core", IfName: "lan5", PVID: ip(10), OperState: model.OperUp},
		{DeviceID: "office", IfName: "lan1", PVID: ip(1), TaggedVLANs: []int{10, 20, 40}},
		{DeviceID: "office", IfName: "lan4", PVID: ip(40), Label: "bench", OperState: model.OperUp},
		{DeviceID: "poe", IfName: "port1", PVID: ip(20), TaggedVLANs: []int{10}},
	}
	state.Radios = []model.WirelessRadio{{DeviceID: "ap", Name: "wifi1", SSIDs: []model.Ssid{{Name: "Home", VLANID: ip(10), ClientCount: 3}}}}
	state.Clients = []model.Client{{MAC: "aa", Online: true, VLAN: ip(40)}}

	g := topology.Graph{}
	for _, d := range state.Devices {
		g.Nodes = append(g.Nodes, topology.Node{ID: d.ID, Kind: topology.NodeDevice})
	}
	g.Nodes = append(g.Nodes, topology.Node{ID: "client", Kind: topology.NodeClient})
	g.Edges = []topology.Edge{
		{ID: "e1", FromID: "fw", FromLink: "bond0", ToID: "core", ToLink: "lag1", Kind: topology.EdgeLAG},
		{ID: "e2", FromID: "core", FromLink: "lan1", ToID: "office", ToLink: "lan1", Kind: topology.EdgeWired},
		{ID: "e3", FromID: "core", FromLink: "lan2", ToID: "poe", ToLink: "port1", Kind: topology.EdgeWired},
		{ID: "e4", FromID: "office", ToID: "ap", Kind: topology.EdgeWirelessUplink},
		{ID: "e5", FromID: "office", FromLink: "lan4", ToID: "client", Kind: topology.EdgeWired},
	}
	return state, g
}

func vlan(t *testing.T, f Flow, id int) VLAN {
	t.Helper()
	for _, v := range f.VLANs {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("VLAN %d missing", id)
	return VLAN{}
}

func hop(t *testing.T, v VLAN, edge string) Hop {
	t.Helper()
	for _, h := range v.Hops {
		if h.EdgeID == edge {
			return h
		}
	}
	t.Fatalf("VLAN %d has no hop on %s", v.ID, edge)
	return Hop{}
}

func TestAnalyze(t *testing.T) {
	f := Analyze(fixture())

	var ids []int
	for _, v := range f.VLANs {
		ids = append(ids, v.ID)
	}
	if !slices.Equal(ids, []int{1, 10, 20, 40}) {
		t.Fatalf("VLANs = %v", ids)
	}

	v10 := vlan(t, f, 10)
	if v10.Name != "LAN" || len(v10.Gateways) != 1 || v10.Gateways[0].ZoneName != "lan" {
		t.Errorf("VLAN 10 name/gateways = %q %+v", v10.Name, v10.Gateways)
	}
	// The LAG's VLANs come from the firewall's segments and the switch's first member port.
	if h := hop(t, v10, "e1"); h.FromMode != ModeTagged || h.ToMode != ModeTagged || h.State != HopCarried {
		t.Errorf("VLAN 10 on e1 = %+v", h)
	}
	if h := hop(t, v10, "e3"); h.FromMode != ModeUntagged || h.ToMode != ModeTagged || h.State != HopLeak {
		t.Errorf("VLAN 10 on e3 = %+v", h)
	}
	if h := hop(t, v10, "e4"); h.State != HopUnverified {
		t.Errorf("VLAN 10 on the wireless uplink = %+v", h)
	}
	if !slices.Equal(v10.Reached, []string{"ap", "core", "fw", "office"}) {
		t.Errorf("VLAN 10 reached = %v", v10.Reached)
	}
	if len(v10.SSIDs) != 1 || !v10.SSIDs[0].Reachable {
		t.Errorf("VLAN 10 SSIDs = %+v", v10.SSIDs)
	}
	if len(v10.AccessPorts) != 1 || v10.AccessPorts[0].Link != "lan5" {
		t.Errorf("VLAN 10 access ports = %+v (infra ports must be excluded)", v10.AccessPorts)
	}

	v40 := vlan(t, f, 40)
	if h := hop(t, v40, "e2"); h.FromMode != ModeNone || h.ToMode != ModeTagged || h.State != HopDeadEnd {
		t.Errorf("VLAN 40 on e2 = %+v", h)
	}
	if !slices.Equal(v40.Reached, []string{"core", "fw"}) || v40.Clients != 1 {
		t.Errorf("VLAN 40 reached = %v clients = %d", v40.Reached, v40.Clients)
	}
	if len(v40.AccessPorts) != 1 || v40.AccessPorts[0].Reachable {
		t.Errorf("VLAN 40 access ports = %+v", v40.AccessPorts)
	}

	// VLAN 1 is an unrouted native VLAN: the firewall drops it, which the hop shows but no finding reports.
	if v1 := vlan(t, f, 1); len(v1.Gateways) != 0 || len(v1.Reached) != 0 || hop(t, v1, "e1").State != HopDeadEnd {
		t.Errorf("VLAN 1 = %+v", v1)
	}

	type key struct {
		kind  FindingKind
		vlans string
		edge  string
	}
	var got []key
	for _, x := range f.Findings {
		got = append(got, key{x.Kind, fmt.Sprint(x.VLANs), x.EdgeID})
	}
	want := []key{
		{FindingLeak, "[10 20]", "e3"},
		{FindingDeadEnd, "[40]", "e2"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	de := f.Findings[1]
	if de.DeviceID != "office" || de.PeerID != "core" || !slices.Equal(de.Stranded, []string{"office"}) ||
		de.Message != "VLAN 40 is tagged on sw-office:lan1 but missing on sw-core:lan1" {
		t.Errorf("dead-end finding = %+v", de)
	}
}

func TestHopState(t *testing.T) {
	for _, c := range []struct {
		from, to Mode
		want     HopState
	}{
		{ModeNone, ModeNone, ""},
		{ModeTagged, ModeTagged, HopCarried},
		{ModeUntagged, ModeUntagged, HopCarried},
		{ModeTagged, ModeNone, HopDeadEnd},
		{ModeNone, ModeUntagged, HopDeadEnd},
		{ModeTagged, ModeUntagged, HopMismatch},
		{ModeUnknown, ModeNone, HopUnverified},
		{ModeTagged, ModeUnknown, HopUnverified},
	} {
		if got := hopState(c.from, c.to); got != c.want {
			t.Errorf("hopState(%s, %s) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestAnalyzeNil(t *testing.T) {
	f := Analyze(nil, topology.Graph{})
	if f.VLANs == nil || f.Findings == nil {
		t.Fatal("nil state must yield empty, non-nil slices")
	}
}
