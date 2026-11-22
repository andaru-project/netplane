// SPDX-License-Identifier: AGPL-3.0-or-later
package pfsense

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestPfSenseDriverPoll(t *testing.T) {
	nowUnix := time.Now().UTC().Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "pf-key-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v2/status/system":
			_, _ = w.Write([]byte(`{"data":{"hostname":"pf-core","version":"2.8.0","uptime_sec":86400,"cpu_usage":8.5,"temp_c":41.0,"states_used":4200,"states_max":500000,"load_avg_1":0.15}}`))
		case "/api/v2/status/interfaces":
			_, _ = w.Write([]byte(`{"data":[
				{"name":"wan","descr":"WAN","hwif":"igc0","status":"up","macaddr":"00:11:22:33:44:01","mtu":1500,"ipaddr":"198.51.100.2","subnet":24,"inbytes":90000,"outbytes":50000},
				{"name":"lan","descr":"LAN","hwif":"igc1","status":"up","macaddr":"00:11:22:33:44:02","mtu":1500,"ipaddr":"192.0.2.1","subnet":24,"ipaddrv6":"2001:db8::1","subnetv6":64}
			]}`))
		case "/api/v2/status/gateways":
			_, _ = w.Write([]byte(`{"data":[{"name":"WAN_DHCP","srcip":"198.51.100.1","monitorip":"1.1.1.1","status":"online","delay":4.2,"loss":0.0,"default":true}]}`))
		case "/api/v2/diagnostics/arp_table":
			_, _ = w.Write([]byte(`{"data":[{"ip":"192.0.2.50","mac":"de:ad:be:ef:00:01","interface":"lan","status":"expires"}]}`))
		case "/api/v2/status/dhcp_server/leases":
			_, _ = w.Write([]byte(`{"data":[{"ip":"192.0.2.50","mac":"de:ad:be:ef:00:01","hostname":"nas","type":"static"}]}`))
		case "/api/v2/status/services":
			_, _ = w.Write([]byte(`{"data":[{"name":"unbound","description":"DNS Resolver","status":true},{"name":"pfblockerng","description":"pfBlockerNG","status":true}]}`))
		case "/api/v2/status/wireguard/peers":
			_, _ = w.Write([]byte(`{"data":[{"tun":"tun_wg0","descr":"mobile","public_key":"abc=","endpoint":"203.0.113.5:51820","latest_handshake":` + formatInt64(nowUnix-10) + `,"transfer_rx":1024,"transfer_tx":2048}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	drv := New()
	if err := drv.ValidateConfig(driver.TargetConfig{ID: "pf1"}); err == nil {
		t.Fatal("expected error for missing endpoint")
	}

	snap, err := drv.Poll(context.Background(), driver.TargetConfig{
		ID:              "pf-core",
		Driver:          "pfsense",
		Endpoint:        srv.URL,
		ResolvedSecrets: map[string]string{"api_key": "pf-key-123"},
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if err := snap.Normalize(); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(snap.Devices) != 1 || snap.Devices[0].Role != model.RoleFirewall || snap.Devices[0].MgmtIP != "192.0.2.1" {
		t.Fatalf("unexpected device: %+v", snap.Devices)
	}
	if len(snap.Links) != 2 || len(snap.Segments) != 2 || len(snap.Gateways) != 1 || !snap.Gateways[0].Online {
		t.Fatalf("unexpected links/segments/gateways: %d/%d/%d", len(snap.Links), len(snap.Segments), len(snap.Gateways))
	}
	if len(snap.VPNs) != 1 || !snap.VPNs[0].Up {
		t.Fatalf("unexpected VPNs: %+v", snap.VPNs)
	}
	if len(snap.Firewalls) != 1 || len(snap.Firewalls[0].Services) != 2 {
		t.Fatalf("unexpected firewall state: %+v", snap.Firewalls)
	}
}

func formatInt64(v int64) string {
	b := make([]byte, 0, 20)
	if v == 0 {
		return "0"
	}
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
