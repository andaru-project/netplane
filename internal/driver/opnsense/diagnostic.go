// SPDX-License-Identifier: AGPL-3.0-or-later
package opnsense

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/andaru-project/netplane/internal/driver"
	"github.com/andaru-project/netplane/internal/model"
)

// pingPoll is how often a running ping job's counters are read.
var pingPoll = time.Second

type pingJob struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"`
	Send      numStr   `json:"send"`
	Received  numStr   `json:"received"`
	Loss      numStr   `json:"loss"`
	Min       *float64 `json:"min"`
	Avg       *float64 `json:"avg"`
	Max       *float64 `json:"max"`
	LastError string   `json:"last_error"`
}

// RunDiagnostic runs ping through OPNsense's ping jobs: a job pings once a second until it is
// stopped, so the driver reads its counters until count probes went out, then stops and
// removes it. Creating a job saves an in-memory model, which a user with
// user-config-readonly is refused.
func (d *Driver) RunDiagnostic(ctx context.Context, cfg driver.TargetConfig, req model.DiagnosticRequest) (*model.DiagnosticResult, error) {
	if err := d.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	if req.Kind != model.DiagPing {
		return nil, fmt.Errorf("%w: opnsense runs ping, not %s", driver.ErrDiagnosticUnsupported, req.Kind)
	}
	host, err := driver.DiagnosticHost(req)
	if err != nil {
		return nil, err
	}
	n, err := driver.DiagnosticCount(req)
	if err != nil {
		return nil, err
	}
	fam := "ip"
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		fam = "ip6"
	}

	a := newAPI(cfg, 15*time.Second)
	defer a.client.CloseIdleConnections()
	var set struct {
		Result      string            `json:"result"`
		UUID        string            `json:"uuid"`
		Validations map[string]string `json:"validations"`
	}
	body := map[string]any{"ping": map[string]any{"settings": map[string]any{"hostname": host, "fam": fam, "description": "netplane"}}}
	if err := a.do(ctx, http.MethodPost, "/api/diagnostics/ping/set", body, &set); err != nil {
		return nil, pingError("set", err)
	}
	if set.Result != "ok" || set.UUID == "" {
		return nil, fmt.Errorf("%w: opnsense refused the ping job: %v", driver.ErrDiagnosticRequest, set.Validations)
	}
	defer func() {
		// Always remove the job, even after a timeout: it would otherwise ping for a day.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var ignored map[string]any
		_ = a.do(dctx, http.MethodPost, "/api/diagnostics/ping/stop/"+set.UUID, map[string]any{}, &ignored)
		_ = a.do(dctx, http.MethodPost, "/api/diagnostics/ping/remove/"+set.UUID, map[string]any{}, &ignored)
	}()
	var started struct {
		Status    string `json:"status"`
		StatusMsg string `json:"status_msg"`
	}
	if err := a.do(ctx, http.MethodPost, "/api/diagnostics/ping/start/"+set.UUID, map[string]any{}, &started); err != nil {
		return nil, pingError("start", err)
	}
	if started.Status != "ok" {
		return nil, fmt.Errorf("opnsense ping start: %s", started.StatusMsg)
	}

	deadline := time.Now().Add(time.Duration(n+5) * time.Second)
	var job pingJob
	for {
		if err := sleep(ctx, pingPoll); err != nil {
			return nil, err
		}
		var jobs struct {
			Rows []pingJob `json:"rows"`
		}
		if err := a.do(ctx, http.MethodGet, "/api/diagnostics/ping/search_jobs", nil, &jobs); err != nil {
			return nil, pingError("search_jobs", err)
		}
		found := false
		for _, j := range jobs.Rows {
			if j.ID == set.UUID {
				job, found = j, true
			}
		}
		if !found {
			return nil, fmt.Errorf("opnsense ping job %s disappeared", set.UUID)
		}
		if job.Send.uint() >= uint64(n) || job.Status != "running" || time.Now().After(deadline) {
			break
		}
	}

	stats := driver.PingStats{Transmitted: int(job.Send.uint()), Received: int(job.Received.uint())}
	if l := job.Loss.float(); l != nil {
		stats.LossPct = *l
	}
	if stats.Received > 0 {
		stats.RTTMinMs, stats.RTTAvgMs, stats.RTTMaxMs = job.Min, job.Avg, job.Max
	}
	noReplies := "no replies"
	if job.LastError != "" {
		noReplies = job.LastError
	}
	res := driver.PingStatsResult(req, host, stats, "", noReplies)
	if stats.Transmitted == 0 {
		res.OK, res.Summary, res.Error = false, fmt.Sprintf("ping %s failed", host), noReplies
	}
	return res, nil
}

// pingError explains a refused ping call: a 400 is a bad request, a 403 a missing privilege.
func pingError(step string, err error) error {
	switch httpStatus(err) {
	case http.StatusBadRequest:
		return fmt.Errorf("%w: opnsense ping %s: %v", driver.ErrDiagnosticRequest, step, err)
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("opnsense ping %s: %w (the API user needs the page-diagnostics-ping privilege)", step, err)
	}
	return fmt.Errorf("opnsense ping %s: %w", step, err)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
