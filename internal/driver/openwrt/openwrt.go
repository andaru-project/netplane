// SPDX-License-Identifier: AGPL-3.0-or-later
package openwrt

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

// Driver polls an OpenWrt router, AP, or DSA switch via uhttpd-mod-ubus HTTP JSON-RPC (/ubus).
// It keeps one HTTP client and ubus session per target across polls, logging in again only
// when rpcd rejects the session (rpcd expires idle sessions after 300 s by default).
type Driver struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// session is the per-target state kept between polls. The poller serializes polls of one
// target, so its fields need no lock of their own.
type session struct {
	key    sessionKey
	client *http.Client
	sid    string
	// denied holds object.method calls this account's ACL refuses even with a fresh session.
	// uhttpd answers both those and an expired session with the same -32002, so a call is only
	// put here after a re-login did not help, and never triggers a login again.
	denied map[string]bool
}

// sessionKey is what a session depends on: any change starts a new client and login.
type sessionKey struct {
	endpoint, username, password string
	insecure                     bool
}

// New returns a new OpenWrt ubus driver.
func New() *Driver { return &Driver{sessions: map[string]*session{}} }

func (d *Driver) session(cfg driver.TargetConfig) *session {
	key := sessionKey{cfg.Endpoint, cfg.ResolvedSecrets["username"], cfg.ResolvedSecrets["password"], cfg.BoolSetting("insecure_skip_verify", false)}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.sessions[cfg.ID]
	if s == nil || s.key != key {
		if s != nil {
			s.client.CloseIdleConnections()
		}
		s = &session{key: key, client: driver.NewHTTPClient(cfg, 15*time.Second), denied: map[string]bool{}}
		d.sessions[cfg.ID] = s
	}
	return s
}

// Kind returns "openwrt".
func (d *Driver) Kind() string { return "openwrt" }

// ValidateConfig checks required OpenWrt target settings.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("openwrt target %q: endpoint is required", cfg.ID)
	}
	if cfg.ResolvedSecrets["username"] == "" || cfg.ResolvedSecrets["password"] == "" {
		return fmt.Errorf("openwrt target %q: username and password secrets are required", cfg.ID)
	}
	return driver.CheckCredentialTransport(cfg)
}

// Poll authenticates via ubus session.login and fetches board/info/device/interface/wireless/host hints.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	sess := d.session(cfg)
	client := sess.client
	// Keep the session, not the connections: a kept-alive connection the router closes while
	// a request is in flight fails that POST, and polls are far apart anyway.
	defer client.CloseIdleConnections()

	ubusURL := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasSuffix(ubusURL, "/ubus") {
		ubusURL += "/ubus"
	}

	login := func() error {
		var loginRes struct {
			Session string `json:"ubus_rpc_session"`
		}
		sess.sid = ""
		err := ubusCall(ctx, client, ubusURL, "00000000000000000000000000000000", "session", "login", map[string]any{
			"username": cfg.ResolvedSecrets["username"],
			"password": cfg.ResolvedSecrets["password"],
		}, &loginRes)
		if err != nil {
			return fmt.Errorf("openwrt ubus login: %w", err)
		}
		if loginRes.Session == "" {
			return errors.New("openwrt ubus login: empty session")
		}
		sess.sid = loginRes.Session
		return nil
	}
	// A poll logs in at most once: up front when it holds no session, else on the first denial.
	relogged := sess.sid == ""
	if relogged {
		if err := login(); err != nil {
			return nil, err
		}
	}
	// call reuses the kept session. A call denied with a fresh session is refused by the ACL
	// and remembered as such until it succeeds again.
	var reloginErr error
	call := func(object, method string, params map[string]any, out any) error {
		name := object + "." + method
		if reloginErr != nil {
			return reloginErr // an earlier re-login failed this poll
		}
		err := ubusCall(ctx, client, ubusURL, sess.sid, object, method, params, out)
		if err == nil {
			delete(sess.denied, name) // the ACL allows it (again)
			return nil
		}
		if !errors.Is(err, errAccessDenied) || sess.denied[name] {
			return err
		}
		if relogged {
			sess.denied[name] = true // denied right after a fresh login: the ACL, not expiry
			return err
		}
		relogged = true
		if reloginErr = login(); reloginErr != nil {
			return reloginErr
		}
		err = ubusCall(ctx, client, ubusURL, sess.sid, object, method, params, out)
		if errors.Is(err, errAccessDenied) {
			sess.denied[name] = true
		} else if err == nil {
			delete(sess.denied, name)
		}
		return err
	}

	var board struct {
		Hostname string `json:"hostname"`
		Model    string `json:"model"`
		Release  struct {
			Description string `json:"description"`
			Version     string `json:"version"`
		} `json:"release"`
	}
	_ = call("system", "board", map[string]any{}, &board)

	var sysInfo struct {
		Uptime uint64    `json:"uptime"`
		Load   []float64 `json:"load"`
		Memory struct {
			Total uint64 `json:"total"`
			Free  uint64 `json:"free"`
		} `json:"memory"`
	}
	_ = call("system", "info", map[string]any{}, &sysInfo)

	devStatus := map[string]ubusNetDev{}
	if err := call("network.device", "status", map[string]any{}, &devStatus); err != nil {
		return nil, fmt.Errorf("openwrt network.device.status: %w", err)
	}

	var ifDump struct {
		Interface []ubusInterface `json:"interface"`
	}
	_ = call("network.interface", "dump", map[string]any{}, &ifDump)

	wirelessStatus := map[string]ubusRadio{}
	_ = call("network.wireless", "status", map[string]any{}, &wirelessStatus)

	hostHints := map[string]struct {
		IPAddrs  []string `json:"ipaddrs"`
		IP6Addrs []string `json:"ip6addrs"`
		Name     string   `json:"name"`
	}{}
	_ = call("luci-rpc", "getHostHints", map[string]any{}, &hostHints)

	now := time.Now().UTC()
	devNames := slices.SortedFunc(maps.Keys(devStatus), comparePortNames)

	primaryMAC := ""
	macs := []string{}
	seenMAC := map[string]bool{}
	for _, k := range devNames {
		nd := devStatus[k]
		if k == "lo" || nd.MacAddr == "" || nd.MacAddr == "00:00:00:00:00:00" {
			continue
		}
		if m, err := model.NormalizeMAC(nd.MacAddr); err == nil && !seenMAC[m] {
			seenMAC[m] = true
			if primaryMAC == "" {
				primaryMAC = m
			}
			macs = append(macs, m)
		}
	}

	// Keyed by target so added interfaces never change identity; MACs stay join metadata.
	devID := "openwrt:" + cfg.ID
	name := cfg.Name
	if name == "" {
		name = board.Hostname
	}
	if name == "" {
		name = cfg.ID
	}

	role := model.RoleRouter
	if len(wirelessStatus) > 0 && len(ifDump.Interface) <= 2 {
		role = model.RoleAP
	} else if len(devNames) >= 6 && len(wirelessStatus) == 0 {
		role = model.RoleSwitch
	}

	caps := []model.Capability{model.CapLinks, model.CapSegments, model.CapFirmwareInfo}
	if len(wirelessStatus) > 0 {
		caps = append(caps, model.CapWireless)
	}
	if len(hostHints) > 0 {
		caps = append(caps, model.CapNeighbors)
	}

	var load1 float64
	if len(sysInfo.Load) > 0 {
		load1 = sysInfo.Load[0] / 65536.0
	}
	var memUsed uint64
	if sysInfo.Memory.Total >= sysInfo.Memory.Free {
		memUsed = sysInfo.Memory.Total - sysInfo.Memory.Free
	}

	mgmtIP := ""
	snap := &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
	}

	// Links
	idx := 0
	for _, ifName := range devNames {
		if ifName == "lo" {
			continue
		}
		nd := devStatus[ifName]
		if nd.Type == "bridge" {
			continue
		}
		idx++
		speedMbps, duplex := parseOpenWrtSpeed(nd.Speed)
		media := guessMedia(ifName, speedMbps)
		mtu := nd.MTU
		oper := model.OperDown
		if nd.Up && nd.Carrier {
			oper = model.OperUp
		} else if nd.Up && !nd.Carrier {
			oper = model.OperNoCarrier
		}
		var fc *model.FlowControlState
		if f := nd.FlowControl; f != nil {
			// netifd reports the pause result as "negotiated" with pause autoneg on, "selected" without.
			active := f.Selected
			if f.Autoneg != nil && *f.Autoneg {
				active = f.Negotiated
			}
			fc = &model.FlowControlState{
				Rx:      slices.Contains(active, "rx"),
				Tx:      slices.Contains(active, "tx"),
				Autoneg: f.Autoneg,
			}
		}
		snap.Links = append(snap.Links, model.Link{
			DeviceID:    devID,
			Index:       idx,
			IfName:      ifName,
			MAC:         nd.MacAddr,
			AdminUp:     nd.Up,
			LinkUp:      nd.Up && nd.Carrier,
			OperState:   oper,
			SpeedMbps:   speedMbps,
			Duplex:      duplex,
			Autoneg:     nd.Autoneg,
			MTU:         &mtu,
			Media:       media,
			FlowControl: fc,
			Counters: &model.PortCounters{
				RxBytes:   nd.Statistics.RxBytes,
				TxBytes:   nd.Statistics.TxBytes,
				RxPackets: nd.Statistics.RxPackets,
				TxPackets: nd.Statistics.TxPackets,
				RxErrors:  nd.Statistics.RxErrors,
				TxErrors:  nd.Statistics.TxErrors,
				RxDropped: nd.Statistics.RxDropped,
				TxDropped: nd.Statistics.TxDropped,
			},
		})
	}

	// Segments
	for _, iface := range ifDump.Interface {
		if iface.Interface == "loopback" {
			continue
		}
		addrs := []model.IPAddress{}
		for _, a := range iface.IPv4Address {
			if mgmtIP == "" && iface.Interface == "lan" {
				mgmtIP = a.Address
			}
			addrs = append(addrs, model.IPAddress{
				Family:       model.IPv4,
				Address:      a.Address,
				PrefixLength: a.Mask,
				Source:       iface.Proto,
			})
		}
		v6 := iface.IPv6Address
		// Delegated-prefix LAN addresses are reported only under ipv6-prefix-assignment.
		for _, pa := range iface.IPv6PrefixAssignment {
			if la := pa.LocalAddress; la.Address != "" && !slices.Contains(v6, la) {
				v6 = append(v6, la)
			}
		}
		for _, a := range v6 {
			addrs = append(addrs, model.IPAddress{
				Family:       model.IPv6,
				Address:      a.Address,
				PrefixLength: a.Mask,
				Source:       iface.Proto,
			})
		}
		oper := model.OperDown
		if iface.Up {
			oper = model.OperUp
		}
		snap.Segments = append(snap.Segments, model.Segment{
			DeviceID:   devID,
			ID:         iface.Interface,
			Name:       iface.Interface,
			Kind:       iface.Proto,
			ParentLink: iface.L3Device,
			AdminUp:    iface.Up,
			OperState:  oper,
			Addresses:  addrs,
		})
	}

	// Wireless radios
	for _, rName := range slices.Sorted(maps.Keys(wirelessStatus)) {
		r := wirelessStatus[rName]
		ssids := []model.Ssid{}
		for _, vif := range r.Interfaces {
			ssids = append(ssids, model.Ssid{
				Name:     vif.Config.SSID,
				Enabled:  r.Up,
				Security: vif.Config.Encryption,
			})
		}
		snap.Radios = append(snap.Radios, model.WirelessRadio{
			DeviceID: devID,
			Name:     rName,
			Band:     r.Config.Band,
			SSIDs:    ssids,
		})
	}

	// Neighbors from host hints
	for rawMAC, hint := range hostHints {
		for _, ip := range hint.IPAddrs {
			snap.Neighbors = append(snap.Neighbors, model.NeighborEntry{
				DeviceID: devID, Family: model.IPv4, IP: ip, MAC: rawMAC, State: "reachable",
			})
		}
		for _, ip := range hint.IP6Addrs {
			snap.Neighbors = append(snap.Neighbors, model.NeighborEntry{
				DeviceID: devID, Family: model.IPv6, IP: ip, MAC: rawMAC, State: "reachable",
			})
		}
	}

	fw := board.Release.Description
	if fw == "" {
		fw = board.Release.Version
	}
	snap.Devices = []model.Device{{
		ID:           devID,
		Driver:       "openwrt",
		TargetID:     cfg.ID,
		Name:         name,
		Hostname:     board.Hostname,
		Vendor:       "OpenWrt",
		Model:        board.Model,
		Role:         role,
		PrimaryMAC:   primaryMAC,
		MACs:         macs,
		MgmtIP:       mgmtIP,
		Firmware:     model.FirmwareInfo{Current: fw},
		UptimeSec:    sysInfo.Uptime,
		Online:       true,
		LastSeen:     now,
		Capabilities: caps,
		SystemStats: model.SystemStats{
			MemUsedBytes:  memUsed,
			MemTotalBytes: sysInfo.Memory.Total,
			Load1:         load1,
		},
	}}

	return snap, nil
}

// errAccessDenied is uhttpd refusing a call: an expired or unknown session, or an ACL refusal.
var errAccessDenied = errors.New("ubus access denied")

func ubusCall(ctx context.Context, client *http.Client, url, sid, object, method string, params map[string]any, out any) error {
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "call",
		"params":  []any{sid, object, method, params},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var rpcResp struct {
		Result []json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		if rpcResp.Error.Code == -32002 { // uhttpd: expired or unknown session, or an ACL refusal
			return fmt.Errorf("ubus error %d: %s: %w", rpcResp.Error.Code, rpcResp.Error.Message, errAccessDenied)
		}
		return fmt.Errorf("ubus error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result) < 1 {
		return fmt.Errorf("empty ubus result")
	}
	var code int
	if err := json.Unmarshal(rpcResp.Result[0], &code); err != nil || code != 0 {
		return fmt.Errorf("ubus call %s.%s returned status %d", object, method, code)
	}
	if out != nil && len(rpcResp.Result) >= 2 {
		return json.Unmarshal(rpcResp.Result[1], out)
	}
	return nil
}

// comparePortNames orders names by prefix, then numeric suffix, so lan2 sorts before lan10.
func comparePortNames(a, b string) int {
	ap, an := splitPortNumber(a)
	bp, bn := splitPortNumber(b)
	if c := strings.Compare(ap, bp); c != 0 {
		return c
	}
	if c := cmp.Compare(an, bn); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// splitPortNumber splits a trailing decimal number off name; n is -1 when there is none.
func splitPortNumber(name string) (prefix string, n int) {
	prefix = strings.TrimRight(name, "0123456789")
	n, err := strconv.Atoi(name[len(prefix):])
	if err != nil {
		return name, -1
	}
	return prefix, n
}

func parseOpenWrtSpeed(raw string) (int, string) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return 0, "unknown"
	}
	duplex := "unknown"
	if strings.HasSuffix(s, "F") {
		duplex = "full"
		s = strings.TrimSuffix(s, "F")
	} else if strings.HasSuffix(s, "H") {
		duplex = "half"
		s = strings.TrimSuffix(s, "H")
	}
	var speed int
	_, _ = fmt.Sscanf(s, "%d", &speed)
	return speed, duplex
}

func guessMedia(ifName string, speedMbps int) model.MediaType {
	lower := strings.ToLower(ifName)
	if strings.Contains(lower, "sfp") {
		if speedMbps >= 10000 {
			return model.MediaSFPPl
		}
		return model.MediaSFP
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
	case speedMbps >= 100:
		return model.MediaFE
	default:
		return model.MediaOther
	}
}

type ubusNetDev struct {
	Type        string `json:"type"`
	Up          bool   `json:"up"`
	Carrier     bool   `json:"carrier"`
	MTU         int    `json:"mtu"`
	MacAddr     string `json:"macaddr"`
	Speed       string `json:"speed"`
	Autoneg     *bool  `json:"autoneg"`
	FlowControl *struct {
		Autoneg    *bool    `json:"autoneg"`
		Selected   []string `json:"selected"`
		Negotiated []string `json:"negotiated"`
	} `json:"flow-control"`
	Statistics struct {
		RxBytes   uint64 `json:"rx_bytes"`
		TxBytes   uint64 `json:"tx_bytes"`
		RxPackets uint64 `json:"rx_packets"`
		TxPackets uint64 `json:"tx_packets"`
		RxErrors  uint64 `json:"rx_errors"`
		TxErrors  uint64 `json:"tx_errors"`
		RxDropped uint64 `json:"rx_dropped"`
		TxDropped uint64 `json:"tx_dropped"`
	} `json:"statistics"`
}

type ubusInterface struct {
	Interface   string `json:"interface"`
	Up          bool   `json:"up"`
	Proto       string `json:"proto"`
	L3Device    string `json:"l3_device"`
	IPv4Address []struct {
		Address string `json:"address"`
		Mask    int    `json:"mask"`
	} `json:"ipv4-address"`
	IPv6Address          []ubusAddress `json:"ipv6-address"`
	IPv6PrefixAssignment []struct {
		LocalAddress ubusAddress `json:"local-address"`
	} `json:"ipv6-prefix-assignment"`
}

type ubusAddress struct {
	Address string `json:"address"`
	Mask    int    `json:"mask"`
}

type ubusRadio struct {
	Up     bool `json:"up"`
	Config struct {
		Band string `json:"band"`
	} `json:"config"`
	Interfaces []struct {
		Config struct {
			SSID       string `json:"ssid"`
			Encryption string `json:"encryption"`
		} `json:"config"`
	} `json:"interfaces"`
}
