// SPDX-License-Identifier: AGPL-3.0-or-later
package rtlplayground

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

// Driver polls an RTLPlayground / Realtek managed switch over its HTTP JSON API.
type Driver struct{}

// New returns a new RTLPlayground switch driver.
func New() *Driver { return &Driver{} }

// Kind returns "rtlplayground".
func (d *Driver) Kind() string { return "rtlplayground" }

// ValidateConfig checks required RTLPlayground target settings.
func (d *Driver) ValidateConfig(cfg driver.TargetConfig) error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("rtlplayground target %q: endpoint is required", cfg.ID)
	}
	return nil
}

// Poll fetches system info, ports (with SFP DOM, EEE, flow control, STP, VLANs), FDB, LAG, and LLDP.
func (d *Driver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(cfg.Endpoint, "/")
	client := driver.NewHTTPClient(cfg, 10*time.Second)
	defer client.CloseIdleConnections()

	user := cfg.ResolvedSecrets["username"]
	pass := cfg.ResolvedSecrets["password"]
	token := cfg.ResolvedSecrets["api_token"]

	var sysInfo struct {
		Hostname string  `json:"hostname"`
		Model    string  `json:"model"`
		MAC      string  `json:"mac"`
		IP       string  `json:"ip"`
		Version  string  `json:"version"`
		Uptime   uint64  `json:"uptime"`
		TempC    float64 `json:"temp_c"`
	}
	if err := getJSON(ctx, client, baseURL+"/api/system", user, pass, token, &sysInfo); err != nil {
		return nil, fmt.Errorf("rtlplayground /api/system: %w", err)
	}

	var ports []struct {
		Index       int    `json:"index"`
		Name        string `json:"name"`
		Label       string `json:"label"`
		Media       string `json:"media"`
		AdminUp     bool   `json:"admin_up"`
		LinkUp      bool   `json:"link_up"`
		SpeedMbps   int    `json:"speed_mbps"`
		Duplex      string `json:"duplex"`
		Autoneg     *bool  `json:"autoneg"`
		MTU         *int   `json:"mtu"`
		EEE         *bool  `json:"eee"`
		FlowRx      *bool  `json:"flow_rx"`
		FlowTx      *bool  `json:"flow_tx"`
		PVID        *int   `json:"pvid"`
		TaggedVLANs []int  `json:"tagged_vlans"`
		StpState    string `json:"stp_state"`
		LagGroupID  string `json:"lag_group_id"`
		SFP         *struct {
			Present      bool     `json:"present"`
			Vendor       string   `json:"vendor"`
			PartNumber   string   `json:"part_number"`
			Serial       string   `json:"serial"`
			WavelengthNm *float64 `json:"wavelength_nm"`
			TempC        *float64 `json:"temp_c"`
			VoltageV     *float64 `json:"voltage_v"`
			BiasMa       *float64 `json:"bias_ma"`
			TxPowerDbm   *float64 `json:"tx_power_dbm"`
			RxPowerDbm   *float64 `json:"rx_power_dbm"`
		} `json:"sfp"`
		Counters *model.PortCounters `json:"counters"`
	}
	if err := getJSON(ctx, client, baseURL+"/api/ports", user, pass, token, &ports); err != nil {
		return nil, fmt.Errorf("rtlplayground /api/ports: %w", err)
	}

	var fdbRows []struct {
		Port string `json:"port"`
		MAC  string `json:"mac"`
		VLAN int    `json:"vlan"`
	}
	_ = getJSON(ctx, client, baseURL+"/api/fdb", user, pass, token, &fdbRows)

	var lagRows []model.LagGroup
	_ = getJSON(ctx, client, baseURL+"/api/lag", user, pass, token, &lagRows)

	var lldpRows []model.LldpNeighbor
	_ = getJSON(ctx, client, baseURL+"/api/lldp", user, pass, token, &lldpRows)

	now := time.Now().UTC()
	primaryMAC, _ := model.NormalizeMAC(sysInfo.MAC)
	devID := "rtlplayground:" + cfg.ID
	if primaryMAC != "" {
		devID = "rtlplayground:" + primaryMAC
	}
	name := cfg.Name
	if name == "" {
		name = sysInfo.Hostname
	}
	if name == "" {
		name = sysInfo.Model
	}

	caps := []model.Capability{
		model.CapLinks,
		model.CapEEE,
		model.CapFlowControl,
		model.CapVLANs,
		model.CapFDB,
		model.CapSfpDom,
		model.CapFirmwareInfo,
	}
	if len(lagRows) > 0 {
		caps = append(caps, model.CapLAG)
	}
	if len(lldpRows) > 0 {
		caps = append(caps, model.CapLLDP)
	}
	var sensors []model.SensorReading
	if sysInfo.TempC > 0 {
		sensors = append(sensors, model.SensorReading{
			Name: "asic", Kind: model.SensorTempC, Value: sysInfo.TempC, Unit: "C", Status: model.AlertOK,
		})
		caps = append(caps, model.CapSensors)
	}

	macs := []string{}
	if primaryMAC != "" {
		macs = append(macs, primaryMAC)
	}

	snap := &driver.Snapshot{
		TargetID:    cfg.ID,
		CollectedAt: now,
		Devices: []model.Device{{
			ID:           devID,
			Driver:       "rtlplayground",
			TargetID:     cfg.ID,
			Name:         name,
			Hostname:     sysInfo.Hostname,
			Vendor:       "RTLPlayground",
			Model:        sysInfo.Model,
			Role:         model.RoleSwitch,
			PrimaryMAC:   primaryMAC,
			MACs:         macs,
			MgmtIP:       sysInfo.IP,
			Firmware:     model.FirmwareInfo{Current: sysInfo.Version},
			UptimeSec:    sysInfo.Uptime,
			Online:       true,
			LastSeen:     now,
			Capabilities: caps,
			Sensors:      sensors,
		}},
	}

	for _, p := range ports {
		media := model.MediaType(strings.ToUpper(strings.TrimSpace(p.Media)))
		if media == "" {
			media = model.MediaOther
		}
		isFiber := model.IsFiberMedia(media)
		oper := model.OperDown
		if !p.AdminUp {
			oper = model.OperDisabled
		} else if p.LinkUp {
			oper = model.OperUp
		} else {
			oper = model.OperNoCarrier
		}

		// Strict SFP guard: never advertise EEE on fiber ports.
		eee := p.EEE
		if isFiber {
			f := false
			eee = &f
		}
		var fc *model.FlowControlState
		if p.FlowRx != nil || p.FlowTx != nil {
			fc = &model.FlowControlState{
				Rx: p.FlowRx != nil && *p.FlowRx,
				Tx: p.FlowTx != nil && *p.FlowTx,
			}
		}
		var sfp *model.SfpDom
		if p.SFP != nil {
			sfp = &model.SfpDom{
				Present:      p.SFP.Present,
				Vendor:       p.SFP.Vendor,
				PartNumber:   p.SFP.PartNumber,
				Serial:       p.SFP.Serial,
				WavelengthNm: p.SFP.WavelengthNm,
				TempC:        p.SFP.TempC,
				VoltageV:     p.SFP.VoltageV,
				BiasMa:       p.SFP.BiasMa,
				TxPowerDbm:   p.SFP.TxPowerDbm,
				RxPowerDbm:   p.SFP.RxPowerDbm,
			}
		}
		snap.Links = append(snap.Links, model.Link{
			DeviceID:    devID,
			Index:       p.Index,
			IfName:      p.Name,
			Label:       p.Label,
			AdminUp:     p.AdminUp,
			LinkUp:      p.LinkUp,
			OperState:   oper,
			SpeedMbps:   p.SpeedMbps,
			Duplex:      p.Duplex,
			Autoneg:     p.Autoneg,
			MTU:         p.MTU,
			Media:       media,
			IsFiber:     isFiber,
			LagGroupID:  p.LagGroupID,
			StpState:    model.StpState(strings.ToLower(p.StpState)),
			EEE:         eee,
			FlowControl: fc,
			PVID:        p.PVID,
			TaggedVLANs: p.TaggedVLANs,
			SFP:         sfp,
			Counters:    p.Counters,
		})
	}

	for _, f := range fdbRows {
		snap.FDB = append(snap.FDB, model.FdbEntry{
			DeviceID: devID, LinkName: f.Port, MAC: f.MAC, VLAN: f.VLAN,
		})
	}
	for _, lag := range lagRows {
		lag.DeviceID = devID
		snap.LagGroups = append(snap.LagGroups, lag)
	}
	for _, n := range lldpRows {
		n.DeviceID = devID
		snap.LLDP = append(snap.LLDP, n)
	}

	return snap, nil
}

func getJSON(ctx context.Context, client *http.Client, url, user, pass, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if user != "" {
		req.SetBasicAuth(user, pass)
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
