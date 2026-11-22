// SPDX-License-Identifier: AGPL-3.0-or-later
package unifi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestUniFiDriverPoll(t *testing.T) {
	loggedIn := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loggedIn = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"unique_id":"admin"}`))
		case "/proxy/network/api/s/default/stat/device":
			if !loggedIn {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{
				"data": [
					{
						"mac": "3c:78:95:1f:71:0d",
						"model": "USL8LP",
						"type": "usw",
						"name": "Shed Switch",
						"ip": "192.0.2.9",
						"version": "7.6.11",
						"serial": "3C78951F710D",
						"state": 1,
						"uptime": 3600,
						"total_max_power": 52.0,
						"has_temperature": true,
						"general_temperature": 44.5,
						"system-stats": {"cpu": "12.5"},
						"port_table": [
							{
								"port_idx": 1, "name": "Port 1", "media": "GE", "enable": true, "up": true,
								"speed": 1000, "full_duplex": true, "autoneg": true, "stp_state": "forwarding",
								"port_poe": true, "poe_enable": true, "poe_power": "6.4", "rx_bytes": 10000, "tx_bytes": 20000
							},
							{
								"port_idx": 9, "name": "SFP+ 9", "media": "SFP+", "enable": true, "up": true,
								"speed": 10000, "full_duplex": true, "sfp_found": true, "sfp_vendor": "FS",
								"sfp_rxpower": "-4.8", "sfp_temperature": "41.2"
							}
						],
						"lldp_table": [
							{"local_port_idx": 9, "chassis_id": "00:11:22:33:44:01", "port_id": "sfp0", "lldp_system_name": "core-fw"}
						]
					},
					{
						"mac": "84:78:48:12:99:50",
						"model": "UAPA6A9",
						"type": "uap",
						"name": "Office AP",
						"ip": "192.0.2.20",
						"state": 1,
						"radio_table": [{"name": "wifi1", "radio": "na", "channel": 36, "ht": 80, "tx_power": 20}],
						"vap_table": [{"radio": "na", "essid": "HomeWiFi", "up": true, "num_sta": 1}],
						"uplink": {"type": "wireless", "uplink_mac": "84:78:48:00:00:01", "rssi": -58, "tx_rate": 866000}
					}
				]
			}`))
		case "/proxy/network/api/s/default/stat/sta":
			_, _ = w.Write([]byte(`{
				"data": [
					{"mac": "de:ad:be:ef:00:02", "ip": "192.0.2.102", "hostname": "pixel", "is_wired": false, "ap_mac": "84:78:48:12:99:50", "essid": "HomeWiFi", "signal": -55, "tx_rate": 1200000, "vlan": 10}
				]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	drv := New()
	if err := drv.ValidateConfig(driver.TargetConfig{ID: "t1"}); err == nil {
		t.Fatal("expected validation error for missing endpoint")
	}

	cfg := driver.TargetConfig{
		ID:       "t-unifi",
		Driver:   "unifi",
		Endpoint: srv.URL,
		Settings: map[string]any{"is_unifi_os": true, "site": "default"},
		ResolvedSecrets: map[string]string{
			"username": "admin",
			"password": "secret",
		},
	}

	snap, err := drv.Poll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	snap.Normalize()
	if len(snap.Devices) != 2 || len(snap.Links) != 2 || len(snap.LLDP) != 1 || len(snap.Radios) != 1 || len(snap.WirelessUplinks) != 1 || len(snap.Clients) != 1 {
		t.Fatalf("unexpected counts: devs=%d links=%d lldp=%d radios=%d uplinks=%d clients=%d",
			len(snap.Devices), len(snap.Links), len(snap.LLDP), len(snap.Radios), len(snap.WirelessUplinks), len(snap.Clients))
	}
	if snap.Devices[0].Role != model.RoleSwitch || snap.Devices[1].Role != model.RoleAP {
		t.Fatalf("unexpected roles: %s / %s", snap.Devices[0].Role, snap.Devices[1].Role)
	}
	if snap.Devices[0].PoEBudget == nil || snap.Devices[0].PoEBudget.UsedWatts != 6.4 {
		t.Fatalf("unexpected PoE budget: %+v", snap.Devices[0].PoEBudget)
	}
	if !snap.Links[1].IsFiber || snap.Links[1].SFP == nil || *snap.Links[1].SFP.RxPowerDbm != -4.8 {
		t.Fatalf("unexpected SFP link: %+v", snap.Links[1])
	}
	if snap.Clients[0].AttachedDeviceID != "unifi:84:78:48:12:99:50" || snap.Clients[0].SSID != "HomeWiFi" {
		t.Fatalf("unexpected client: %+v", snap.Clients[0])
	}
}
