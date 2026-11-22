// SPDX-License-Identifier: AGPL-3.0-or-later
package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/events"
	"github.com/andrebrait/netplane/internal/model"
	"github.com/andrebrait/netplane/internal/store"
)

type mockDriver struct {
	kind  string
	calls atomic.Int32
	fn    func(ctx context.Context, cfg driver.TargetConfig, call int32) (*driver.Snapshot, error)
}

func (m *mockDriver) Kind() string                             { return m.kind }
func (m *mockDriver) ValidateConfig(driver.TargetConfig) error { return nil }
func (m *mockDriver) Poll(ctx context.Context, cfg driver.TargetConfig) (*driver.Snapshot, error) {
	n := m.calls.Add(1)
	return m.fn(ctx, cfg, n)
}

func TestSchedulerConcurrentIsolationAndShutdown(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	defer st.Close()

	reg := driver.NewRegistry()
	goodDrv := &mockDriver{
		kind: "mock-good",
		fn: func(_ context.Context, cfg driver.TargetConfig, n int32) (*driver.Snapshot, error) {
			return &driver.Snapshot{
				TargetID:    cfg.ID,
				CollectedAt: time.Now().UTC(),
				Devices: []model.Device{{
					ID: "mock:00:11:22:33:44:55", Driver: "mock", TargetID: cfg.ID,
					Name: "LabSwitch", Role: model.RoleSwitch, PrimaryMAC: "00:11:22:33:44:55",
					Online: true, LastSeen: time.Now().UTC(),
				}},
				Links: []model.Link{{
					DeviceID: "mock:00:11:22:33:44:55", IfName: "lan1",
					LinkUp: n%2 == 1, Media: model.Media25GE,
				}},
			}, nil
		},
	}
	badDrv := &mockDriver{
		kind: "mock-bad",
		fn: func(_ context.Context, _ driver.TargetConfig, _ int32) (*driver.Snapshot, error) {
			return nil, errors.New("connection refused")
		},
	}
	_ = reg.Register(goodDrv)
	_ = reg.Register(badDrv)

	bus := events.NewBus()
	evCh, unsub := bus.Subscribe(32)
	defer unsub()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := New(reg, st, bus, logger)
	sched.SetMaxBackoff(80 * time.Millisecond)

	targets := []driver.TargetConfig{
		{ID: "t-good", Name: "Good", Driver: "mock-good", Enabled: true, PollInterval: 20 * time.Millisecond},
		{ID: "t-bad", Name: "Bad", Driver: "mock-bad", Enabled: true, PollInterval: 20 * time.Millisecond},
		{ID: "t-disabled", Name: "Disabled", Driver: "mock-good", Enabled: false, PollInterval: 20 * time.Millisecond},
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		sched.Run(runCtx, targets)
		close(done)
	}()

	// Wait until goodDrv completes at least 2 polls (triggering link transition + topology update).
	deadline := time.Now().Add(2 * time.Second)
	for goodDrv.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if goodDrv.calls.Load() < 2 {
		t.Fatalf("expected >= 2 polls on good target, got %d", goodDrv.calls.Load())
	}

	stopStart := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("scheduler did not shut down within 100ms")
	}
	if elapsed := time.Since(stopStart); elapsed > 100*time.Millisecond {
		t.Fatalf("shutdown took %v, want <= 100ms", elapsed)
	}

	// Verify failure isolation: good target succeeded while bad target recorded errors.
	statuses := sched.Status()
	if statuses["t-good"].ConsecutiveFailures != 0 || statuses["t-good"].LastSuccess.IsZero() {
		t.Fatalf("unexpected good status: %+v", statuses["t-good"])
	}
	if statuses["t-bad"].ConsecutiveFailures == 0 || statuses["t-bad"].LastError == "" {
		t.Fatalf("expected error recorded on bad target: %+v", statuses["t-bad"])
	}
	if _, ok := statuses["t-disabled"]; ok {
		t.Fatal("disabled target should not have been polled")
	}

	// Verify topology snapshot saved and events emitted.
	if raw, ok, err := st.LoadTopologyRaw(ctx); err != nil || !ok || len(raw) == 0 {
		t.Fatalf("expected persisted topology, got (%s, %v, %v)", raw, ok, err)
	}
	if len(evCh) == 0 {
		t.Fatal("expected emitted events on bus")
	}
}

func TestPollOnceUnknownDriverAndBackoff(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	defer st.Close()

	sched := New(driver.NewRegistry(), st, nil, nil)
	err := sched.PollOnce(ctx, driver.TargetConfig{ID: "t1", Driver: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if got := backoffDuration(10*time.Second, 0, time.Minute); got != 10*time.Second {
		t.Fatalf("backoff(0) = %v", got)
	}
	if got := backoffDuration(10*time.Second, 3, time.Minute); got != 40*time.Second {
		t.Fatalf("backoff(3) = %v", got)
	}
	if got := backoffDuration(10*time.Second, 10, time.Minute); got != time.Minute {
		t.Fatalf("backoff(10) = %v, want 1m cap", got)
	}
}
