// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rtlplayground polls switches running the RTLPlayground firmware (RTL837x/RTL836x
// on an 8051 MCU). The device is weak: its web server holds one TCP connection (shared with
// the web UI), never keeps connections alive, builds each JSON reply in a 2.5 kB buffer, and
// keeps at most 4 login sessions, evicting the least recently used. Its Prometheus exporter
// has its own port and connection slot, shared with any Prometheus scraper.
//
// So the driver:
//   - reads link state, speed, counters, SFP DOM, temperatures and identity from the public
//     /metrics exporter, once per poll;
//   - only with a password, logs in once, keeps that one session, and refreshes LLDP, LAG,
//     system information and the forwarding table on slow schedules, replaying the cached
//     results in every snapshot;
//   - sends one request at a time with a pause between requests;
//   - never calls the endpoints that change state (/l2_del.json, /reset, /cmd, /config, uploads).
package rtlplayground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

const (
	defaultMetricsPort = "9100"
	requestTimeout     = 15 * time.Second
	minPollInterval    = 30 * time.Second
	// Refresh periods of the data behind the login; each is cheap for the device but shares
	// the web UI's only connection slot.
	lldpEvery = 5 * time.Minute
	infoEvery = 10 * time.Minute
	fdbEvery  = 15 * time.Minute
	// A full forwarding table is 4096 entries at about 29 per page.
	maxFdbPages = 160
)

// requestGap spaces consecutive requests so the device and its web UI get the slot back.
var requestGap = 150 * time.Millisecond

// Driver polls RTLPlayground switches. It keeps per-target session and cache state.
type Driver struct {
	mu      sync.Mutex
	targets map[string]*target
}

// target is the state kept between polls of one switch. The poller never polls one target
// concurrently, so its fields need no lock of their own.
type target struct {
	session string // "session" cookie value

	lldpAt, infoAt, fdbAt time.Time
	lldp                  []lldpNeighbor
	lag                   *lagReply
	info                  *infoReply
	fdb                   []l2Entry
}

// expire drops cached slow data that has failed to refresh for three periods (or whose
// password was removed), so a broken login cannot keep stale neighbours alive forever.
func (t *target) expire(now time.Time) {
	if now.Sub(t.lldpAt) > 3*lldpEvery {
		t.lldp, t.lag = nil, nil
	}
	if now.Sub(t.infoAt) > 3*infoEvery {
		t.info = nil
	}
	if now.Sub(t.fdbAt) > 3*fdbEvery {
		t.fdb, t.fdbAt = nil, time.Time{}
	}
}

// New returns a new RTLPlayground switch driver.
func New() *Driver { return &Driver{targets: map[string]*target{}} }

// Kind returns "rtlplayground".
func (d *Driver) Kind() string { return "rtlplayground" }

// MinPollInterval keeps polls of this weak device at least 30 seconds apart.
func (d *Driver) MinPollInterval() time.Duration { return minPollInterval }

// ValidateConfig checks required RTLPlayground target settings.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("rtlplayground target %q: endpoint is required", cfg.ID)
	}
	if _, err := metricsURL(cfg); err != nil {
		return fmt.Errorf("rtlplayground target %q: %w", cfg.ID, err)
	}
	if cfg.ResolvedSecrets["password"] != "" {
		// The firmware has no TLS, so a password always needs allow_insecure_http.
		return driver.CheckCredentialTransport(cfg)
	}
	return nil
}

// metricsURL is the metrics_url setting, else the endpoint's host on the exporter port.
func metricsURL(cfg driver.TargetConfig) (string, error) {
	if u := cfg.StringSetting("metrics_url", ""); u != "" {
		return u, nil
	}
	u, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid endpoint %q", cfg.Endpoint)
	}
	return "http://" + net.JoinHostPort(u.Hostname(), defaultMetricsPort) + "/metrics", nil
}

func (d *Driver) state(id string) *target {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.targets[id]
	if t == nil {
		t = &target{}
		d.targets[id] = t
	}
	return t
}

// Poll scrapes /metrics and, when a password is configured, refreshes the slow JSON data.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	mURL, _ := metricsURL(cfg)
	c := newClient(cfg)
	defer c.http.CloseIdleConnections()

	samples, err := c.scrape(ctx, mURL)
	if err != nil {
		return nil, fmt.Errorf("rtlplayground metrics (is `prometheus on` set on the switch?): %w", err)
	}

	st := d.state(cfg.ID)
	if c.password != "" {
		// Slow data is best effort: a failure keeps the previous cache and the poll goes on.
		now := time.Now()
		refresh := func(at *time.Time, every time.Duration, fetch func() error) {
			if now.Sub(*at) < every {
				return
			}
			if err := fetch(); err == nil {
				*at = now
			}
		}
		refresh(&st.lldpAt, lldpEvery, func() error { return c.fetchLLDPAndLAG(ctx, st) })
		refresh(&st.infoAt, infoEvery, func() error { return c.fetchInfo(ctx, st) })
		refresh(&st.fdbAt, fdbEvery, func() error { return c.fetchFDB(ctx, st) })
	}
	st.expire(time.Now())
	return buildSnapshot(cfg, samples, st), nil
}

// client sends one request at a time to one switch.
type client struct {
	http     *http.Client
	base     string
	password string
	sent     bool
}

func newClient(cfg driver.TargetConfig) *client {
	hc := driver.NewHTTPClient(cfg, requestTimeout)
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.DisableKeepAlives = true // the firmware closes after every reply anyway
		tr.MaxConnsPerHost = 1
	}
	// /login answers with a 302 whose cookie is the result; never follow it.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{http: hc, base: strings.TrimRight(cfg.Endpoint, "/"), password: cfg.ResolvedSecrets["password"]}
}

var errUnauthorized = errors.New("unauthorized")

// do sends req after the pacing gap and returns the body, limited to limit bytes.
func (c *client) do(ctx context.Context, req *http.Request, limit int64) (*http.Response, []byte, error) {
	if c.sent {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(requestGap):
		}
	}
	c.sent = true
	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return resp, body, err
}

func (c *client) scrape(ctx context.Context, u string) ([]sample, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, body, err := c.do(ctx, req, 256<<10)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return parsePromText(strings.NewReader(string(body)))
}

// login takes a session, reusing the current cookie's slot when it is still valid so the
// driver never holds more than one of the firmware's 4 sessions.
func (c *client) login(ctx context.Context, st *target) error {
	form := url.Values{"pwd": {c.password}}.Encode()
	req, err := http.NewRequest(http.MethodPost, c.base+"/login", strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if st.session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: st.session})
	}
	resp, _, err := c.do(ctx, req, 4<<10)
	if err != nil {
		return err
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "session" && ck.Value != "" {
			st.session = ck.Value
			return nil
		}
	}
	// A failed login redirects to login.html without a cookie, whatever the status code.
	st.session = ""
	return errors.New("rtlplayground login refused (wrong password?)")
}

// getJSON fetches an authenticated JSON endpoint, logging in first or again after a 401.
func (c *client) getJSON(ctx context.Context, st *target, path string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if st.session == "" {
			if err := c.login(ctx, st); err != nil {
				return err
			}
		}
		req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
		if err != nil {
			return err
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: st.session})
		resp, body, err := c.do(ctx, req, 16<<10)
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case http.StatusOK:
			return json.Unmarshal(body, out)
		case http.StatusUnauthorized:
			st.session = "" // expired or evicted: log in again, once
			continue
		default:
			return fmt.Errorf("rtlplayground %s: HTTP %d", path, resp.StatusCode)
		}
	}
	return fmt.Errorf("rtlplayground %s: %w", path, errUnauthorized)
}

type lldpNeighbor struct {
	Port     int    `json:"port"`
	Chassis  string `json:"chassis"`
	PortID   string `json:"portId"`
	SysName  string `json:"sysName"`
	PortDesc string `json:"portDesc"`
	MgmtAddr string `json:"mgmtAddr"`
}

type lagReply struct {
	Lags []struct {
		LagNum        int    `json:"lagNum"`
		Type          string `json:"type"`
		Members       []int  `json:"members"`
		ActiveMembers []int  `json:"active_members"`
	} `json:"lags"`
}

type infoReply struct {
	IPAddress  string `json:"ip_address"`
	MACAddress string `json:"mac_address"`
	Hostname   string `json:"hostname"`
	SwVer      string `json:"sw_ver"`
	HwVer      string `json:"hw_ver"`
	Uptime     string `json:"uptime"` // hex seconds, "0x0001a2b3"
}

type l2Entry struct {
	VLAN string `json:"vlan"` // hex
	MAC  string `json:"mac"`
	Type string `json:"type"` // "l" learned, "s" static
	Port int    `json:"port"` // physical port, 0 = CPU
	Idx  string `json:"idx"`  // hex table index
}

func (c *client) fetchLLDPAndLAG(ctx context.Context, st *target) error {
	var l struct {
		Neighbors []lldpNeighbor `json:"neighbors"`
	}
	if err := c.getJSON(ctx, st, "/lldp.json", &l); err != nil {
		return err
	}
	var lag lagReply
	if err := c.getJSON(ctx, st, "/lag.json", &lag); err != nil {
		return err
	}
	st.lldp, st.lag = l.Neighbors, &lag
	return nil
}

func (c *client) fetchInfo(ctx context.Context, st *target) error {
	var info infoReply
	if err := c.getJSON(ctx, st, "/information.json", &info); err != nil {
		return err
	}
	st.info = &info
	return nil
}

// fetchFDB walks the forwarding table the way the web UI does: each page starts after the
// last index returned, and the walk ends on an empty page or when an index repeats (the
// firmware's hash walk wraps around). A partial walk is discarded.
func (c *client) fetchFDB(ctx context.Context, st *target) error {
	seen := map[string]bool{}
	var all []l2Entry
	idx := 0
	for page := 0; page < maxFdbPages; page++ {
		var rows []l2Entry
		if err := c.getJSON(ctx, st, "/l2.json?idx="+strconv.Itoa(idx), &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		wrapped := false
		for _, r := range rows {
			if seen[r.Idx] {
				wrapped = true
				break
			}
			seen[r.Idx] = true
			all = append(all, r)
		}
		last, err := strconv.ParseUint(rows[len(rows)-1].Idx, 16, 16)
		if wrapped || err != nil || int(last)+1 <= idx {
			break
		}
		idx = int(last) + 1
	}
	st.fdb = all
	return nil
}

func ifName(port int) string { return "lan" + strconv.Itoa(port) }

// portOf parses the exporter's device="lanN" label.
func portOf(dev string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(dev, "lan"))
	return n, err == nil && strings.HasPrefix(dev, "lan") && n > 0
}

func buildSnapshot(cfg driver.TargetConfig, samples []sample, st *target) *driver.Snapshot {
	now := time.Now().UTC()
	devID := "rtlplayground:" + cfg.ID

	type port struct {
		seen, carrier, adminDown bool
		alias                    string
		speedMbps                int
		counters                 model.PortCounters
		sfp                      *model.SfpDom
	}
	ports := map[int]*port{}
	get := func(dev string) *port {
		n, ok := portOf(dev)
		if !ok {
			return nil
		}
		if ports[n] == nil {
			ports[n] = &port{}
		}
		return ports[n]
	}
	sfp := func(p *port) *model.SfpDom {
		if p.sfp == nil {
			p.sfp = &model.SfpDom{Present: true}
		}
		return p.sfp
	}

	var hostname, release, machine, mac string
	var bootTime, nodeTime float64
	var socTemp *float64
	counter := map[string]func(*model.PortCounters, uint64){
		"node_network_receive_bytes_total":    func(c *model.PortCounters, v uint64) { c.RxBytes = v },
		"node_network_transmit_bytes_total":   func(c *model.PortCounters, v uint64) { c.TxBytes = v },
		"node_network_receive_packets_total":  func(c *model.PortCounters, v uint64) { c.RxPackets = v },
		"node_network_transmit_packets_total": func(c *model.PortCounters, v uint64) { c.TxPackets = v },
		"node_network_receive_errs_total":     func(c *model.PortCounters, v uint64) { c.RxErrors = v },
		"node_network_transmit_errs_total":    func(c *model.PortCounters, v uint64) { c.TxErrors = v },
		"node_network_receive_drop_total":     func(c *model.PortCounters, v uint64) { c.RxDropped = v },
		"node_network_transmit_drop_total":    func(c *model.PortCounters, v uint64) { c.TxDropped = v },
		"node_network_receive_frame_total":    func(c *model.PortCounters, v uint64) { c.CrcErrors = v },
	}
	for _, s := range samples {
		dev := s.labels["device"]
		switch s.name {
		case "node_uname_info":
			hostname, release, machine = s.labels["nodename"], s.labels["release"], s.labels["machine"]
		case "node_boot_time_seconds":
			bootTime = s.value
		case "node_time_seconds":
			nodeTime = s.value
		case "node_hwmon_temp_celsius":
			chip := s.labels["chip"]
			if chip == "thermal_thermal_zone0" {
				v := s.value
				socTemp = &v
			} else if p := get(strings.TrimPrefix(chip, "sfp_")); p != nil && strings.HasPrefix(chip, "sfp_") {
				v := s.value
				sfp(p).TempC = &v
			}
		case "node_network_info":
			if p := get(dev); p != nil {
				p.seen = true
				p.alias = s.labels["ifalias"]
				p.adminDown = s.labels["operstate"] == "down"
				if mac == "" {
					mac = s.labels["address"]
				}
			}
		case "node_network_carrier":
			if p := get(dev); p != nil {
				p.seen = true
				p.carrier = s.value == 1
			}
		case "node_network_speed_bytes":
			if p := get(dev); p != nil {
				p.speedMbps = int(s.value * 8 / 1e6)
			}
		case "rtlplayground_sfp_info":
			if p := get(dev); p != nil {
				d := sfp(p)
				d.Vendor, d.PartNumber, d.Serial = s.labels["vendor"], s.labels["model"], s.labels["serial"]
			}
		case "rtlplayground_sfp_voltage_volts":
			if p := get(dev); p != nil {
				v := s.value
				sfp(p).VoltageV = &v
			}
		case "rtlplayground_sfp_tx_bias_amperes":
			if p := get(dev); p != nil {
				v := s.value * 1000
				sfp(p).BiasMa = &v
			}
		case "rtlplayground_sfp_tx_power_watts":
			if p := get(dev); p != nil {
				sfp(p).TxPowerDbm = dbm(s.value)
			}
		case "rtlplayground_sfp_rx_power_watts":
			if p := get(dev); p != nil {
				sfp(p).RxPowerDbm = dbm(s.value)
			}
		default:
			// Counters are exact 64-bit integers; float64 would round them above 2^53.
			if set, ok := counter[s.name]; ok {
				if v, err := strconv.ParseUint(s.raw, 10, 64); err == nil {
					if p := get(dev); p != nil {
						set(&p.counters, v)
					}
				}
			}
		}
	}

	lagOf := map[int]string{}
	var lagGroups []model.LagGroup
	if st.lag != nil {
		for _, l := range st.lag.Lags {
			if len(l.Members) == 0 {
				continue
			}
			g := model.LagGroup{DeviceID: devID, ID: "lag" + strconv.Itoa(l.LagNum+1), Mode: l.Type}
			g.Name = g.ID
			for _, m := range l.Members {
				if m == 0 {
					continue // CPU port
				}
				lagOf[m] = g.ID
				g.MemberLinks = append(g.MemberLinks, ifName(m))
				if p := ports[m]; p != nil && p.carrier {
					g.LinkUp = true
					g.SpeedMbps += p.speedMbps
				}
			}
			lagGroups = append(lagGroups, g)
		}
	}

	nums := make([]int, 0, len(ports))
	for n, p := range ports {
		if p.seen {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	links := make([]model.Link, 0, len(nums))
	for _, n := range nums {
		p := ports[n]
		media := mediaFor(p.speedMbps, p.carrier, p.sfp != nil)
		oper := model.OperUp
		switch {
		case p.adminDown:
			oper = model.OperDisabled
		case !p.carrier:
			oper = model.OperNoCarrier
		}
		counters := p.counters
		l := model.Link{
			DeviceID:   devID,
			Index:      n,
			IfName:     ifName(n),
			Label:      p.alias,
			AdminUp:    !p.adminDown,
			LinkUp:     p.carrier,
			OperState:  oper,
			Media:      media,
			LagGroupID: lagOf[n],
			SFP:        p.sfp,
			Counters:   &counters,
		}
		if p.carrier {
			l.SpeedMbps = p.speedMbps
		}
		links = append(links, l)
	}

	caps := []model.Capability{model.CapLinks, model.CapFirmwareInfo}
	var sensors []model.SensorReading
	if socTemp != nil {
		caps = append(caps, model.CapSensors)
		sensors = append(sensors, model.SensorReading{Name: "soc", Kind: model.SensorTempC, Value: *socTemp, Unit: "C"})
	}
	for _, l := range links {
		if l.SFP != nil {
			caps = append(caps, model.CapSfpDom)
			break
		}
	}
	if st.lag != nil {
		caps = append(caps, model.CapLLDP, model.CapLAG)
	}
	if st.fdbAt != (time.Time{}) {
		caps = append(caps, model.CapFDB)
	}

	var uptime uint64
	if bootTime > 0 && nodeTime > bootTime {
		uptime = uint64(nodeTime - bootTime)
	}
	var mgmtIP string
	if info := st.info; info != nil {
		mgmtIP = info.IPAddress
		if hostname == "" {
			hostname = info.Hostname
		}
		if release == "" {
			release = info.SwVer
		}
		if machine == "" {
			machine = info.HwVer
		}
		if mac == "" {
			mac = info.MACAddress
		}
		if uptime == 0 { // no NTP sync: the exporter omits boot time
			if v, err := strconv.ParseUint(strings.TrimPrefix(info.Uptime, "0x"), 16, 64); err == nil {
				uptime = v
			}
		}
	}
	name := cfg.Name
	if name == "" {
		name = hostname
	}
	var macs []string
	if mac != "" {
		macs = []string{mac}
	}

	snap := &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
		Devices: []model.Device{{
			ID:           devID,
			Driver:       "rtlplayground",
			TargetID:     cfg.ID,
			Name:         name,
			Hostname:     hostname,
			Model:        machine,
			Role:         model.RoleSwitch,
			PrimaryMAC:   mac,
			MACs:         macs,
			MgmtIP:       mgmtIP,
			Firmware:     model.FirmwareInfo{Current: release},
			UptimeSec:    uptime,
			Online:       true,
			LastSeen:     now,
			Capabilities: caps,
			Sensors:      sensors,
		}},
		Links:     links,
		LagGroups: lagGroups,
	}
	for _, n := range st.lldp {
		ln := model.LldpNeighbor{
			DeviceID:      devID,
			LocalLink:     ifName(n.Port),
			ChassisID:     n.Chassis,
			RemotePort:    n.PortID,
			RemoteSysName: n.SysName,
			RemoteMgmtIP:  n.MgmtAddr,
		}
		if ln.RemotePort == "" {
			ln.RemotePort = n.PortDesc
		}
		snap.LLDP = append(snap.LLDP, ln)
	}
	for _, e := range st.fdb {
		if e.Port == 0 {
			continue // the switch's own CPU port
		}
		vlan, _ := strconv.ParseUint(e.VLAN, 16, 16)
		snap.FDB = append(snap.FDB, model.FdbEntry{DeviceID: devID, LinkName: ifName(e.Port), MAC: e.MAC, VLAN: int(vlan)})
	}
	return snap
}

// dbm converts optical power in watts to dBm; zero or negative power has no dBm value.
func dbm(w float64) *float64 {
	if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		return nil
	}
	v := 10 * math.Log10(w*1000)
	return &v
}

// mediaFor infers the port media: the exporter reports no PHY type, only SFP modules and speed.
func mediaFor(speedMbps int, up, hasSFP bool) model.MediaType {
	if hasSFP {
		if speedMbps > 1000 {
			return model.MediaSFPPl
		}
		return model.MediaSFP
	}
	if !up {
		return model.MediaOther
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
	default:
		return model.MediaFE
	}
}
