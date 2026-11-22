// SPDX-License-Identifier: AGPL-3.0-or-later
package pfsense

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

// Driver polls a pfSense firewall over its REST API (v2) using a scoped least-privilege API key.
type Driver struct{}

// New returns a new pfSense REST API driver.
func New() *Driver { return &Driver{} }

// Kind returns "pfsense".
func (d *Driver) Kind() string { return "pfsense" }

// ValidateConfig checks required pfSense target configuration.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("pfsense target %q: endpoint is required", cfg.ID)
	}
	if cfg.ResolvedSecrets["api_key"] == "" {
		return fmt.Errorf("pfsense target %q: api_key secret is required", cfg.ID)
	}
	return nil
}

// Poll reads system status, interfaces, gateways, DHCP leases, ARP, services, and WireGuard peers.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(cfg.Endpoint, "/")
	client := driver.NewHTTPClient(cfg, 15*time.Second)
	defer client.CloseIdleConnections()
	apiKey := cfg.ResolvedSecrets["api_key"]

	var sysResp struct {
		Data struct {
			Hostname    string  `json:"hostname"`
			Version     string  `json:"version"`
			UptimeSec   uint64  `json:"uptime_sec"`
			CPUUsage    float64 `json:"cpu_usage"`
			MemUsagePct float64 `json:"mem_usage"`
			TempC       float64 `json:"temp_c"`
			StatesUsed  int64   `json:"states_used"`
			StatesMax   int64   `json:"states_max"`
			LoadAvg1    float64 `json:"load_avg_1"`
		} `json:"data"`
	}
	if err := getJSON(ctx, client, baseURL+"/api/v2/status/system", apiKey, &sysResp); err != nil {
		return nil, fmt.Errorf("pfsense status/system: %w", err)
	}

	var ifResp struct {
		Data []struct {
			Name     string `json:"name"`
			Descr    string `json:"descr"`
			HwIf     string `json:"hwif"`
			Status   string `json:"status"`
			MAC      string `json:"macaddr"`
			MTU      int    `json:"mtu"`
			IPAddr   string `json:"ipaddr"`
			Subnet   int    `json:"subnet"`
			IPAddrV6 string `json:"ipaddrv6"`
			SubnetV6 int    `json:"subnetv6"`
			InBytes  uint64 `json:"inbytes"`
			OutBytes uint64 `json:"outbytes"`
			InPkts   uint64 `json:"inpkts"`
			OutPkts  uint64 `json:"outpkts"`
			InErrs   uint64 `json:"inerrs"`
			OutErrs  uint64 `json:"outerrs"`
		} `json:"data"`
	}
	if err := getJSON(ctx, client, baseURL+"/api/v2/status/interfaces", apiKey, &ifResp); err != nil {
		return nil, fmt.Errorf("pfsense status/interfaces: %w", err)
	}

	var gwResp struct {
		Data []struct {
			Name      string  `json:"name"`
			SrcIP     string  `json:"srcip"`
			MonitorIP string  `json:"monitorip"`
			Status    string  `json:"status"`
			DelayMs   float64 `json:"delay"`
			LossPct   float64 `json:"loss"`
			Default   bool    `json:"default"`
		} `json:"data"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/v2/status/gateways", apiKey, &gwResp)

	var arpResp struct {
		Data []struct {
			IP        string `json:"ip"`
			MAC       string `json:"mac"`
			Interface string `json:"interface"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/v2/diagnostics/arp_table", apiKey, &arpResp)

	var dhcpResp struct {
		Data []struct {
			IP       string `json:"ip"`
			MAC      string `json:"mac"`
			Hostname string `json:"hostname"`
			Type     string `json:"type"`
		} `json:"data"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/v2/status/dhcp_server/leases", apiKey, &dhcpResp)

	var svcResp struct {
		Data []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Status      bool   `json:"status"`
		} `json:"data"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/v2/status/services", apiKey, &svcResp)

	var wgResp struct {
		Data []struct {
			Tun        string `json:"tun"`
			Descr      string `json:"descr"`
			PublicKey  string `json:"public_key"`
			Endpoint   string `json:"endpoint"`
			LatestHS   int64  `json:"latest_handshake"`
			TransferRx uint64 `json:"transfer_rx"`
			TransferTx uint64 `json:"transfer_tx"`
		} `json:"data"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/v2/status/wireguard/peers", apiKey, &wgResp)

	now := time.Now().UTC()
	primaryMAC := ""
	macs := []string{}
	seenMAC := map[string]bool{}
	mgmtIP := ""
	for _, iface := range ifResp.Data {
		if m, err := model.NormalizeMAC(iface.MAC); err == nil && !seenMAC[m] {
			seenMAC[m] = true
			if primaryMAC == "" {
				primaryMAC = m
			}
			macs = append(macs, m)
		}
		if mgmtIP == "" && strings.EqualFold(iface.Name, "lan") && iface.IPAddr != "" {
			mgmtIP = iface.IPAddr
		}
	}

	devID := "pfsense:" + cfg.ID
	if primaryMAC != "" {
		devID = "pfsense:" + primaryMAC
	}
	name := cfg.Name
	if name == "" {
		name = sysResp.Data.Hostname
	}
	if name == "" {
		name = cfg.ID
	}

	caps := []model.Capability{
		model.CapLinks,
		model.CapSegments,
		model.CapGateway,
		model.CapFirewall,
		model.CapNeighbors,
		model.CapDHCP,
		model.CapFirmwareInfo,
	}
	var sensors []model.SensorReading
	if sysResp.Data.TempC > 0 {
		sensors = append(sensors, model.SensorReading{
			Name: "cpu", Kind: model.SensorTempC, Value: sysResp.Data.TempC, Unit: "C", Status: model.AlertOK,
		})
		caps = append(caps, model.CapSensors)
	}
	if len(wgResp.Data) > 0 {
		caps = append(caps, model.CapVPN)
	}

	snap := &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
		Devices: []model.Device{{
			ID:           devID,
			Driver:       "pfsense",
			TargetID:     cfg.ID,
			Name:         name,
			Hostname:     sysResp.Data.Hostname,
			Vendor:       "Netgate",
			Model:        "pfSense",
			Role:         model.RoleFirewall,
			PrimaryMAC:   primaryMAC,
			MACs:         macs,
			MgmtIP:       mgmtIP,
			Firmware:     model.FirmwareInfo{Current: sysResp.Data.Version},
			UptimeSec:    sysResp.Data.UptimeSec,
			Online:       true,
			LastSeen:     now,
			Capabilities: caps,
			SystemStats: model.SystemStats{
				CPUPct: sysResp.Data.CPUUsage,
				Load1:  sysResp.Data.LoadAvg1,
			},
			Sensors: sensors,
		}},
	}

	for idx, iface := range ifResp.Data {
		up := strings.EqualFold(iface.Status, "up")
		oper := model.OperDown
		if up {
			oper = model.OperUp
		}
		mtu := iface.MTU
		ifName := iface.HwIf
		if ifName == "" {
			ifName = iface.Name
		}
		snap.Links = append(snap.Links, model.Link{
			DeviceID:  devID,
			Index:     idx + 1,
			IfName:    ifName,
			Label:     iface.Descr,
			MAC:       iface.MAC,
			AdminUp:   up,
			LinkUp:    up,
			OperState: oper,
			MTU:       &mtu,
			Media:     model.MediaOther,
			Counters: &model.PortCounters{
				RxBytes:   iface.InBytes,
				TxBytes:   iface.OutBytes,
				RxPackets: iface.InPkts,
				TxPackets: iface.OutPkts,
				RxErrors:  iface.InErrs,
				TxErrors:  iface.OutErrs,
			},
		})

		addrs := []model.IPAddress{}
		if iface.IPAddr != "" {
			addrs = append(addrs, model.IPAddress{Family: model.IPv4, Address: iface.IPAddr, PrefixLength: iface.Subnet})
		}
		if iface.IPAddrV6 != "" {
			addrs = append(addrs, model.IPAddress{Family: model.IPv6, Address: iface.IPAddrV6, PrefixLength: iface.SubnetV6})
		}
		snap.Segments = append(snap.Segments, model.Segment{
			DeviceID:   devID,
			ID:         iface.Name,
			Name:       iface.Descr,
			Kind:       "ethernet",
			ParentLink: ifName,
			MTU:        &mtu,
			AdminUp:    up,
			OperState:  oper,
			Addresses:  addrs,
		})
	}

	for _, gw := range gwResp.Data {
		rtt := gw.DelayMs
		loss := gw.LossPct
		online := strings.EqualFold(gw.Status, "online") || strings.EqualFold(gw.Status, "none")
		snap.Gateways = append(snap.Gateways, model.GatewayStatus{
			DeviceID:  devID,
			Name:      gw.Name,
			Family:    model.IPv4,
			GatewayIP: gw.SrcIP,
			MonitorIP: gw.MonitorIP,
			Online:    online,
			RTTMs:     &rtt,
			LossPct:   &loss,
			IsDefault: gw.Default,
		})
	}

	for _, a := range arpResp.Data {
		normMAC, err := model.NormalizeMAC(a.MAC)
		if err != nil {
			continue
		}
		snap.Neighbors = append(snap.Neighbors, model.NeighborEntry{
			DeviceID:    devID,
			Family:      model.IPv4,
			IP:          a.IP,
			MAC:         normMAC,
			SegmentName: a.Interface,
			State:       strings.ToLower(a.Status),
		})
	}

	for _, l := range dhcpResp.Data {
		normMAC, err := model.NormalizeMAC(l.MAC)
		if err != nil {
			continue
		}
		snap.DHCP = append(snap.DHCP, model.DhcpLease{
			DeviceID: devID,
			Family:   model.IPv4,
			IP:       l.IP,
			MAC:      normMAC,
			Hostname: l.Hostname,
			Static:   strings.EqualFold(l.Type, "static"),
		})
	}

	services := make([]model.ServiceStatus, 0, len(svcResp.Data))
	for _, sv := range svcResp.Data {
		st := "stopped"
		if sv.Status {
			st = "running"
		}
		services = append(services, model.ServiceStatus{Name: sv.Name, State: st, Summary: sv.Description})
	}
	used := sysResp.Data.StatesUsed
	maxSt := sysResp.Data.StatesMax
	snap.Firewalls = append(snap.Firewalls, model.FirewallState{
		DeviceID:   devID,
		Engine:     "pf",
		StatesUsed: &used,
		StatesMax:  &maxSt,
		Services:   services,
	})

	for idx, p := range wgResp.Data {
		var hs *time.Time
		up := false
		if p.LatestHS > 0 {
			t := time.Unix(p.LatestHS, 0).UTC()
			hs = &t
			up = now.Sub(t) < 3*time.Minute
		}
		name := p.Descr
		if name == "" {
			name = fmt.Sprintf("%s-peer-%d", p.Tun, idx)
		}
		snap.VPNs = append(snap.VPNs, model.VpnTunnel{
			DeviceID:      devID,
			ID:            fmt.Sprintf("%s:%s", p.Tun, p.PublicKey),
			Name:          name,
			Kind:          "wireguard",
			Peer:          p.PublicKey,
			Endpoint:      p.Endpoint,
			Up:            up,
			LastHandshake: hs,
			RxBytes:       p.TransferRx,
			TxBytes:       p.TransferTx,
		})
	}

	return snap, nil
}

func getJSON(ctx context.Context, client *http.Client, url, apiKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
