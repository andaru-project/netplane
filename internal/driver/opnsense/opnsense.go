// SPDX-License-Identifier: AGPL-3.0-or-later

// Package opnsense polls an OPNsense firewall over its core REST API, authenticated with an
// API key and secret. Endpoints and fields follow opnsense/core 25.1 to 26.7.
//
// OPNsense checks the request path against the user's privileges case-sensitively, and 25.1
// spells some paths in camelCase where 25.7 and later use snake_case: those calls try the
// snake_case path first and the camelCase one after a 403 or 404.
package opnsense

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/andaru-project/netplane/internal/driver"
	"github.com/andaru-project/netplane/internal/model"
)

// wgOnlineAge is how recent a WireGuard handshake must be for the peer to count as up; it is
// OPNsense's own threshold for "online" in its WireGuard status page.
const wgOnlineAge = 300 * time.Second

// Driver polls an OPNsense firewall.
type Driver struct{}

// New returns a new OPNsense REST API driver.
func New() *Driver { return &Driver{} }

// Kind returns "opnsense".
func (d *Driver) Kind() string { return "opnsense" }

// ValidateConfig checks required OPNsense target configuration.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("opnsense target %q: endpoint is required", cfg.ID)
	}
	if cfg.ResolvedSecrets["api_key"] == "" || cfg.ResolvedSecrets["api_secret"] == "" {
		return fmt.Errorf("opnsense target %q: api_key and api_secret secrets are required", cfg.ID)
	}
	return driver.CheckCredentialTransport(cfg)
}

// api is one target's authenticated REST client.
type api struct {
	client      *http.Client
	base        string
	key, secret string
}

func newAPI(cfg driver.TargetConfig, timeout time.Duration) *api {
	return &api{
		client: driver.NewHTTPClient(cfg, timeout),
		base:   strings.TrimRight(cfg.Endpoint, "/"),
		key:    cfg.ResolvedSecrets["api_key"],
		secret: cfg.ResolvedSecrets["api_secret"],
	}
}

// statusError is a non-2xx answer from the REST API.
type statusError struct {
	code    int
	message string
}

func (e *statusError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("HTTP %d", e.code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.code, e.message)
}

func httpStatus(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.code
	}
	return 0
}

// do sends body (when not nil) as JSON and decodes the response into out.
func (a *api) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reqBody)
	if err != nil {
		return err
	}
	// OPNsense rejects an empty body sent as JSON, so only a real body gets the header.
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(a.key, a.secret)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Auth failures carry "message", application errors "errorMessage".
		var e struct {
			Message      string `json:"message"`
			ErrorMessage string `json:"errorMessage"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		return &statusError{code: resp.StatusCode, message: cmp.Or(e.Message, e.ErrorMessage)}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// get tries each path in order, moving on only when the API refuses or lacks it (403, 404).
func (a *api) get(ctx context.Context, out any, paths ...string) error {
	var err error
	for _, p := range paths {
		if err = a.do(ctx, http.MethodGet, p, nil, out); err == nil {
			return nil
		}
		if c := httpStatus(err); c != http.StatusForbidden && c != http.StatusNotFound {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return fmt.Errorf("%s: %w", paths[len(paths)-1], err)
}

// numStr decodes a JSON string, number or null as text: OPNsense returns many counters as
// strings, and some fields changed between the two across releases.
type numStr string

func (n *numStr) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*n = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*n = numStr(s)
		return nil
	}
	*n = numStr(b)
	return nil
}

func (n numStr) uint() uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(string(n)), 10, 64)
	return v
}

func (n numStr) int64() (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(string(n)), 10, 64)
	return v, err == nil
}

// float reads the leading number of a value such as "4.2 ms" or "0.0 %"; "~" (unmonitored)
// and other text give nil.
func (n numStr) float() *float64 {
	f := strings.Fields(string(n))
	if len(f) == 0 {
		return nil
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return nil
	}
	return &v
}

type ifaceRow struct {
	Device      string   `json:"device"`
	Identifier  string   `json:"identifier"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled"`
	Status      string   `json:"status"`
	Flags       []string `json:"flags"`
	MTU         numStr   `json:"mtu"`
	MAC         string   `json:"macaddr"`
	MACHW       string   `json:"macaddr_hw"`
	Media       string   `json:"media"`
	IsPhysical  bool     `json:"is_physical"`
	Addr4       string   `json:"addr4"`
	IPv4        []struct {
		IPAddr string `json:"ipaddr"`
	} `json:"ipv4"`
	IPv6 []struct {
		IPAddr string `json:"ipaddr"`
	} `json:"ipv6"`
	VLAN *struct {
		Tag    numStr `json:"tag"`
		Parent string `json:"parent"`
	} `json:"vlan"`
	Statistics map[string]numStr `json:"statistics"`
}

// leaseRow is a lease row from Kea, dnsmasq or ISC dhcpd; each backend fills its own fields.
type leaseRow struct {
	Address  string `json:"address"`
	HWAddr   string `json:"hwaddr"` // Kea, dnsmasq
	MAC      string `json:"mac"`    // ISC
	Hostname string `json:"hostname"`
	Expire   numStr `json:"expire"` // Kea, dnsmasq: epoch seconds
	IfName   string `json:"if_name"`
	If       string `json:"if"`
	Type     string `json:"type"` // ISC: dynamic or static
}

// Poll reads identity, interfaces, gateways, ARP/NDP, DHCP leases, pf states and WireGuard.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	a := newAPI(cfg, 15*time.Second)
	defer a.client.CloseIdleConnections()

	var sysInfo struct {
		Name     string   `json:"name"`
		Versions []string `json:"versions"`
	}
	if err := a.get(ctx, &sysInfo, "/api/diagnostics/system/system_information", "/api/diagnostics/system/systemInformation"); err != nil {
		return nil, fmt.Errorf("opnsense system information: %w", err)
	}
	var ifResp []ifaceRow
	if err := a.get(ctx, &ifResp, "/api/interfaces/overview/interfaces_info/1"); err != nil {
		return nil, fmt.Errorf("opnsense interfaces: %w", err)
	}

	// Best-effort calls: a failure marks its section unavailable so the store keeps the old data.
	snap := &driver.Snapshot{TargetID: cfg.ID}
	var sysTime struct {
		Uptime  string `json:"uptime"`
		LoadAvg string `json:"loadavg"`
	}
	_ = a.get(ctx, &sysTime, "/api/diagnostics/system/system_time", "/api/diagnostics/system/systemTime")
	var sysRes struct {
		Memory struct {
			Total numStr `json:"total"`
			Used  numStr `json:"used"`
		} `json:"memory"`
	}
	_ = a.get(ctx, &sysRes, "/api/diagnostics/system/system_resources", "/api/diagnostics/system/systemResources")

	var gwResp struct {
		Items []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Status  string `json:"status"`
			Loss    numStr `json:"loss"`
			Delay   numStr `json:"delay"`
			Monitor string `json:"monitor"`
		} `json:"items"`
		Status string `json:"status"`
	}
	if err := a.get(ctx, &gwResp, "/api/routes/gateway/status"); err != nil || gwResp.Status == "failed" {
		snap.MarkUnavailable(driver.SectionGateways)
	}

	type neighborRow struct {
		IP        string `json:"ip"`
		MAC       string `json:"mac"`
		Intf      string `json:"intf"`
		Expired   bool   `json:"expired"`
		Permanent bool   `json:"permanent"`
	}
	var arpResp, ndpResp struct {
		Rows []neighborRow `json:"rows"`
	}
	// search_arp and search_ndp keep one spelling in every release.
	errARP := a.get(ctx, &arpResp, "/api/diagnostics/interface/search_arp")
	errNDP := a.get(ctx, &ndpResp, "/api/diagnostics/interface/search_ndp")
	if errARP != nil || errNDP != nil {
		snap.MarkUnavailable(driver.SectionNeighbors)
	}

	// Any of the three DHCP servers may run; one that is not installed answers 404.
	var leases []leaseRow
	dhcpOK, dhcpFailed := false, false
	for _, p := range []string{"/api/kea/leases4/search", "/api/dnsmasq/leases/search", "/api/dhcpv4/leases/search_lease"} {
		var r struct {
			Rows []leaseRow `json:"rows"`
		}
		err := a.get(ctx, &r, p)
		switch {
		case err == nil:
			dhcpOK = true
			leases = append(leases, r.Rows...)
		case httpStatus(err) != http.StatusNotFound:
			dhcpFailed = true
		}
	}
	if dhcpFailed && !dhcpOK {
		snap.MarkUnavailable(driver.SectionDHCP)
	}

	var pfStates struct {
		Current numStr `json:"current"`
		Limit   numStr `json:"limit"`
	}
	errPF := a.get(ctx, &pfStates, "/api/diagnostics/firewall/pf_states")

	var wgResp struct {
		Rows []struct {
			If              string `json:"if"`
			Type            string `json:"type"`
			PublicKey       string `json:"public-key"`
			Endpoint        string `json:"endpoint"`
			LatestHandshake numStr `json:"latest-handshake"`
			TransferRx      numStr `json:"transfer-rx"`
			TransferTx      numStr `json:"transfer-tx"`
			Name            string `json:"name"`
		} `json:"rows"`
	}
	errWG := a.get(ctx, &wgResp, "/api/wireguard/service/show")
	// No WireGuard plugin (404) means no tunnels, not stale ones.
	if errWG != nil && httpStatus(errWG) != http.StatusNotFound {
		snap.MarkUnavailable(driver.SectionVPNs)
	}

	now := time.Now().UTC()
	devID := "opnsense:" + cfg.ID
	product, version := parseVersion(sysInfo.Versions)
	name := cmp.Or(cfg.Name, sysInfo.Name, cfg.ID)

	segOfDevice := map[string]string{}
	var rawMACs []string
	mgmtIP := ""
	for _, r := range ifResp {
		if r.Identifier != "" {
			segOfDevice[r.Device] = r.Identifier
		}
		if r.IsPhysical {
			rawMACs = append(rawMACs, cmp.Or(r.MACHW, r.MAC))
		}
		if r.Identifier == "lan" && mgmtIP == "" {
			mgmtIP, _, _ = strings.Cut(r.Addr4, "/")
		}
	}
	// Lowest MAC keeps PrimaryMAC independent of interface order, as for pfSense.
	macs, _ := driver.CollectMACs(rawMACs)
	primaryMAC := ""
	if len(macs) > 0 {
		primaryMAC = slices.Min(macs)
	}

	caps := []model.Capability{
		model.CapLinks, model.CapSegments, model.CapGateway, model.CapFirewall,
		model.CapNeighbors, model.CapDHCP, model.CapFirmwareInfo, model.CapDiagPing,
	}
	stats := model.SystemStats{MemTotalBytes: sysRes.Memory.Total.uint(), MemUsedBytes: sysRes.Memory.Used.uint()}
	if f := numStr(strings.Split(sysTime.LoadAvg, ",")[0]).float(); f != nil {
		stats.Load1 = *f
	}

	snap.CollectedAt = now
	snap.Devices = []model.Device{{
		ID:           devID,
		Driver:       "opnsense",
		TargetID:     cfg.ID,
		Name:         name,
		Hostname:     sysInfo.Name,
		Vendor:       "Deciso",
		Model:        product,
		Role:         model.RoleFirewall,
		PrimaryMAC:   primaryMAC,
		MACs:         macs,
		MgmtIP:       mgmtIP,
		Firmware:     model.FirmwareInfo{Current: version},
		UptimeSec:    parseUptime(sysTime.Uptime),
		Online:       true,
		LastSeen:     now,
		Capabilities: caps,
		SystemStats:  stats,
	}}

	for i, r := range ifResp {
		adminUp := slices.Contains(r.Flags, "up")
		if r.Enabled != nil {
			adminUp = adminUp && *r.Enabled
		}
		linkUp := r.Status == "up"
		oper := model.OperFromAdminLink(adminUp, linkUp)
		var mtu *int
		if v, err := strconv.Atoi(strings.TrimSpace(string(r.MTU))); err == nil {
			mtu = &v
		}
		if r.IsPhysical {
			speed, duplex := parseMedia(r.Media)
			l := model.Link{
				DeviceID:  devID,
				Index:     i + 1,
				IfName:    r.Device,
				Label:     r.Description,
				MAC:       r.MAC,
				AdminUp:   adminUp,
				LinkUp:    linkUp,
				OperState: oper,
				Duplex:    duplex,
				MTU:       mtu,
				Media:     mediaType(r.Media, speed),
			}
			if linkUp {
				l.SpeedMbps = speed
			}
			if st := r.Statistics; st != nil {
				l.Counters = &model.PortCounters{
					RxBytes:   st["bytes received"].uint(),
					TxBytes:   st["bytes transmitted"].uint(),
					RxPackets: st["packets received"].uint(),
					TxPackets: st["packets transmitted"].uint(),
					RxErrors:  st["input errors"].uint(),
					TxErrors:  st["output errors"].uint(),
					RxDropped: st["input queue drops"].uint(),
				}
			}
			snap.Links = append(snap.Links, l)
		}
		if r.Identifier == "" {
			continue // unassigned: a port, not a logical interface
		}
		seg := model.Segment{
			DeviceID:   devID,
			ID:         r.Identifier,
			Name:       r.Description,
			Kind:       "ethernet",
			ParentLink: r.Device,
			MTU:        mtu,
			AdminUp:    adminUp,
			OperState:  oper,
			Addresses:  []model.IPAddress{},
		}
		if r.VLAN != nil {
			seg.Kind, seg.ParentLink = "vlan", r.VLAN.Parent
			if v, ok := r.VLAN.Tag.int64(); ok {
				tag := int(v)
				seg.VLANID = &tag
			}
		}
		for _, ip := range r.IPv4 {
			if a, ok := parsePrefix(ip.IPAddr, model.IPv4); ok {
				seg.Addresses = append(seg.Addresses, a)
			}
		}
		for _, ip := range r.IPv6 {
			if a, ok := parsePrefix(ip.IPAddr, model.IPv6); ok {
				seg.Addresses = append(seg.Addresses, a)
			}
		}
		snap.Segments = append(snap.Segments, seg)
	}

	for _, gw := range gwResp.Items {
		family := model.IPv4
		if addr, err := netip.ParseAddr(gw.Address); err == nil && addr.Is6() {
			family = model.IPv6
		}
		snap.Gateways = append(snap.Gateways, model.GatewayStatus{
			DeviceID:  devID,
			Name:      gw.Name,
			Family:    family,
			GatewayIP: gw.Address,
			MonitorIP: strings.Trim(gw.Monitor, "~"),
			// dpinger reports "none" for a healthy gateway; delay and loss still forward traffic.
			Online:  gw.Status != "down" && gw.Status != "force_down",
			RTTMs:   gw.Delay.float(),
			LossPct: gw.Loss.float(),
		})
	}

	addNeighbors := func(rows []neighborRow, family model.IPFamily) {
		for _, n := range rows {
			mac, err := model.NormalizeMAC(n.MAC)
			if err != nil {
				continue
			}
			ip, _, _ := strings.Cut(n.IP, "%") // NDP reports link-local addresses with a zone
			state := ""
			switch {
			case n.Permanent:
				state = "permanent"
			case family == model.IPv4 && !n.Expired:
				state = "reachable"
			}
			snap.Neighbors = append(snap.Neighbors, model.NeighborEntry{
				DeviceID: devID, Family: family, IP: ip, MAC: mac,
				SegmentName: cmp.Or(segOfDevice[n.Intf], n.Intf), State: state,
			})
		}
	}
	addNeighbors(arpResp.Rows, model.IPv4)
	addNeighbors(ndpResp.Rows, model.IPv6)

	for _, l := range leases {
		mac, err := model.NormalizeMAC(cmp.Or(l.HWAddr, l.MAC))
		if err != nil || l.Address == "" {
			continue
		}
		lease := model.DhcpLease{
			DeviceID: devID,
			Family:   model.IPv4,
			IP:       l.Address,
			MAC:      mac,
			Hostname: l.Hostname,
			Pool:     cmp.Or(l.IfName, l.If),
			Static:   l.Type == "static",
		}
		if v, ok := l.Expire.int64(); ok && v > 0 {
			t := time.Unix(v, 0).UTC()
			lease.ExpiresAt = &t
		}
		snap.DHCP = append(snap.DHCP, lease)
	}

	if errPF == nil {
		fw := model.FirewallState{DeviceID: devID, Engine: "pf", Services: []model.ServiceStatus{}}
		if v, ok := pfStates.Current.int64(); ok {
			fw.StatesUsed = &v
		}
		if v, ok := pfStates.Limit.int64(); ok {
			fw.StatesMax = &v
		}
		snap.Firewalls = append(snap.Firewalls, fw)
	} else {
		snap.MarkUnavailable(driver.SectionFirewalls)
	}

	for _, p := range wgResp.Rows {
		if p.Type != "peer" {
			continue
		}
		var hs *time.Time
		up := false
		if v, ok := p.LatestHandshake.int64(); ok && v > 0 {
			t := time.Unix(v, 0).UTC()
			hs = &t
			up = now.Sub(t) <= wgOnlineAge
		}
		snap.VPNs = append(snap.VPNs, model.VpnTunnel{
			DeviceID:      devID,
			ID:            p.If + ":" + p.PublicKey,
			Name:          cmp.Or(p.Name, p.If+" peer"),
			Kind:          "wireguard",
			Peer:          p.PublicKey,
			Endpoint:      p.Endpoint,
			Up:            up,
			LastHandshake: hs,
			RxBytes:       p.TransferRx.uint(),
			TxBytes:       p.TransferTx.uint(),
		})
	}
	if len(snap.VPNs) > 0 {
		snap.Devices[0].Capabilities = append(snap.Devices[0].Capabilities, model.CapVPN)
	}
	return snap, nil
}

// parseVersion splits "OPNsense 26.7.9-amd64" into product and version.
func parseVersion(versions []string) (product, version string) {
	if len(versions) == 0 {
		return "OPNsense", ""
	}
	f := strings.Fields(versions[0])
	if len(f) < 2 {
		return "OPNsense", ""
	}
	version, _, _ = strings.Cut(f[len(f)-1], "-")
	return strings.Join(f[:len(f)-1], " "), version
}

var uptimeRE = regexp.MustCompile(`^(?:(\d+) days?, )?(\d+):(\d{2}):(\d{2})$`)

// parseUptime reads system_time's uptime ("12 days, 03:04:05" or "03:04:05"); other text
// gives 0.
func parseUptime(s string) uint64 {
	m := uptimeRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n := func(i int) uint64 { v, _ := strconv.ParseUint(m[i], 10, 64); return v }
	return n(1)*86400 + n(2)*3600 + n(3)*60 + n(4)
}

var mediaRE = regexp.MustCompile(`(?i)\b(\d+)(G?)base-?(\w*)`)

// parseMedia reads the speed and duplex from ifconfig media text ("1000baseT <full-duplex>",
// "10Gbase-SR <full-duplex,rxpause,txpause>").
func parseMedia(media string) (speedMbps int, duplex string) {
	if m := mediaRE.FindStringSubmatch(media); m != nil {
		speedMbps, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			speedMbps *= 1000
		}
	}
	switch {
	case strings.Contains(media, "full-duplex"):
		duplex = "full"
	case strings.Contains(media, "half-duplex"):
		duplex = "half"
	}
	return speedMbps, duplex
}

// mediaType maps ifconfig media to a port type: optical and direct-attach media (SR, LR, CR,
// Twinax, ...) are SFP cages, the rest copper by speed.
func mediaType(media string, speedMbps int) model.MediaType {
	m := mediaRE.FindStringSubmatch(media)
	if m != nil {
		if t := strings.ToUpper(m[3]); t != "" && (!strings.HasPrefix(t, "T") || strings.HasPrefix(t, "TWINAX")) {
			if speedMbps > 1000 {
				return model.MediaSFPPl
			}
			return model.MediaSFP
		}
	}
	switch {
	case speedMbps >= 10000:
		return model.Media10GE
	case speedMbps >= 5000:
		return model.Media5GE
	case speedMbps >= 2500:
		return model.Media25GE
	case speedMbps >= 1000:
		return model.MediaGE
	case speedMbps > 0:
		return model.MediaFE
	}
	return model.MediaOther
}

// parsePrefix reads "192.0.2.1/24" or "fe80::1%igc1/64".
func parsePrefix(s string, family model.IPFamily) (model.IPAddress, bool) {
	addr, bits, ok := strings.Cut(s, "/")
	addr, _, _ = strings.Cut(addr, "%")
	if !ok || addr == "" {
		return model.IPAddress{}, false
	}
	n, err := strconv.Atoi(bits)
	if err != nil {
		return model.IPAddress{}, false
	}
	return model.IPAddress{Family: family, Address: addr, PrefixLength: n}, true
}
