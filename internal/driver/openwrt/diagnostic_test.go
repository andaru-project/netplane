// SPDX-License-Identifier: AGPL-3.0-or-later
package openwrt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

const traceOut = `traceroute to example.com (93.184.215.14), 20 hops max, 46 byte packets
 1  192.0.2.1  0.512 ms
 2  *
 3  93.184.215.14  9.871 ms`

// execServer fakes rpcd: login, then file.exec answered by exec for the command and params.
func execServer(t *testing.T, exec func(cmd string, params []string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var sid, obj, method string
		_ = json.Unmarshal(req.Params[0], &sid)
		_ = json.Unmarshal(req.Params[1], &obj)
		_ = json.Unmarshal(req.Params[2], &method)
		switch {
		case obj == "session" && method == "login":
			_, _ = w.Write([]byte(`{"result":[0,{"ubus_rpc_session":"diag-sid"}]}`))
		case obj == "file" && method == "exec" && sid == "diag-sid":
			var p struct {
				Command string   `json:"command"`
				Params  []string `json:"params"`
			}
			_ = json.Unmarshal(req.Params[3], &p)
			_, _ = w.Write([]byte(exec(p.Command, p.Params)))
		default:
			t.Errorf("unexpected call %s.%s (sid %s)", obj, method, sid)
		}
	}))
}

func execReply(code int, stdout string) string {
	b, _ := json.Marshal(map[string]any{"code": code, "stdout": stdout})
	return `{"result":[0,` + string(b) + `]}`
}

func diagCfg(url string) driver.TargetConfig {
	return driver.TargetConfig{ID: "ow", Endpoint: url, Settings: map[string]any{"allow_insecure_http": true},
		ResolvedSecrets: map[string]string{"username": "netplane", "password": "pw"}}
}

func TestOpenWrtPingFallsBackToUsrBin(t *testing.T) {
	var tried []string
	srv := execServer(t, func(cmd string, params []string) string {
		tried = append(tried, cmd)
		if cmd == "/bin/ping" {
			return `{"result":[4]}` // UBUS_STATUS_NOT_FOUND
		}
		if !slices.Equal(params, []string{"-c", "2", "-w", "4", "192.0.2.1"}) {
			t.Errorf("params = %v", params)
		}
		return execReply(0, "2 packets transmitted, 2 packets received, 0% packet loss\nround-trip min/avg/max = 1.0/1.5/2.0 ms\n")
	})
	defer srv.Close()
	res, err := New().RunDiagnostic(context.Background(), diagCfg(srv.URL), model.DiagnosticRequest{
		Kind: model.DiagPing, DeviceID: "openwrt:ow", Target: "192.0.2.1", Params: map[string]string{"count": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Summary != "2/2 replies from 192.0.2.1 (0% loss), avg 1.5 ms" || !slices.Equal(tried, []string{"/bin/ping", "/usr/bin/ping"}) {
		t.Fatalf("res=%+v tried=%v", res, tried)
	}
}

func TestOpenWrtTraceroute(t *testing.T) {
	srv := execServer(t, func(cmd string, params []string) string {
		if cmd != "/usr/bin/traceroute" || params[len(params)-1] != "example.com" || params[0] != "-n" {
			t.Errorf("exec %s %v", cmd, params)
		}
		return execReply(0, traceOut)
	})
	defer srv.Close()
	res, err := New().RunDiagnostic(context.Background(), diagCfg(srv.URL), model.DiagnosticRequest{Kind: model.DiagTraceroute, Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	hops := res.Structured.(map[string]any)["hops"].([]TracerouteHop)
	if !res.OK || res.Summary != "example.com (93.184.215.14) reached in 3 hops" || len(hops) != 3 || hops[1].Address != "" || *hops[2].RTTMs != 9.871 {
		t.Fatalf("res=%+v hops=%+v", res, hops)
	}
}

func TestTracerouteResultNotReachedOrFailed(t *testing.T) {
	req := model.DiagnosticRequest{Kind: model.DiagTraceroute}
	partial := strings.Join(strings.Split(traceOut, "\n")[:3], "\n")
	if res := tracerouteResult(req, "example.com", 0, partial); res.OK || res.Summary != "example.com (93.184.215.14) not reached within 2 hops" {
		t.Errorf("partial: %+v", res)
	}
	if res := tracerouteResult(req, "nope.lan", 1, "traceroute: bad address 'nope.lan'"); res.OK || res.Error != "traceroute: bad address 'nope.lan'" {
		t.Errorf("failed: %+v", res)
	}
}

func TestOpenWrtDiagnosticErrors(t *testing.T) {
	srv := execServer(t, func(string, []string) string {
		return `{"jsonrpc":"2.0","id":1,"error":{"code":-32002,"message":"Access denied"}}`
	})
	defer srv.Close()
	d, cfg := New(), diagCfg(srv.URL)
	for _, tc := range []struct {
		req  model.DiagnosticRequest
		want error
	}{
		{model.DiagnosticRequest{Kind: model.DiagCableTest, Target: "1/2"}, driver.ErrDiagnosticUnsupported},
		{model.DiagnosticRequest{Kind: model.DiagPing, Target: "-f"}, driver.ErrDiagnosticRequest},
		{model.DiagnosticRequest{Kind: model.DiagPing, Target: "192.0.2.1", Params: map[string]string{"count": "50"}}, driver.ErrDiagnosticRequest},
		{model.DiagnosticRequest{Kind: model.DiagPing, Target: "192.0.2.1"}, errAccessDenied},
	} {
		if _, err := d.RunDiagnostic(context.Background(), cfg, tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%+v: err = %v, want %v", tc.req, err, tc.want)
		}
	}
}
