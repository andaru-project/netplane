// SPDX-License-Identifier: AGPL-3.0-or-later
package rtlplayground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func TestRTLPlaygroundDriverPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system":
			_, _ = w.Write([]byte(`{"hostname":"lg510s","model":"LG510S","mac":"02:51:0a:00:00:01","ip":"192.0.2.10","version":"24.10.1-rtl","uptime":900,"temp_c":46.5}`))
		case "/api/ports":
			_, _ = w.Write([]byte(`[
				{"index":1,"name":"lan1","media":"2.5GE","admin_up":true,"link_up":true,"speed_mbps":2500,"duplex":"full","eee":true,"flow_rx":true,"flow_tx":true,"pvid":1,"tagged_vlans":[10,20],"stp_state":"forwarding","counters":{"rx_bytes":1000,"tx_bytes":2000}},
				{"index":9,"name":"sfp9","media":"SFP+","admin_up":true,"link_up":true,"speed_mbps":10000,"duplex":"full","eee":true,"pvid":1,"sfp":{"present":true,"vendor":"FS","part_number":"SFP-10GSR-85","rx_power_dbm":-3.2,"temp_c":39.0}}
			]`))
		case "/api/fdb":
			_, _ = w.Write([]byte(`[{"port":"lan1","mac":"de:ad:be:ef:00:01","vlan":1}]`))
		case "/api/lag":
			_, _ = w.Write([]byte(`[]`))
		case "/api/lldp":
			_, _ = w.Write([]byte(`[{"local_link":"sfp9","chassis_id":"00:11:22:33:44:01","remote_port":"sfp0","remote_sys_name":"core-fw"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	drv := New()
	if err := drv.ValidateConfig(driver.TargetConfig{ID: "r1"}); err == nil {
		t.Fatal("expected error for missing endpoint")
	}

	snap, err := drv.Poll(context.Background(), driver.TargetConfig{
		ID:       "sw-lg510s",
		Driver:   "rtlplayground",
		Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	snap.Normalize()
	if len(snap.Devices) != 1 || snap.Devices[0].ID != "rtlplayground:sw-lg510s" || snap.Devices[0].Model != "LG510S" || len(snap.Devices[0].Sensors) != 1 {
		t.Fatalf("unexpected device: %+v", snap.Devices)
	}
	if len(snap.Links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(snap.Links))
	}
	if !*snap.Links[0].EEE || snap.Links[0].FlowControl == nil || !snap.Links[0].FlowControl.Rx {
		t.Fatalf("expected copper EEE+FC enabled: %+v", snap.Links[0])
	}
	// Strict SFP guard: even if raw API reported eee=true, driver forces false on fiber.
	if !snap.Links[1].IsFiber || *snap.Links[1].EEE {
		t.Fatalf("expected SFP+ port to have IsFiber=true and EEE=false: %+v", snap.Links[1])
	}
	if snap.Links[1].SFP == nil || *snap.Links[1].SFP.RxPowerDbm != -3.2 || snap.Links[1].SFP.AlertState != model.AlertOK {
		t.Fatalf("unexpected SFP DOM: %+v", snap.Links[1].SFP)
	}
	if len(snap.FDB) != 1 || len(snap.LLDP) != 1 {
		t.Fatalf("expected 1 FDB and 1 LLDP, got %d/%d", len(snap.FDB), len(snap.LLDP))
	}
}

func TestRTLPlaygroundParseMedia(t *testing.T) {
	for in, want := range map[string]model.MediaType{
		" sfp+ ": model.MediaSFPPl, "2.5ge": model.Media25GE, "other": model.MediaOther,
		"OTHER": model.MediaOther, "bogus": model.MediaOther, "": model.MediaOther,
	} {
		if got := parseMedia(in); got != want {
			t.Errorf("parseMedia(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRTLPlaygroundRefusesCredentialsOverHTTP(t *testing.T) {
	cfg := driver.TargetConfig{ID: "r", Endpoint: "http://192.0.2.10",
		ResolvedSecrets: map[string]string{"api_token": "t"}}
	if err := New().ValidateConfig(cfg); err == nil {
		t.Fatal("expected refusal to send a token over plain http")
	}
	cfg.Settings = map[string]any{"allow_insecure_http": true}
	if err := New().ValidateConfig(cfg); err != nil {
		t.Fatalf("opt-in rejected: %v", err)
	}
}
