// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vlanflow traces every VLAN across the solved topology, port by port, and flags where a
// VLAN dead-ends (carried on one end of a link but not the other) or leaks into another VLAN
// (a PVID mismatch across a link).
package vlanflow

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/andaru-project/netplane/internal/model"
	"github.com/andaru-project/netplane/internal/store"
	"github.com/andaru-project/netplane/internal/topology"
)

// Mode is how one port carries a VLAN.
type Mode string

const (
	ModeTagged   Mode = "tagged"
	ModeUntagged Mode = "untagged"
	ModeNone     Mode = "none"
	// ModeUnknown means the driver reports no VLAN membership for the port.
	ModeUnknown Mode = "unknown"
)

// HopState is what a link does to a VLAN.
type HopState string

const (
	HopCarried HopState = "carried"
	// HopDeadEnd: one end carries the VLAN, and the other end does not although its device uses it.
	HopDeadEnd HopState = "dead_end"
	// HopUnused: one end carries the VLAN toward a device that uses it nowhere (an unpruned trunk).
	HopUnused HopState = "unused"
	// HopMismatch: one end tags the VLAN and the other carries it untagged.
	HopMismatch HopState = "mismatch"
	// HopLeak: one end sends the VLAN untagged and the other end's PVID is a different VLAN.
	HopLeak HopState = "leak"
	// HopUnverified: at least one end's membership is unknown (including wireless uplinks).
	HopUnverified HopState = "unverified"
)

// Hop is one infrastructure link as seen by one VLAN.
type Hop struct {
	EdgeID   string            `json:"edge_id"`
	FromID   string            `json:"from_id"`
	FromLink string            `json:"from_link,omitempty"`
	ToID     string            `json:"to_id"`
	ToLink   string            `json:"to_link,omitempty"`
	Kind     topology.EdgeKind `json:"kind"`
	FromMode Mode              `json:"from_mode"`
	ToMode   Mode              `json:"to_mode"`
	State    HopState          `json:"state"`
	// The two ends' VLAN configuration as the analysis read it (nil and empty when not reported).
	FromPVID   *int  `json:"from_pvid,omitempty"`
	FromTagged []int `json:"from_tagged"`
	ToPVID     *int  `json:"to_pvid,omitempty"`
	ToTagged   []int `json:"to_tagged"`
}

// Gateway is an L3 segment that routes the VLAN.
type Gateway struct {
	DeviceID     string            `json:"device_id"`
	SegmentID    string            `json:"segment_id"`
	SegmentName  string            `json:"segment_name"`
	ParentLink   string            `json:"parent_link,omitempty"`
	ZoneID       string            `json:"zone_id,omitempty"`
	ZoneName     string            `json:"zone_name,omitempty"`
	Addresses    []model.IPAddress `json:"addresses"`
	IsManagement bool              `json:"is_management,omitempty"`
}

// AccessPort is a non-infrastructure port whose PVID is the VLAN.
type AccessPort struct {
	DeviceID  string          `json:"device_id"`
	Link      string          `json:"link"`
	Label     string          `json:"label,omitempty"`
	OperState model.OperState `json:"oper_state"`
	Reachable bool            `json:"reachable"`
}

// SSID is a wireless network mapped to the VLAN.
type SSID struct {
	DeviceID    string `json:"device_id"`
	Radio       string `json:"radio"`
	Name        string `json:"name"`
	ClientCount int    `json:"client_count"`
	Reachable   bool   `json:"reachable"`
}

// VLAN is one VLAN's path through the network.
type VLAN struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
	// Gateways are the segments routing the VLAN; empty means L2 only.
	Gateways []Gateway `json:"gateways"`
	// Devices configure the VLAN on a port, segment or SSID.
	Devices []string `json:"devices"`
	// Reached are the devices with a path to a gateway over links that carry the VLAN.
	Reached     []string     `json:"reached"`
	Hops        []Hop        `json:"hops"`
	AccessPorts []AccessPort `json:"access_ports"`
	SSIDs       []SSID       `json:"ssids"`
	Clients     int          `json:"clients"`
}

// FindingKind classifies a VLAN problem.
type FindingKind string

const (
	// FindingLeak: the two ends of a link have different PVIDs, so untagged frames change VLAN.
	FindingLeak FindingKind = "leak"
	// FindingDeadEnd: a VLAN is carried on one end of a link and missing on the other.
	FindingDeadEnd FindingKind = "dead_end"
	// FindingTagMismatch: a VLAN is tagged on one end of a link and untagged on the other.
	FindingTagMismatch FindingKind = "tag_mismatch"
)

// Severity ranks a finding.
type Severity string

const (
	SeverityCrit Severity = "crit"
	SeverityWarn Severity = "warn"
)

// Finding is one VLAN problem on one link.
type Finding struct {
	Kind     FindingKind `json:"kind"`
	Severity Severity    `json:"severity"`
	// VLANs holds the affected VLAN; a leak holds both, the sender's first.
	VLANs    []int  `json:"vlans"`
	EdgeID   string `json:"edge_id"`
	DeviceID string `json:"device_id"`
	Link     string `json:"link"`
	PeerID   string `json:"peer_id"`
	PeerLink string `json:"peer_link"`
	Message  string `json:"message"`
	// Stranded lists devices that configure the VLAN but cannot reach its gateway through this link.
	Stranded []string `json:"stranded"`
}

// Flow is the VLAN flow snapshot.
type Flow struct {
	GeneratedAt time.Time `json:"generated_at"`
	VLANs       []VLAN    `json:"vlans"`
	Findings    []Finding `json:"findings"`
}

type portKey struct{ dev, link string }

// membership is a port's VLAN configuration; known is false when the driver reports none.
type membership struct {
	known bool
	// nativeUnknown: the port carries untagged traffic whose VLAN ID is not reported (a routed parent).
	nativeUnknown bool
	pvid          *int
	tagged        map[int]bool
}

func (m membership) mode(v int) Mode {
	switch {
	case m.tagged[v]:
		return ModeTagged
	case m.pvid != nil && *m.pvid == v:
		return ModeUntagged
	case !m.known || m.nativeUnknown:
		return ModeUnknown
	}
	return ModeNone
}

type analyzer struct {
	state    *store.NetworkState
	devices  map[string]bool
	links    map[portKey]model.Link
	lags     map[portKey]model.LagGroup
	infra    map[portKey]bool
	edges    []topology.Edge
	zoneName map[portKey]string
	names    map[string]string
	// routers holds the devices that route: gateway capability or a router/gateway/firewall role.
	routers map[string]bool
	// adj is the infrastructure graph: each device's peers with the index of the edge into edges.
	adj map[string][]peerEdge
	// native maps a routed port with an untagged segment to the VLAN its peer's PVID puts it in.
	native map[portKey]int
	// segVLAN maps an index into state.Segments to the segment's VLAN, native ones included.
	segVLAN map[int]int
}

type peerEdge struct {
	peer string
	edge int
}

// Analyze traces every VLAN in state across the infrastructure edges of g.
func Analyze(state *store.NetworkState, g topology.Graph) Flow {
	f := Flow{GeneratedAt: g.GeneratedAt, VLANs: []VLAN{}, Findings: []Finding{}}
	if state == nil {
		return f
	}
	a := &analyzer{
		state:    state,
		devices:  map[string]bool{},
		links:    map[portKey]model.Link{},
		lags:     map[portKey]model.LagGroup{},
		infra:    map[portKey]bool{},
		zoneName: map[portKey]string{},
		names:    map[string]string{},
		routers:  map[string]bool{},
		adj:      map[string][]peerEdge{},
		native:   map[portKey]int{},
		segVLAN:  map[int]int{},
	}
	for _, d := range state.Devices {
		a.names[d.ID] = d.Name
		a.routers[d.ID] = slices.Contains(d.Capabilities, model.CapGateway) ||
			slices.Contains([]model.DeviceRole{model.RoleRouter, model.RoleGateway, model.RoleFirewall}, d.Role)
	}
	for _, n := range g.Nodes {
		if n.Kind == topology.NodeDevice {
			a.devices[n.ID] = true
		}
	}
	for _, l := range state.Links {
		a.links[portKey{l.DeviceID, l.IfName}] = l
	}
	for _, lag := range state.LagGroups {
		a.lags[portKey{lag.DeviceID, lag.Name}] = lag
	}
	for _, z := range state.Zones {
		a.zoneName[portKey{z.DeviceID, z.ID}] = z.Name
	}
	for _, e := range g.Edges {
		if !a.devices[e.FromID] || !a.devices[e.ToID] {
			continue
		}
		a.adj[e.FromID] = append(a.adj[e.FromID], peerEdge{e.ToID, len(a.edges)})
		a.adj[e.ToID] = append(a.adj[e.ToID], peerEdge{e.FromID, len(a.edges)})
		a.edges = append(a.edges, e)
		for _, p := range []portKey{{e.FromID, e.FromLink}, {e.ToID, e.ToLink}} {
			a.infra[p] = true
			for _, m := range a.lags[p].MemberLinks {
				a.infra[portKey{p.dev, m}] = true
			}
		}
	}

	a.nativeVLANs()

	for _, v := range a.vlanIDs() {
		vl, findings := a.trace(v)
		f.VLANs = append(f.VLANs, vl)
		f.Findings = append(f.Findings, findings...)
	}
	f.Findings = append(f.Findings, a.leaks()...)
	slices.SortStableFunc(f.Findings, func(x, y Finding) int {
		return cmp.Or(cmp.Compare(rank(x.Severity), rank(y.Severity)), cmp.Compare(x.VLANs[0], y.VLANs[0]), cmp.Compare(x.EdgeID, y.EdgeID))
	})
	return f
}

func rank(s Severity) int {
	if s == SeverityCrit {
		return 0
	}
	return 1
}

// portNames returns a port's name plus, for a LAG, its members' names.
func (a *analyzer) portNames(dev, name string) []string {
	return append([]string{name}, a.lags[portKey{dev, name}].MemberLinks...)
}

// port returns the VLAN membership of a port. A LAG combines its members' VLANs (the first reported
// PVID wins). A port that reports no VLANs falls back to the segments routed on it; an untagged
// segment takes the VLAN its peer's PVID gives it, when known.
func (a *analyzer) port(dev, name string) membership {
	m := membership{tagged: map[int]bool{}}
	if name == "" {
		return m
	}
	names := a.portNames(dev, name)
	for _, n := range names {
		l, ok := a.links[portKey{dev, n}]
		if !ok || (l.PVID == nil && len(l.TaggedVLANs) == 0) {
			continue
		}
		m.known = true
		if m.pvid == nil {
			m.pvid = l.PVID
		}
		for _, t := range l.TaggedVLANs {
			m.tagged[t] = true
		}
	}
	if m.known {
		return m
	}
	for _, s := range a.state.Segments {
		if s.DeviceID != dev || !slices.Contains(names, s.ParentLink) {
			continue
		}
		m.known = true
		if s.VLANID != nil {
			m.tagged[*s.VLANID] = true
		} else {
			m.nativeUnknown = true
		}
	}
	if p, ok := a.native[portKey{dev, name}]; ok && m.nativeUnknown {
		m.pvid, m.nativeUnknown = &p, false
	}
	return m
}

// nativeVLANs resolves untagged routed segments: when a routed port carries an untagged segment and
// its peer port reports a PVID, that segment is the gateway of the peer's PVID VLAN.
func (a *analyzer) nativeVLANs() {
	for i, s := range a.state.Segments {
		if s.VLANID != nil {
			a.segVLAN[i] = *s.VLANID
		}
	}
	for _, e := range a.edges {
		if e.Kind == topology.EdgeWirelessUplink {
			continue
		}
		for _, end := range [][2]portKey{{{e.FromID, e.FromLink}, {e.ToID, e.ToLink}}, {{e.ToID, e.ToLink}, {e.FromID, e.FromLink}}} {
			self, peer := a.port(end[0].dev, end[0].link), a.port(end[1].dev, end[1].link)
			if !self.nativeUnknown || peer.pvid == nil || !validVLAN(*peer.pvid) {
				continue
			}
			a.native[end[0]] = *peer.pvid
			names := a.portNames(end[0].dev, end[0].link)
			for i, s := range a.state.Segments {
				if s.DeviceID == end[0].dev && s.VLANID == nil && slices.Contains(names, s.ParentLink) {
					a.segVLAN[i] = *peer.pvid
				}
			}
		}
	}
}

// sideUses reports whether start, or any device it reaches without crossing edge skip or entering
// the edge's far end (a parallel link or a ring would otherwise lead back to it), uses the VLAN.
func (a *analyzer) sideUses(skip int, start, far string, using map[string]bool) bool {
	seen := map[string]bool{start: true, far: true}
	queue := []string{start}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if using[d] {
			return true
		}
		for _, pe := range a.adj[d] {
			if pe.edge != skip && !seen[pe.peer] {
				seen[pe.peer] = true
				queue = append(queue, pe.peer)
			}
		}
	}
	return false
}

// vlanIDs returns every VLAN ID configured on a port, segment or SSID, sorted.
func (a *analyzer) vlanIDs() []int {
	ids := map[int]bool{}
	for _, l := range a.state.Links {
		if l.PVID != nil {
			ids[*l.PVID] = true
		}
		for _, t := range l.TaggedVLANs {
			ids[t] = true
		}
	}
	for _, v := range a.segVLAN {
		ids[v] = true
	}
	for _, r := range a.state.Radios {
		for _, s := range r.SSIDs {
			if s.VLANID != nil {
				ids[*s.VLANID] = true
			}
		}
	}
	maps.DeleteFunc(ids, func(id int, _ bool) bool { return !validVLAN(id) })
	return slices.Sorted(maps.Keys(ids))
}

func (a *analyzer) trace(v int) (VLAN, []Finding) {
	vl := VLAN{ID: v, Gateways: []Gateway{}, Devices: []string{}, Reached: []string{}, Hops: []Hop{}, AccessPorts: []AccessPort{}, SSIDs: []SSID{}}
	using := map[string]bool{}
	gateways := map[string]bool{}

	for i, s := range a.state.Segments {
		if sv, ok := a.segVLAN[i]; !ok || sv != v {
			continue
		}
		using[s.DeviceID] = true
		if !a.routes(s) {
			continue
		}
		gw := Gateway{
			DeviceID: s.DeviceID, SegmentID: s.ID, SegmentName: s.Name, ParentLink: s.ParentLink,
			ZoneID: s.ZoneID, ZoneName: a.zoneName[portKey{s.DeviceID, s.ZoneID}], Addresses: s.Addresses, IsManagement: s.IsManagement,
		}
		if gw.Addresses == nil {
			gw.Addresses = []model.IPAddress{}
		}
		vl.Gateways = append(vl.Gateways, gw)
		gateways[s.DeviceID] = true
		if vl.Name == "" {
			vl.Name = s.Name
		}
	}
	for _, l := range a.state.Links {
		if l.PVID != nil && *l.PVID == v || slices.Contains(l.TaggedVLANs, v) {
			using[l.DeviceID] = true
		}
	}
	for _, r := range a.state.Radios {
		for _, s := range r.SSIDs {
			if s.VLANID != nil && *s.VLANID == v {
				using[r.DeviceID] = true
				if vl.Name == "" {
					vl.Name = s.Name
				}
			}
		}
	}

	adj := map[string][]string{}
	for i, e := range a.edges {
		h := Hop{EdgeID: e.ID, FromID: e.FromID, FromLink: e.FromLink, ToID: e.ToID, ToLink: e.ToLink, Kind: e.Kind, FromTagged: []int{}, ToTagged: []int{}}
		if e.Kind == topology.EdgeWirelessUplink {
			// Wireless uplinks report no VLAN membership: never a dead end, only unverified.
			h.FromMode, h.ToMode = ModeUnknown, ModeUnknown
		} else {
			from, to := a.port(e.FromID, e.FromLink), a.port(e.ToID, e.ToLink)
			h.FromMode, h.ToMode = from.mode(v), to.mode(v)
			h.FromPVID, h.FromTagged, h.ToPVID, h.ToTagged = from.pvid, from.taggedList(), to.pvid, to.taggedList()
			if h.FromMode == ModeUntagged && otherPVID(to, v) || h.ToMode == ModeUntagged && otherPVID(from, v) {
				h.State = HopLeak
			}
		}
		h.State = cmp.Or(h.State, hopState(h.FromMode, h.ToMode))
		switch h.State {
		case HopDeadEnd:
			// Missing on an end whose whole side of the network has no use for the VLAN is unpruned, not broken.
			if h.FromMode == ModeNone && !a.sideUses(i, e.FromID, e.ToID, using) || h.ToMode == ModeNone && !a.sideUses(i, e.ToID, e.FromID, using) {
				h.State = HopUnused
			}
		case HopUnverified:
			// An unverified link matters only when the VLAN is used on both of its sides.
			if !a.sideUses(i, e.FromID, e.ToID, using) || !a.sideUses(i, e.ToID, e.FromID, using) {
				h.State = ""
			}
		}
		if h.State == "" {
			continue
		}
		if h.State == HopCarried || h.State == HopUnverified {
			adj[e.FromID] = append(adj[e.FromID], e.ToID)
			adj[e.ToID] = append(adj[e.ToID], e.FromID)
		}
		vl.Hops = append(vl.Hops, h)
	}

	reached := reach(adj, slices.Collect(maps.Keys(gateways)))
	vl.Devices = slices.Sorted(maps.Keys(using))
	vl.Reached = slices.Sorted(maps.Keys(reached))

	seenPort := map[portKey]bool{}
	for _, l := range a.state.Links {
		k := portKey{l.DeviceID, l.IfName}
		if l.PVID == nil || *l.PVID != v || a.infra[k] || !a.devices[l.DeviceID] || seenPort[k] {
			continue
		}
		seenPort[k] = true
		vl.AccessPorts = append(vl.AccessPorts, AccessPort{DeviceID: l.DeviceID, Link: l.IfName, Label: l.Label, OperState: l.OperState, Reachable: reached[l.DeviceID]})
	}
	for _, r := range a.state.Radios {
		for _, s := range r.SSIDs {
			if s.VLANID != nil && *s.VLANID == v {
				vl.SSIDs = append(vl.SSIDs, SSID{DeviceID: r.DeviceID, Radio: r.Name, Name: s.Name, ClientCount: s.ClientCount, Reachable: reached[r.DeviceID]})
			}
		}
	}
	for _, c := range a.state.Clients {
		if c.Online && c.VLAN != nil && *c.VLAN == v {
			vl.Clients++
		}
	}

	var findings []Finding
	for _, h := range vl.Hops {
		switch h.State {
		case HopDeadEnd:
			dev, link, peer, peerLink, mode := h.FromID, h.FromLink, h.ToID, h.ToLink, h.FromMode
			if h.FromMode == ModeNone {
				dev, link, peer, peerLink, mode = h.ToID, h.ToLink, h.FromID, h.FromLink, h.ToMode
			}
			// When one side reaches a gateway, the break strands the other side; when neither
			// does, the break cannot be told from an upstream one, so both sides are listed.
			stranded := []string{}
			if len(gateways) > 0 {
				sides := []string{h.FromID, h.ToID}
				if reached[h.FromID] {
					sides = []string{h.ToID}
				} else if reached[h.ToID] {
					sides = []string{h.FromID}
				}
				for d := range reach(adj, sides) {
					if using[d] && !reached[d] {
						stranded = append(stranded, d)
					}
				}
			}
			slices.Sort(stranded)
			f := Finding{
				Kind: FindingDeadEnd, Severity: SeverityWarn, VLANs: []int{v}, EdgeID: h.EdgeID,
				DeviceID: dev, Link: link, PeerID: peer, PeerLink: peerLink, Stranded: stranded,
				Message: fmt.Sprintf("VLAN %d is %s on %s:%s but missing on %s:%s", v, mode, a.name(dev), link, a.name(peer), peerLink),
			}
			if mode == ModeUnknown {
				f.Message = fmt.Sprintf("VLAN %d is missing on %s:%s, and %s:%s reports no VLAN membership", v, a.name(peer), peerLink, a.name(dev), link)
			}
			findings = append(findings, f)
		case HopMismatch:
			findings = append(findings, Finding{
				Kind: FindingTagMismatch, Severity: SeverityWarn, VLANs: []int{v}, EdgeID: h.EdgeID,
				DeviceID: h.FromID, Link: h.FromLink, PeerID: h.ToID, PeerLink: h.ToLink, Stranded: []string{},
				Message: fmt.Sprintf("VLAN %d is %s on %s:%s but %s on %s:%s", v, h.FromMode, a.name(h.FromID), h.FromLink, h.ToMode, a.name(h.ToID), h.ToLink),
			})
		}
	}
	return vl, findings
}

// validVLAN reports whether id is a usable 802.1Q VLAN ID (0 and 4095 are reserved).
func validVLAN(id int) bool { return id >= 1 && id <= 4094 }

// routes reports whether a segment routes its VLAN: it is enabled, sits on a routing device, and is
// not only a DHCP, SLAAC or PPPoE client (a downstream router's WAN, a switch's management address).
func (a *analyzer) routes(s model.Segment) bool {
	if !s.AdminUp || s.OperState == model.OperDisabled || !a.routers[s.DeviceID] {
		return false
	}
	for _, ad := range s.Addresses {
		if !slices.Contains([]string{"dhcp", "dhcpv6", "slaac", "pppoe"}, ad.Source) {
			return true
		}
	}
	return len(s.Addresses) == 0
}

// taggedList returns the port's tagged VLANs, sorted.
func (m membership) taggedList() []int { return slices.Sorted(maps.Keys(m.tagged)) }

// otherPVID reports whether m classifies untagged frames into a VLAN other than v.
func otherPVID(m membership, v int) bool { return m.pvid != nil && *m.pvid != v }

// name returns a device's display name, or its ID.
func (a *analyzer) name(dev string) string { return cmp.Or(a.names[dev], dev) }

// hopState combines the two ends of a link; "" means neither end carries the VLAN.
func hopState(from, to Mode) HopState {
	switch {
	case from == ModeNone && to == ModeNone:
		return ""
	case from == ModeUnknown && to == ModeNone || from == ModeNone && to == ModeUnknown:
		// The VLAN cannot cross the end known not to carry it, whatever the other end does.
		return HopDeadEnd
	case from == ModeUnknown || to == ModeUnknown:
		return HopUnverified
	case from == to:
		return HopCarried
	case from == ModeNone || to == ModeNone:
		return HopDeadEnd
	}
	return HopMismatch
}

// leaks flags wired links whose two ends both report a PVID and disagree.
func (a *analyzer) leaks() []Finding {
	out := []Finding{}
	for _, e := range a.edges {
		if e.Kind == topology.EdgeWirelessUplink {
			continue
		}
		from, to := a.port(e.FromID, e.FromLink), a.port(e.ToID, e.ToLink)
		if from.pvid == nil || to.pvid == nil || *from.pvid == *to.pvid || !validVLAN(*from.pvid) || !validVLAN(*to.pvid) {
			continue
		}
		out = append(out, Finding{
			Kind: FindingLeak, Severity: SeverityCrit, VLANs: []int{*from.pvid, *to.pvid}, EdgeID: e.ID,
			DeviceID: e.FromID, Link: e.FromLink, PeerID: e.ToID, PeerLink: e.ToLink, Stranded: []string{},
			Message: fmt.Sprintf("Untagged frames cross between VLAN %d on %s:%s and VLAN %d on %s:%s",
				*from.pvid, a.name(e.FromID), e.FromLink, *to.pvid, a.name(e.ToID), e.ToLink),
		})
	}
	return out
}

// reach returns every device connected to one of starts in adj, starts included.
func reach(adj map[string][]string, starts []string) map[string]bool {
	seen := map[string]bool{}
	queue := slices.Clone(starts)
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		if seen[d] {
			continue
		}
		seen[d] = true
		queue = append(queue, adj[d]...)
	}
	return seen
}
