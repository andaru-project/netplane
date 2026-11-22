// SPDX-License-Identifier: AGPL-3.0-or-later
package openwrt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestOpenWrtDriverPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var obj, method string
		_ = json.Unmarshal(req.Params[1], &obj)
		_ = json.Unmarshal(req.Params[2], &method)

		switch obj + "." + method {
		case "session.login":
			_, _ = w.Write([]byte(`{"result":[0,{"ubus_rpc_session":"sid-123"}]}`))
		case "system.board":
			_, _ = w.Write([]byte(`{"result":[0,{"hostname":"sw-lab","model":"RTL930x Switch","release":{"description":"OpenWrt 24.10.1"}}]}`))
		case "system.info":
			_, _ = w.Write([]byte(`{"result":[0,{"uptime":7200,"load":[6553],"memory":{"total":134217728,"free":67108864}}]}`))
		case "network.device.status":
			_, _ = w.Write([]byte(`{"result":[0,{
				"lo":{"type":"Network device","up":true,"carrier":true,"mtu":65536},
				"lan1":{"type":"Network device","up":true,"carrier":true,"mtu":1500,"macaddr":"02:51:0a:00:00:01","speed":"2500F","autoneg":true,"flow-control":{"rx":true,"tx":true},"statistics":{"rx_bytes":1000,"tx_bytes":2000}},
				"sfp9":{"type":"Network device","up":true,"carrier":true,"mtu":1500,"macaddr":"02:51:0a:00:00:02","speed":"10000F","statistics":{"rx_bytes":50000,"tx_bytes":40000}}
			}]}`))
		case "network.interface.dump":
			_, _ = w.Write([]byte(`{"result":[0,{"interface":[
				{"interface":"lan","up":true,"proto":"static","l3_device":"br-lan","ipv4-address":[{"address":"192.0.2.10","mask":24}],"ipv6-address":[{"address":"2001:db8::10","mask":64}]}
			]}]}`))
		case "network.wireless.status":
			_, _ = w.Write([]byte(`{"result":[0,{}]}`))
		case "luci-rpc.getHostHints":
			_, _ = w.Write([]byte(`{"result":[0,{"DE:AD:BE:EF:00:01":{"ipaddrs":["192.0.2.50"],"name":"pc1"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"result":[4]}`))
		}
	}))
	defer srv.Close()

	drv := New()
	if err := drv.ValidateConfig(driver.TargetConfig{ID: "ow1"}); err == nil {
		t.Fatal("expected error for missing endpoint")
	}

	snap, err := drv.Poll(context.Background(), driver.TargetConfig{
		ID:       "ow-sw",
		Driver:   "openwrt",
		Endpoint: srv.URL,
		ResolvedSecrets: map[string]string{
			"username": "root",
			"password": "secret",
		},
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	snap.Normalize()
	if len(snap.Devices) != 1 || snap.Devices[0].MgmtIP != "192.0.2.10" {
		t.Fatalf("unexpected device: %+v", snap.Devices)
	}
	if len(snap.Links) != 2 || snap.Links[0].SpeedMbps != 2500 || !snap.Links[1].IsFiber || snap.Links[1].Media != model.MediaSFPPl {
		t.Fatalf("unexpected links: %+v", snap.Links)
	}
	if len(snap.Segments) != 1 || len(snap.Segments[0].Addresses) != 2 {
		t.Fatalf("unexpected segments: %+v", snap.Segments)
	}
	if len(snap.Neighbors) != 1 || snap.Neighbors[0].MAC != "de:ad:be:ef:00:01" {
		t.Fatalf("unexpected neighbors: %+v", snap.Neighbors)
	}
}

func TestOpenWrtEmptySessionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[0,{"ubus_rpc_session":""}]}`))
	}))
	defer srv.Close()
	_, err := New().Poll(context.Background(), driver.TargetConfig{
		ID: "ow1", Endpoint: srv.URL,
		ResolvedSecrets: map[string]string{"username": "root", "password": "pw"},
	})
	if err == nil || strings.Contains(err.Error(), "%!") {
		t.Fatalf("want plain empty-session error, got %v", err)
	}
}
