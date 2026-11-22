// SPDX-License-Identifier: AGPL-3.0-or-later
package snmp

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

// Standard SNMP MIB OID roots.
const (
	oidSysDescr   = ".1.3.6.1.2.1.1.1.0"
	oidSysUpTime  = ".1.3.6.1.2.1.1.3.0"
	oidSysName    = ".1.3.6.1.2.1.1.5.0"
	oidIfTable    = ".1.3.6.1.2.1.2.2.1"
	oidIfXTable   = ".1.3.6.1.2.1.31.1.1.1"
	oidBasePortIf = ".1.3.6.1.2.1.17.1.4.1.2"
	oidDot1qFdb   = ".1.3.6.1.2.1.17.7.1.2.2.1.2"
	oidLldpRem    = ".1.0.8802.1.1.2.1.4.1.1"
	oidPethPort   = ".1.3.6.1.2.1.105.1.1.1"
)

// Walker abstracts SNMP subtree walking so tests can inject deterministic OID fixtures.
type Walker interface {
	Walk(ctx context.Context, rootOID string) ([]gosnmp.SnmpPDU, error)
	Close() error
}

// WalkerFactory constructs a Walker for a given target config.
type WalkerFactory func(cfg driver.TargetConfig) (Walker, error)

// Driver polls managed switches and routers over SNMP v2c or SNMP v3.
type Driver struct {
	newWalker WalkerFactory
}

// New returns a new SNMP v2c/v3 driver using gosnmp.
func New() *Driver {
	return &Driver{newWalker: defaultWalkerFactory}
}

// NewWithFactory returns an SNMP driver using a custom WalkerFactory (for testing).
func NewWithFactory(f WalkerFactory) *Driver {
	return &Driver{newWalker: f}
}

// Kind returns "snmp".
func (d *Driver) Kind() string { return "snmp" }

// ValidateConfig validates SNMP v2c or v3 target settings and required secrets.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("snmp target %q: endpoint (host[:port]) is required", cfg.ID)
	}
	ver := snmpVersion(cfg)
	switch ver {
	case "2c":
		if cfg.ResolvedSecrets["community"] == "" {
			return fmt.Errorf("snmp v2c target %q: community secret is required", cfg.ID)
		}
	case "3":
		user := stringSetting(cfg.Settings, "username", cfg.ResolvedSecrets["username"])
		if user == "" {
			return fmt.Errorf("snmp v3 target %q: username setting or secret is required", cfg.ID)
		}
		if ap := stringSetting(cfg.Settings, "auth_protocol", ""); ap != "" && cfg.ResolvedSecrets["auth_passphrase"] == "" {
			return fmt.Errorf("snmp v3 target %q: auth_passphrase secret is required for auth_protocol %q", cfg.ID, ap)
		}
		if pp := stringSetting(cfg.Settings, "priv_protocol", ""); pp != "" && cfg.ResolvedSecrets["priv_passphrase"] == "" {
			return fmt.Errorf("snmp v3 target %q: priv_passphrase secret is required for priv_protocol %q", cfg.ID, pp)
		}
	default:
		return fmt.Errorf("snmp target %q: unsupported version %q (supported: 2c, 3)", cfg.ID, ver)
	}
	return nil
}

// Poll walks IF-MIB, IF-X-MIB, Q-BRIDGE-MIB, LLDP-MIB, and POWER-ETHERNET-MIB.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	w, err := d.newWalker(cfg)
	if err != nil {
		return nil, fmt.Errorf("snmp connect %s: %w", cfg.Endpoint, err)
	}
	defer func() { _ = w.Close() }()

	sysPDUs, err := w.Walk(ctx, ".1.3.6.1.2.1.1")
	if err != nil {
		return nil, fmt.Errorf("snmp walk system: %w", err)
	}
	var sysDescr, sysName string
	var uptimeSec uint64
	for _, p := range sysPDUs {
		switch p.Name {
		case oidSysDescr:
			sysDescr = pduString(p)
		case oidSysName:
			sysName = pduString(p)
		case oidSysUpTime:
			uptimeSec = pduUint64(p) / 100
		}
	}

	ifPDUs, err := w.Walk(ctx, oidIfTable)
	if err != nil {
		return nil, fmt.Errorf("snmp walk ifTable: %w", err)
	}
	ifXPDUs, _ := w.Walk(ctx, oidIfXTable)
	basePortPDUs, _ := w.Walk(ctx, oidBasePortIf)
	fdbPDUs, _ := w.Walk(ctx, oidDot1qFdb)
	lldpPDUs, _ := w.Walk(ctx, oidLldpRem)
	poePDUs, _ := w.Walk(ctx, oidPethPort)

	type ifAcc struct {
		idx       int
		descr     string
		name      string
		alias     string
		mtu       int
		speed     uint64
		highSpeed uint64
		mac       string
		adminUp   bool
		operUp    bool
		inBytes   uint64
		outBytes  uint64
		inPkts    uint64
		outPkts   uint64
		inErr     uint64
		outErr    uint64
		inDrop    uint64
		outDrop   uint64
	}
	ifaces := map[int]*ifAcc{}
	getIf := func(idx int) *ifAcc {
		if a, ok := ifaces[idx]; ok {
			return a
		}
		a := &ifAcc{idx: idx}
		ifaces[idx] = a
		return a
	}

	for _, p := range ifPDUs {
		col, idx, ok := parseTableOID(p.Name, oidIfTable)
		if !ok {
			continue
		}
		a := getIf(idx)
		switch col {
		case 2:
			a.descr = pduString(p)
		case 4:
			a.mtu = int(pduUint64(p))
		case 5:
			a.speed = pduUint64(p)
		case 6:
			a.mac = pduMAC(p)
		case 7:
			a.adminUp = pduUint64(p) == 1
		case 8:
			a.operUp = pduUint64(p) == 1
		case 10:
			if a.inBytes == 0 {
				a.inBytes = pduUint64(p)
			}
		case 13:
			a.inDrop = pduUint64(p)
		case 14:
			a.inErr = pduUint64(p)
		case 16:
			if a.outBytes == 0 {
				a.outBytes = pduUint64(p)
			}
		case 19:
			a.outDrop = pduUint64(p)
		case 20:
			a.outErr = pduUint64(p)
		}
	}

	for _, p := range ifXPDUs {
		col, idx, ok := parseTableOID(p.Name, oidIfXTable)
		if !ok {
			continue
		}
		a := getIf(idx)
		switch col {
		case 1:
			a.name = pduString(p)
		case 6:
			a.inBytes = pduUint64(p)
		case 7:
			a.inPkts = pduUint64(p)
		case 10:
			a.outBytes = pduUint64(p)
		case 11:
			a.outPkts = pduUint64(p)
		case 15:
			a.highSpeed = pduUint64(p)
		case 18:
			a.alias = pduString(p)
		}
	}

	// Map bridge base port -> ifIndex.
	bridgePortToIf := map[int]int{}
	for _, p := range basePortPDUs {
		parts := strings.Split(strings.TrimPrefix(p.Name, oidBasePortIf+"."), ".")
		if len(parts) == 1 {
			if bp, err := strconv.Atoi(parts[0]); err == nil {
				bridgePortToIf[bp] = int(pduUint64(p))
			}
		}
	}

	// PoE status per port index (group.port -> enabled/state).
	poeByPort := map[int]*model.PortPoE{}
	for _, p := range poePDUs {
		rest := strings.TrimPrefix(p.Name, oidPethPort+".")
		parts := strings.Split(rest, ".")
		if len(parts) != 3 {
			continue
		}
		col, err1 := strconv.Atoi(parts[0])
		portIdx, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		poe := poeByPort[portIdx]
		if poe == nil {
			poe = &model.PortPoE{Supported: true}
			poeByPort[portIdx] = poe
		}
		switch col {
		case 3: // pethPsePortAdminEnable (1=true, 2=false)
			poe.Enabled = pduUint64(p) == 1
		case 6: // pethPsePortDetectionStatus (3=deliveringPower)
			switch pduUint64(p) {
			case 1:
				poe.State = "disabled"
			case 2:
				poe.State = "searching"
			case 3:
				poe.State = "delivering"
			case 4:
				poe.State = "fault"
			}
		}
	}

	idxs := make([]int, 0, len(ifaces))
	for idx := range ifaces {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)

	primaryMAC := ""
	macs := []string{}
	seenMAC := map[string]bool{}
	ifIdxToName := map[int]string{}

	for _, idx := range idxs {
		a := ifaces[idx]
		ifName := a.name
		if ifName == "" {
			ifName = a.descr
		}
		if ifName == "" {
			ifName = fmt.Sprintf("if%d", idx)
		}
		ifIdxToName[idx] = ifName
		if a.mac != "" && a.mac != "00:00:00:00:00:00" && !seenMAC[a.mac] {
			seenMAC[a.mac] = true
			if primaryMAC == "" {
				primaryMAC = a.mac
			}
			macs = append(macs, a.mac)
		}
	}

	now := time.Now().UTC()
	devID := "snmp:" + cfg.ID
	if primaryMAC != "" {
		devID = "snmp:" + primaryMAC
	}
	name := cfg.Name
	if name == "" {
		name = sysName
	}
	if name == "" {
		name = cfg.ID
	}
	host, _, err := net.SplitHostPort(cfg.Endpoint)
	if err != nil {
		host = cfg.Endpoint
	}

	caps := []model.Capability{model.CapLinks}
	if len(fdbPDUs) > 0 {
		caps = append(caps, model.CapFDB, model.CapVLANs)
	}
	if len(lldpPDUs) > 0 {
		caps = append(caps, model.CapLLDP)
	}
	if len(poeByPort) > 0 {
		caps = append(caps, model.CapPoE)
	}

	snap := &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
		Devices: []model.Device{{
			ID:           devID,
			Driver:       "snmp",
			TargetID:     cfg.ID,
			Name:         name,
			Hostname:     sysName,
			Model:        sysDescr,
			Role:         model.RoleSwitch,
			PrimaryMAC:   primaryMAC,
			MACs:         macs,
			MgmtIP:       host,
			UptimeSec:    uptimeSec,
			Online:       true,
			LastSeen:     now,
			Capabilities: caps,
		}},
	}

	for _, idx := range idxs {
		a := ifaces[idx]
		ifName := ifIdxToName[idx]
		if strings.EqualFold(ifName, "lo") || strings.EqualFold(a.descr, "loopback") {
			continue
		}
		speedMbps := int(a.highSpeed)
		if speedMbps == 0 && a.speed > 0 {
			speedMbps = int(a.speed / 1_000_000)
		}
		oper := model.OperDown
		if !a.adminUp {
			oper = model.OperDisabled
		} else if a.operUp {
			oper = model.OperUp
		} else {
			oper = model.OperNoCarrier
		}
		mtu := a.mtu
		media := guessSnmpMedia(ifName, speedMbps)
		snap.Links = append(snap.Links, model.Link{
			DeviceID:  devID,
			Index:     idx,
			IfName:    ifName,
			Label:     a.alias,
			MAC:       a.mac,
			AdminUp:   a.adminUp,
			LinkUp:    a.adminUp && a.operUp,
			OperState: oper,
			SpeedMbps: speedMbps,
			MTU:       &mtu,
			Media:     media,
			IsFiber:   model.IsFiberMedia(media),
			PoE:       poeByPort[idx],
			Counters: &model.PortCounters{
				RxBytes:   a.inBytes,
				TxBytes:   a.outBytes,
				RxPackets: a.inPkts,
				TxPackets: a.outPkts,
				RxErrors:  a.inErr,
				TxErrors:  a.outErr,
				RxDropped: a.inDrop,
				TxDropped: a.outDrop,
			},
		})
	}

	// Q-BRIDGE-MIB FDB: .1.3.6.1.2.1.17.7.1.2.2.1.2.<vlan>.<m0>.<m1>.<m2>.<m3>.<m4>.<m5> = bridgePort
	for _, p := range fdbPDUs {
		rest := strings.TrimPrefix(p.Name, oidDot1qFdb+".")
		parts := strings.Split(rest, ".")
		if len(parts) != 7 {
			continue
		}
		vlan, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		var b [6]byte
		valid := true
		for i := 0; i < 6; i++ {
			v, err := strconv.Atoi(parts[i+1])
			if err != nil || v < 0 || v > 255 {
				valid = false
				break
			}
			b[i] = byte(v)
		}
		if !valid {
			continue
		}
		bp := int(pduUint64(p))
		ifIdx := bp
		if mapped, ok := bridgePortToIf[bp]; ok {
			ifIdx = mapped
		}
		linkName := ifIdxToName[ifIdx]
		if linkName == "" {
			linkName = fmt.Sprintf("if%d", ifIdx)
		}
		snap.FDB = append(snap.FDB, model.FdbEntry{
			DeviceID: devID,
			LinkName: linkName,
			MAC:      fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5]),
			VLAN:     vlan,
		})
	}

	// LLDP-MIB: .1.0.8802.1.1.2.1.4.1.1.<col>.<timeMark>.<localPortNum>.<remIndex>
	type lldpKey struct{ localPort, remIdx string }
	lldpAcc := map[lldpKey]*model.LldpNeighbor{}
	for _, p := range lldpPDUs {
		rest := strings.TrimPrefix(p.Name, oidLldpRem+".")
		parts := strings.Split(rest, ".")
		if len(parts) < 4 {
			continue
		}
		col, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		k := lldpKey{localPort: parts[2], remIdx: parts[3]}
		n := lldpAcc[k]
		if n == nil {
			lpNum, _ := strconv.Atoi(parts[2])
			linkName := ifIdxToName[lpNum]
			if linkName == "" {
				linkName = fmt.Sprintf("if%d", lpNum)
			}
			n = &model.LldpNeighbor{DeviceID: devID, LocalLink: linkName}
			lldpAcc[k] = n
		}
		switch col {
		case 5: // lldpRemChassisId
			if mac := pduMAC(p); mac != "" {
				n.ChassisID = mac
			} else {
				n.ChassisID = pduString(p)
			}
		case 7: // lldpRemPortId
			n.RemotePort = pduString(p)
		case 9: // lldpRemSysName
			n.RemoteSysName = pduString(p)
		}
	}
	for _, n := range lldpAcc {
		snap.LLDP = append(snap.LLDP, *n)
	}

	return snap, nil
}

type goSnmpWalker struct {
	g *gosnmp.GoSNMP
}

func (w *goSnmpWalker) Walk(ctx context.Context, rootOID string) ([]gosnmp.SnmpPDU, error) {
	w.g.Context = ctx
	return w.g.BulkWalkAll(rootOID)
}

func (w *goSnmpWalker) Close() error {
	if w.g.Conn != nil {
		return w.g.Conn.Close()
	}
	return nil
}

func defaultWalkerFactory(cfg driver.TargetConfig) (Walker, error) {
	host, portStr, err := net.SplitHostPort(cfg.Endpoint)
	port := uint16(161)
	if err != nil {
		host = cfg.Endpoint
	} else if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
		port = uint16(p)
	}

	g := &gosnmp.GoSNMP{
		Target:         host,
		Port:           port,
		Timeout:        5 * time.Second,
		Retries:        1,
		MaxRepetitions: 25,
	}

	if snmpVersion(cfg) == "3" {
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		authProto := parseAuthProto(stringSetting(cfg.Settings, "auth_protocol", ""))
		privProto := parsePrivProto(stringSetting(cfg.Settings, "priv_protocol", ""))
		flags := gosnmp.NoAuthNoPriv
		if authProto != gosnmp.NoAuth && privProto != gosnmp.NoPriv {
			flags = gosnmp.AuthPriv
		} else if authProto != gosnmp.NoAuth {
			flags = gosnmp.AuthNoPriv
		}
		g.MsgFlags = flags
		g.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 stringSetting(cfg.Settings, "username", cfg.ResolvedSecrets["username"]),
			AuthenticationProtocol:   authProto,
			AuthenticationPassphrase: cfg.ResolvedSecrets["auth_passphrase"],
			PrivacyProtocol:          privProto,
			PrivacyPassphrase:        cfg.ResolvedSecrets["priv_passphrase"],
		}
	} else {
		g.Version = gosnmp.Version2c
		g.Community = cfg.ResolvedSecrets["community"]
	}

	if err := g.Connect(); err != nil {
		return nil, err
	}
	return &goSnmpWalker{g: g}, nil
}

func snmpVersion(cfg driver.TargetConfig) string {
	v := strings.ToLower(strings.TrimSpace(stringSetting(cfg.Settings, "version", "2c")))
	if v == "2" {
		return "2c"
	}
	return v
}

func parseAuthProto(s string) gosnmp.SnmpV3AuthProtocol {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "MD5":
		return gosnmp.MD5
	case "SHA", "SHA1":
		return gosnmp.SHA
	case "SHA224":
		return gosnmp.SHA224
	case "SHA256":
		return gosnmp.SHA256
	case "SHA384":
		return gosnmp.SHA384
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.NoAuth
	}
}

func parsePrivProto(s string) gosnmp.SnmpV3PrivProtocol {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DES":
		return gosnmp.DES
	case "AES", "AES128":
		return gosnmp.AES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	default:
		return gosnmp.NoPriv
	}
}

func parseTableOID(oid, root string) (col, idx int, ok bool) {
	prefix := root + "."
	if !strings.HasPrefix(oid, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(oid, prefix), ".")
	if len(parts) != 2 {
		return 0, 0, false
	}
	c, err1 := strconv.Atoi(parts[0])
	i, err2 := strconv.Atoi(parts[1])
	return c, i, err1 == nil && err2 == nil
}

func pduString(p gosnmp.SnmpPDU) string {
	switch v := p.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func pduMAC(p gosnmp.SnmpPDU) string {
	b, ok := p.Value.([]byte)
	if !ok || len(b) != 6 {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func pduUint64(p gosnmp.SnmpPDU) uint64 {
	return gosnmp.ToBigInt(p.Value).Uint64()
}

func guessSnmpMedia(ifName string, speedMbps int) model.MediaType {
	lower := strings.ToLower(ifName)
	if strings.Contains(lower, "sfp") || strings.Contains(lower, "fiber") || strings.Contains(lower, "ten") {
		if speedMbps >= 10000 {
			return model.MediaSFPPl
		}
		return model.MediaSFP
	}
	switch {
	case speedMbps >= 10000:
		return model.Media10GE
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

func stringSetting(m map[string]any, key, def string) string {
	if m == nil {
		return def
	}
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}
