// SPDX-License-Identifier: AGPL-3.0-or-later
package topology

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/andrebrait/netplane/internal/model"
	"github.com/andrebrait/netplane/internal/store"
)

// NodeKind distinguishes infrastructure devices from end-user clients.
type NodeKind string

const (
	NodeDevice NodeKind = "device"
	NodeClient NodeKind = "client"
)

// EdgeKind classifies a topology link.
type EdgeKind string

const (
	EdgeWired          EdgeKind = "wired"
	EdgeLAG            EdgeKind = "lag"
	EdgeWirelessUplink EdgeKind = "wireless_uplink"
	EdgeWirelessClient EdgeKind = "wireless_client"
)

// EdgeEvidence explains how an edge was discovered.
type EdgeEvidence string

const (
	EvidenceLLDP           EdgeEvidence = "lldp"
	EvidenceFDBUnique      EdgeEvidence = "fdb_unique"
	EvidenceFDBInferred    EdgeEvidence = "fdb_inferred"
	EvidenceWirelessUplink EdgeEvidence = "wireless_uplink"
	EvidenceWirelessClient EdgeEvidence = "wireless_client"
)

// Node represents one vertex in the topology graph.
type Node struct {
	ID     string   `json:"id"`
	Kind   NodeKind `json:"kind"`
	Role   string   `json:"role,omitempty"`
	Driver string   `json:"driver,omitempty"`
	Label  string   `json:"label"`
	MAC    string   `json:"mac,omitempty"`
	IP     string   `json:"ip,omitempty"`
	Online bool     `json:"online"`
}

// Edge represents a resolved connection between two nodes.
type Edge struct {
	ID          string       `json:"id"`
	FromID      string       `json:"from_id"`
	FromLink    string       `json:"from_link,omitempty"`
	ToID        string       `json:"to_id"`
	ToLink      string       `json:"to_link,omitempty"`
	Kind        EdgeKind     `json:"kind"`
	Evidence    EdgeEvidence `json:"evidence"`
	Confidence  float64      `json:"confidence"`
	SpeedMbps   int          `json:"speed_mbps,omitempty"`
	MemberLinks []string     `json:"member_links"`
}

// Graph is the complete deterministic cross-vendor L2/L3/Wi-Fi topology snapshot.
type Graph struct {
	GeneratedAt time.Time `json:"generated_at"`
	RootIDs     []string  `json:"root_ids"`
	Nodes       []Node    `json:"nodes"`
	Edges       []Edge    `json:"edges"`
}

type pairKey struct{ a, b string }

func makePair(x, y string) pairKey {
	if x < y {
		return pairKey{x, y}
	}
	return pairKey{y, x}
}

type devPortKey struct{ devID, link string }

// Solve deterministically constructs a cross-vendor topology graph from merged network state.
func Solve(state *store.NetworkState, now time.Time) Graph {
	g := Graph{
		GeneratedAt: now.UTC(),
		RootIDs:     []string{},
		Nodes:       []Node{},
		Edges:       []Edge{},
	}
	if state == nil {
		return g
	}

	devByID := make(map[string]model.Device, len(state.Devices))
	devByMAC := make(map[string]string)
	devByIP := make(map[string]string)
	devByName := make(map[string]string)

	for _, d := range state.Devices {
		devByID[d.ID] = d
		if d.PrimaryMAC != "" {
			devByMAC[d.PrimaryMAC] = d.ID
		}
		for _, m := range d.MACs {
			devByMAC[m] = d.ID
		}
		if d.MgmtIP != "" {
			devByIP[d.MgmtIP] = d.ID
		}
		if d.Hostname != "" {
			devByName[strings.ToLower(d.Hostname)] = d.ID
		}
		if d.Name != "" {
			devByName[strings.ToLower(d.Name)] = d.ID
		}
		label := d.Name
		if label == "" {
			label = d.Hostname
		}
		if label == "" {
			label = d.ID
		}
		g.Nodes = append(g.Nodes, Node{
			ID:     d.ID,
			Kind:   NodeDevice,
			Role:   string(d.Role),
			Driver: d.Driver,
			Label:  label,
			MAC:    d.PrimaryMAC,
			IP:     d.MgmtIP,
			Online: d.Online,
		})
	}

	// Index physical links and LAG memberships.
	linkSpeed := make(map[devPortKey]int)
	linkToLAG := make(map[devPortKey]model.LagGroup)
	lagByName := make(map[devPortKey]model.LagGroup)
	for _, lag := range state.LagGroups {
		lagByName[devPortKey{lag.DeviceID, lag.Name}] = lag
		for _, m := range lag.MemberLinks {
			linkToLAG[devPortKey{lag.DeviceID, m}] = lag
		}
	}
	for _, l := range state.Links {
		if l.MAC != "" {
			if _, exists := devByMAC[l.MAC]; !exists {
				devByMAC[l.MAC] = l.DeviceID
			}
		}
		linkSpeed[devPortKey{l.DeviceID, l.IfName}] = l.SpeedMbps
	}

	normalizePort := func(devID, portName string) (normName string, isLag bool, members []string, speed int) {
		if lag, ok := linkToLAG[devPortKey{devID, portName}]; ok {
			ms := append([]string(nil), lag.MemberLinks...)
			sort.Strings(ms)
			return lag.Name, true, ms, lag.SpeedMbps
		}
		if lag, ok := lagByName[devPortKey{devID, portName}]; ok {
			ms := append([]string(nil), lag.MemberLinks...)
			sort.Strings(ms)
			return lag.Name, true, ms, lag.SpeedMbps
		}
		return portName, false, []string{}, linkSpeed[devPortKey{devID, portName}]
	}

	resolvePeer := func(chassisID, mgmtIP, sysName string) string {
		if chassisID != "" {
			if mac, err := model.NormalizeMAC(chassisID); err == nil {
				if id, ok := devByMAC[mac]; ok {
					return id
				}
			}
			if id, ok := devByMAC[strings.ToLower(chassisID)]; ok {
				return id
			}
		}
		if mgmtIP != "" {
			if id, ok := devByIP[mgmtIP]; ok {
				return id
			}
		}
		if sysName != "" {
			if id, ok := devByName[strings.ToLower(sysName)]; ok {
				return id
			}
		}
		return ""
	}

	infraEdges := make(map[pairKey]*Edge)
	infraPorts := make(map[devPortKey]bool)

	// 1. Direct LLDP edges (confidence 1.0).
	for _, n := range state.LLDP {
		if _, ok := devByID[n.DeviceID]; !ok {
			continue
		}
		peerID := resolvePeer(n.ChassisID, n.RemoteMgmtIP, n.RemoteSysName)
		if peerID == "" || peerID == n.DeviceID {
			continue
		}
		localName, localLag, members, spd := normalizePort(n.DeviceID, n.LocalLink)
		remoteName, remoteLag, rMembers, rSpd := normalizePort(peerID, n.RemotePort)

		pk := makePair(n.DeviceID, peerID)
		kind := EdgeWired
		if localLag || remoteLag {
			kind = EdgeLAG
		}
		if spd == 0 {
			spd = rSpd
		}
		if len(members) == 0 && len(rMembers) > 0 {
			members = rMembers
		}

		if existing, ok := infraEdges[pk]; ok {
			// Fill missing remote port name from the reverse LLDP record.
			if existing.FromID == n.DeviceID && existing.FromLink == "" {
				existing.FromLink = localName
			}
			if existing.ToID == n.DeviceID && existing.ToLink == "" {
				existing.ToLink = localName
			}
			if kind == EdgeLAG {
				existing.Kind = EdgeLAG
			}
			if existing.SpeedMbps == 0 && spd > 0 {
				existing.SpeedMbps = spd
			}
			if len(existing.MemberLinks) == 0 && len(members) > 0 {
				existing.MemberLinks = members
			}
		} else {
			e := &Edge{
				FromID:      n.DeviceID,
				FromLink:    localName,
				ToID:        peerID,
				ToLink:      remoteName,
				Kind:        kind,
				Evidence:    EvidenceLLDP,
				Confidence:  1.0,
				SpeedMbps:   spd,
				MemberLinks: members,
			}
			infraEdges[pk] = e
		}
		infraPorts[devPortKey{n.DeviceID, localName}] = true
		if remoteName != "" {
			infraPorts[devPortKey{peerID, remoteName}] = true
		}
	}

	// 2. Wireless mesh uplinks (confidence 0.95).
	for _, u := range state.WirelessUplinks {
		if _, ok := devByID[u.DeviceID]; !ok {
			continue
		}
		peerID := u.PeerDeviceID
		if peerID == "" && u.PeerMAC != "" {
			peerID = devByMAC[u.PeerMAC]
		}
		if peerID == "" || peerID == u.DeviceID {
			continue
		}
		pk := makePair(u.DeviceID, peerID)
		if _, exists := infraEdges[pk]; exists {
			continue
		}
		spd := u.TxRateMbps
		if u.RxRateMbps > spd {
			spd = u.RxRateMbps
		}
		infraEdges[pk] = &Edge{
			FromID:      peerID,
			ToID:        u.DeviceID,
			Kind:        EdgeWirelessUplink,
			Evidence:    EvidenceWirelessUplink,
			Confidence:  0.95,
			SpeedMbps:   spd,
			MemberLinks: []string{},
		}
	}

	// 3. FDB infrastructure edges (confidence 0.85), avoiding transitive shortcuts.
	portMACs := make(map[devPortKey]map[string]bool)
	for _, f := range state.FDB {
		if f.IsLocal || f.MAC == "" {
			continue
		}
		if _, ok := devByID[f.DeviceID]; !ok {
			continue
		}
		normName, _, _, _ := normalizePort(f.DeviceID, f.LinkName)
		key := devPortKey{f.DeviceID, normName}
		if portMACs[key] == nil {
			portMACs[key] = make(map[string]bool)
		}
		portMACs[key][f.MAC] = true
	}

	// Build adjacency from already-known edges to avoid FDB shortcuts across multi-hop LLDP chains.
	connected := func(src, dst string) bool {
		if src == dst {
			return true
		}
		adj := make(map[string][]string)
		for pk := range infraEdges {
			adj[pk.a] = append(adj[pk.a], pk.b)
			adj[pk.b] = append(adj[pk.b], pk.a)
		}
		visited := map[string]bool{src: true}
		q := []string{src}
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			for _, nxt := range adj[cur] {
				if nxt == dst {
					return true
				}
				if !visited[nxt] {
					visited[nxt] = true
					q = append(q, nxt)
				}
			}
		}
		return false
	}

	type fdbCandidate struct {
		srcID   string
		srcPort string
		dstID   string
		card    int
	}
	var cands []fdbCandidate
	for pk, macSet := range portMACs {
		for mac := range macSet {
			dstID, isDev := devByMAC[mac]
			if !isDev || dstID == pk.devID {
				continue
			}
			cands = append(cands, fdbCandidate{
				srcID:   pk.devID,
				srcPort: pk.link,
				dstID:   dstID,
				card:    len(macSet),
			})
		}
	}
	// Smallest port MAC cardinality first (closest hop first), deterministic tie-break.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].card != cands[j].card {
			return cands[i].card < cands[j].card
		}
		if cands[i].srcID != cands[j].srcID {
			return cands[i].srcID < cands[j].srcID
		}
		if cands[i].dstID != cands[j].dstID {
			return cands[i].dstID < cands[j].dstID
		}
		return cands[i].srcPort < cands[j].srcPort
	})

	for _, c := range cands {
		normName, isLag, members, spd := normalizePort(c.srcID, c.srcPort)
		// Any port seeing another infrastructure device's MAC is a transit/uplink port,
		// never a leaf client access port — mark it even if an LLDP/earlier path already exists.
		alreadyInfra := infraPorts[devPortKey{c.srcID, normName}]
		infraPorts[devPortKey{c.srcID, normName}] = true
		if connected(c.srcID, c.dstID) || alreadyInfra {
			continue
		}
		kind := EdgeWired
		if isLag {
			kind = EdgeLAG
		}
		pk := makePair(c.srcID, c.dstID)
		infraEdges[pk] = &Edge{
			FromID:      c.srcID,
			FromLink:    normName,
			ToID:        c.dstID,
			Kind:        kind,
			Evidence:    EvidenceFDBInferred,
			Confidence:  0.85,
			SpeedMbps:   spd,
			MemberLinks: members,
		}
	}

	// 4. Identify root devices and orient edges away from roots via BFS.
	for _, d := range state.Devices {
		if d.Role == model.RoleGateway || d.Role == model.RoleFirewall || d.HasCapability(model.CapGateway) {
			g.RootIDs = append(g.RootIDs, d.ID)
		}
	}
	sort.Strings(g.RootIDs)

	depth := make(map[string]int, len(state.Devices))
	if len(g.RootIDs) > 0 {
		adj := make(map[string][]string)
		for pk := range infraEdges {
			adj[pk.a] = append(adj[pk.a], pk.b)
			adj[pk.b] = append(adj[pk.b], pk.a)
		}
		for k := range adj {
			sort.Strings(adj[k])
		}
		q := make([]string, 0, len(g.RootIDs))
		for _, r := range g.RootIDs {
			depth[r] = 0
			q = append(q, r)
		}
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			for _, nxt := range adj[cur] {
				if _, seen := depth[nxt]; !seen {
					depth[nxt] = depth[cur] + 1
					q = append(q, nxt)
				}
			}
		}
	}

	for _, e := range infraEdges {
		dFrom, okFrom := depth[e.FromID]
		dTo, okTo := depth[e.ToID]
		swap := false
		if okFrom && okTo {
			if dTo < dFrom || (dTo == dFrom && e.ToID < e.FromID) {
				swap = true
			}
		} else if !okFrom && okTo {
			swap = true
		} else if !okFrom && !okTo && e.ToID < e.FromID {
			swap = true
		}
		if swap {
			e.FromID, e.ToID = e.ToID, e.FromID
			e.FromLink, e.ToLink = e.ToLink, e.FromLink
		}
		e.ID = fmt.Sprintf("%s:%s->%s:%s", e.FromID, e.FromLink, e.ToID, e.ToLink)
		if e.MemberLinks == nil {
			e.MemberLinks = []string{}
		}
		g.Edges = append(g.Edges, *e)
	}

	// 5. Correlate client metadata (IPs, hostnames) from Clients, DHCP, and Neighbors.
	type clientInfo struct {
		mac      string
		ip       string
		hostname string
		online   bool
		wireless bool
		devID    string
		link     string
		ssid     string
		speed    int
	}
	clientsByMAC := make(map[string]*clientInfo)
	ensureClient := func(mac string) *clientInfo {
		if c, ok := clientsByMAC[mac]; ok {
			return c
		}
		c := &clientInfo{mac: mac}
		clientsByMAC[mac] = c
		return c
	}

	for _, c := range state.Clients {
		if _, isDev := devByMAC[c.MAC]; isDev {
			continue
		}
		ci := ensureClient(c.MAC)
		if c.Hostname != "" {
			ci.hostname = c.Hostname
		}
		if len(c.IPs) > 0 && ci.ip == "" {
			ci.ip = c.IPs[0]
		}
		ci.online = ci.online || c.Online
		if c.ConnectionType == "wireless" && c.AttachedDeviceID != "" {
			ci.wireless = true
			ci.devID = c.AttachedDeviceID
			ci.ssid = c.SSID
			if c.TxRateMbps != nil {
				ci.speed = *c.TxRateMbps
			}
		} else if c.AttachedDeviceID != "" && ci.devID == "" {
			ci.devID = c.AttachedDeviceID
			ci.link = c.AttachedLink
		}
	}
	for _, l := range state.DHCP {
		if l.MAC == "" {
			continue
		}
		if _, isDev := devByMAC[l.MAC]; isDev {
			continue
		}
		ci := ensureClient(l.MAC)
		if ci.hostname == "" && l.Hostname != "" {
			ci.hostname = l.Hostname
		}
		if ci.ip == "" && l.IP != "" {
			ci.ip = l.IP
		}
	}
	for _, n := range state.Neighbors {
		if n.MAC == "" {
			continue
		}
		if _, isDev := devByMAC[n.MAC]; isDev {
			continue
		}
		ci := ensureClient(n.MAC)
		if ci.ip == "" && n.IP != "" {
			ci.ip = n.IP
		}
		if n.State == "reachable" || n.State == "permanent" {
			ci.online = true
		}
	}

	// Build leaf access-port candidates for wired clients from FDB.
	type leafMatch struct {
		devID string
		port  string
		card  int
	}
	clientLeaf := make(map[string][]leafMatch)
	for pk, macSet := range portMACs {
		if infraPorts[pk] {
			continue
		}
		for mac := range macSet {
			if _, isDev := devByMAC[mac]; isDev {
				continue
			}
			ensureClient(mac).online = true
			clientLeaf[mac] = append(clientLeaf[mac], leafMatch{
				devID: pk.devID,
				port:  pk.link,
				card:  len(macSet),
			})
		}
	}

	for mac, ci := range clientsByMAC {
		nodeID := "client:" + mac
		label := ci.hostname
		if label == "" {
			label = ci.ip
		}
		if label == "" {
			label = mac
		}
		g.Nodes = append(g.Nodes, Node{
			ID:     nodeID,
			Kind:   NodeClient,
			Label:  label,
			MAC:    mac,
			IP:     ci.ip,
			Online: ci.online,
		})

		if ci.wireless && ci.devID != "" {
			g.Edges = append(g.Edges, Edge{
				ID:          fmt.Sprintf("%s:%s->%s", ci.devID, ci.ssid, nodeID),
				FromID:      ci.devID,
				FromLink:    ci.ssid,
				ToID:        nodeID,
				Kind:        EdgeWirelessClient,
				Evidence:    EvidenceWirelessClient,
				Confidence:  0.95,
				SpeedMbps:   ci.speed,
				MemberLinks: []string{},
			})
			continue
		}

		if leaves := clientLeaf[mac]; len(leaves) > 0 {
			sort.Slice(leaves, func(i, j int) bool {
				if leaves[i].card != leaves[j].card {
					return leaves[i].card < leaves[j].card
				}
				if leaves[i].devID != leaves[j].devID {
					return leaves[i].devID < leaves[j].devID
				}
				return leaves[i].port < leaves[j].port
			})
			best := leaves[0]
			ev := EvidenceFDBInferred
			conf := 0.80
			if len(leaves) == 1 && best.card == 1 {
				ev = EvidenceFDBUnique
				conf = 0.90
			}
			g.Edges = append(g.Edges, Edge{
				ID:          fmt.Sprintf("%s:%s->%s", best.devID, best.port, nodeID),
				FromID:      best.devID,
				FromLink:    best.port,
				ToID:        nodeID,
				Kind:        EdgeWired,
				Evidence:    ev,
				Confidence:  conf,
				SpeedMbps:   linkSpeed[devPortKey{best.devID, best.port}],
				MemberLinks: []string{},
			})
		} else if ci.devID != "" {
			g.Edges = append(g.Edges, Edge{
				ID:          fmt.Sprintf("%s:%s->%s", ci.devID, ci.link, nodeID),
				FromID:      ci.devID,
				FromLink:    ci.link,
				ToID:        nodeID,
				Kind:        EdgeWired,
				Evidence:    EvidenceFDBInferred,
				Confidence:  0.75,
				MemberLinks: []string{},
			})
		}
	}

	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool { return g.Edges[i].ID < g.Edges[j].ID })
	return g
}
