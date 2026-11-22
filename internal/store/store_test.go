// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/andrebrait/netplane/internal/driver"
	"github.com/andrebrait/netplane/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "netplane.db")
	st, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStoreTargetsAndIdempotentMigrate(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "netplane.db")
	st1, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	cfg := driver.TargetConfig{
		ID:           "t-sw1",
		Name:         "LG510S",
		Driver:       "openwrt",
		Endpoint:     "https://192.0.2.10",
		Enabled:      true,
		PollInterval: 10 * time.Second,
		SecretRefs:   map[string]model.SecretRef{"token": {Secret: "SW1_TOKEN"}},
	}
	if err := st1.UpsertTarget(ctx, cfg); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}
	_ = st1.Close()

	// Re-open verifies migration idempotency and persisted data.
	st2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer st2.Close()

	targets, err := st2.ListTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].ID != "t-sw1" {
		t.Fatalf("ListTargets = (%v, %v)", targets, err)
	}
	if err := st2.DeleteTarget(ctx, "t-sw1"); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	targets, _ = st2.ListTargets(ctx)
	if len(targets) != 0 {
		t.Fatalf("expected 0 targets after delete, got %d", len(targets))
	}
}

func TestStoreSnapshotRatesAndRetention(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	st.SetRetention(10 * time.Minute)

	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	rxDbm := -5.4
	tempC := 43.2

	snap1 := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: t0,
		Devices: []model.Device{{
			ID:         "openwrt:02:51:0a:00:00:01",
			Driver:     "openwrt",
			TargetID:   "t1",
			Name:       "LG510S",
			Role:       model.RoleSwitch,
			PrimaryMAC: "02510A000001",
			Online:     true,
			LastSeen:   t0,
		}},
		Links: []model.Link{{
			DeviceID: "openwrt:02:51:0a:00:00:01",
			Index:    9,
			IfName:   "sfp1",
			Media:    model.MediaSFPPl,
			AdminUp:  true,
			LinkUp:   true,
			SFP:      &model.SfpDom{Present: true, RxPowerDbm: &rxDbm, TempC: &tempC},
			Counters: &model.PortCounters{RxBytes: 1000, TxBytes: 2000},
		}},
	}
	prev, err := st.SaveSnapshot(ctx, snap1)
	if err != nil {
		t.Fatalf("SaveSnapshot 1: %v", err)
	}
	if prev != nil {
		t.Fatalf("expected nil prev on first save, got %+v", prev)
	}

	// Second poll 10s later: +10,000 RxBytes = 8,000 bps; +20,000 TxBytes = 16,000 bps.
	t1 := t0.Add(10 * time.Second)
	snap2 := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: t1,
		Devices:     snap1.Devices,
		Links: []model.Link{{
			DeviceID: "openwrt:02:51:0a:00:00:01",
			Index:    9,
			IfName:   "sfp1",
			Media:    model.MediaSFPPl,
			AdminUp:  true,
			LinkUp:   true,
			SFP:      &model.SfpDom{Present: true, RxPowerDbm: &rxDbm, TempC: &tempC},
			Counters: &model.PortCounters{RxBytes: 11000, TxBytes: 22000},
		}},
	}
	prev, err = st.SaveSnapshot(ctx, snap2)
	if err != nil {
		t.Fatalf("SaveSnapshot 2: %v", err)
	}
	if prev == nil || prev.CollectedAt != t0 {
		t.Fatalf("unexpected prev snapshot: %+v", prev)
	}
	if snap2.Links[0].Rates == nil || snap2.Links[0].Rates.RxBps != 8000 || snap2.Links[0].Rates.TxBps != 16000 {
		t.Fatalf("unexpected computed rates: %+v", snap2.Links[0].Rates)
	}

	// Verify indexed device lookup & merged state.
	dev, ok, err := st.GetDevice(ctx, "openwrt:02:51:0a:00:00:01")
	if err != nil || !ok || dev.PrimaryMAC != "02:51:0a:00:00:01" {
		t.Fatalf("GetDevice = (%+v, %v, %v)", dev, ok, err)
	}
	state, err := st.LoadState(ctx)
	if err != nil || len(state.Devices) != 1 || len(state.Links) != 1 {
		t.Fatalf("LoadState = (%+v, %v)", state, err)
	}

	// Third poll 20m later prunes samples from t0/t1 (retention = 10m).
	t2 := t0.Add(20 * time.Minute)
	snap3 := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: t2,
		Devices:     snap1.Devices,
		Links: []model.Link{{
			DeviceID: "openwrt:02:51:0a:00:00:01",
			Index:    9,
			IfName:   "sfp1",
			Media:    model.MediaSFPPl,
			Counters: &model.PortCounters{RxBytes: 500, TxBytes: 500}, // simulates counter reset
		}},
	}
	if _, err := st.SaveSnapshot(ctx, snap3); err != nil {
		t.Fatalf("SaveSnapshot 3: %v", err)
	}
	if snap3.Links[0].Rates.RxBps < 0 {
		t.Fatalf("negative bitrate after counter reset: %v", snap3.Links[0].Rates.RxBps)
	}

	samples, err := st.QueryLinkSamples(ctx, "openwrt:02:51:0a:00:00:01", "sfp1", t0)
	if err != nil {
		t.Fatalf("QueryLinkSamples: %v", err)
	}
	if len(samples) != 1 || !samples[0].Time.Equal(t2) {
		t.Fatalf("expected 1 retained sample at t2, got %+v", samples)
	}

	// Verify PoE + SFP DOM sample readback and missing device lookup.
	t3 := t2.Add(15 * time.Second)
	snap4 := &driver.Snapshot{
		TargetID:    "t1",
		CollectedAt: t3,
		Devices:     snap1.Devices,
		Links: []model.Link{{
			DeviceID: "openwrt:02:51:0a:00:00:01",
			IfName:   "lan1",
			Media:    model.Media25GE,
			PoE:      &model.PortPoE{Supported: true, Enabled: true, Watts: 12.4},
			Rates:    &model.PortRates{RxBps: 5000, TxBps: 7000},
		}},
	}
	if _, err := st.SaveSnapshot(ctx, snap4); err != nil {
		t.Fatalf("SaveSnapshot 4: %v", err)
	}
	poeSamples, err := st.QueryLinkSamples(ctx, "openwrt:02:51:0a:00:00:01", "lan1", t2)
	if err != nil || len(poeSamples) != 1 || poeSamples[0].PoEWatts == nil || *poeSamples[0].PoEWatts != 12.4 {
		t.Fatalf("unexpected PoE samples: (%+v, %v)", poeSamples, err)
	}
	if _, ok, err := st.GetDevice(ctx, "nonexistent"); err != nil || ok {
		t.Fatalf("expected missing device false, got (%v, %v)", ok, err)
	}
	if _, err := st.SaveSnapshot(ctx, nil); err == nil {
		t.Fatal("expected error for nil snapshot")
	}
}

func TestStoreTopologyAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if raw, ok, err := st.LoadTopologyRaw(ctx); err != nil || ok || raw != nil {
		t.Fatalf("expected empty topology, got (%v, %v, %v)", raw, ok, err)
	}
	now := time.Now().UTC()
	if err := st.SaveTopology(ctx, now, map[string]any{"nodes": []string{"d1"}}); err != nil {
		t.Fatalf("SaveTopology: %v", err)
	}
	raw, ok, err := st.LoadTopologyRaw(ctx)
	if err != nil || !ok || len(raw) == 0 {
		t.Fatalf("LoadTopologyRaw = (%s, %v, %v)", raw, ok, err)
	}

	run := model.DiagnosticResult{
		ID:         "diag-1",
		Kind:       model.DiagPing,
		DeviceID:   "d1",
		Target:     "1.1.1.1",
		OK:         true,
		StartedAt:  now,
		DurationMs: 14,
		Summary:    "3/3 packets received",
	}
	if err := st.SaveDiagnosticRun(ctx, run); err != nil {
		t.Fatalf("SaveDiagnosticRun: %v", err)
	}
	runs, err := st.ListDiagnosticRuns(ctx, "d1", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != "diag-1" {
		t.Fatalf("ListDiagnosticRuns = (%+v, %v)", runs, err)
	}
	allRuns, err := st.ListDiagnosticRuns(ctx, "", 10)
	if err != nil || len(allRuns) != 1 {
		t.Fatalf("ListDiagnosticRuns(all) = (%+v, %v)", allRuns, err)
	}
}

func TestStorePruneTargetsAndStaleDevices(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	save := func(target string, devIDs ...string) {
		t.Helper()
		snap := &driver.Snapshot{TargetID: target}
		for _, id := range devIDs {
			snap.Devices = append(snap.Devices, model.Device{ID: id, TargetID: target, Online: true})
		}
		if _, err := st.SaveSnapshot(ctx, snap); err != nil {
			t.Fatalf("SaveSnapshot %s: %v", target, err)
		}
	}
	has := func(id string) bool {
		t.Helper()
		_, ok, err := st.GetDevice(ctx, id)
		if err != nil {
			t.Fatalf("GetDevice %s: %v", id, err)
		}
		return ok
	}

	save("keep", "k1", "k2")
	save("keep", "k1") // k2 left the target's snapshot: its index row must go too
	save("gone", "g1")
	if has("k2") || !has("g1") {
		t.Fatalf("after resave: k2=%v g1=%v", has("k2"), has("g1"))
	}

	if err := st.PruneTargets(ctx, map[string]bool{"keep": true}); err != nil {
		t.Fatalf("PruneTargets: %v", err)
	}
	state, err := st.LoadState(ctx)
	if err != nil || len(state.Devices) != 1 || state.Devices[0].ID != "k1" || has("g1") {
		t.Fatalf("after prune: devices=%v err=%v g1=%v", state.Devices, err, has("g1"))
	}
}

func TestLoadStateMergesOverlappingTargets(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	router := &driver.Snapshot{TargetID: "router", Clients: []model.Client{
		{MAC: "aa:bb:cc:dd:ee:ff", Hostname: "phone", IPs: []string{"192.0.2.7"}, LastSeen: t0},
	}}
	ap := &driver.Snapshot{TargetID: "ap", Clients: []model.Client{
		{MAC: "aa:bb:cc:dd:ee:ff", Online: true, AttachedDeviceID: "ap1", IPs: []string{"2001:db8::7"}, LastSeen: t0.Add(time.Minute)},
	}}
	for _, s := range []*driver.Snapshot{router, ap} {
		if _, err := st.SaveSnapshot(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	state, err := st.LoadState(ctx)
	if err != nil || len(state.Clients) != 1 {
		t.Fatalf("clients = %+v err=%v", state.Clients, err)
	}
	c := state.Clients[0]
	if !c.Online || c.AttachedDeviceID != "ap1" || c.Hostname != "phone" || len(c.IPs) != 2 || !c.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("merged client = %+v", c)
	}
}

func TestMergeClientsPrefersFreshAssociation(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	got := mergeClients([]model.Client{
		{MAC: "aa:bb:cc:dd:ee:ff", AttachedDeviceID: "ap-a", LastSeen: t0},
		{MAC: "aa:bb:cc:dd:ee:ff", AttachedDeviceID: "ap-b", Online: true, LastSeen: t0.Add(5 * time.Minute)},
	})
	if len(got) != 1 || got[0].AttachedDeviceID != "ap-b" || !got[0].Online {
		t.Fatalf("merged = %+v", got)
	}
}

func TestPruneTargetsDropsStoredTopology(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := st.SaveSnapshot(ctx, &driver.Snapshot{TargetID: "gone"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTopology(ctx, time.Now(), map[string]any{"nodes": []string{"gone"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PruneTargets(ctx, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.LoadTopologyRaw(ctx); err != nil || ok {
		t.Fatalf("stored topology survived prune: ok=%v err=%v", ok, err)
	}
}

func TestMergeClientsThreeWayAndOfflineRace(t *testing.T) {
	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	got := mergeClients([]model.Client{
		{MAC: "aa:bb:cc:dd:ee:ff", AttachedDeviceID: "ap-a", LastSeen: t0},
		{MAC: "aa:bb:cc:dd:ee:ff", Online: true, LastSeen: t0.Add(2 * time.Minute)},
		{MAC: "aa:bb:cc:dd:ee:ff", AttachedDeviceID: "ap-b", Online: true, LastSeen: t0.Add(time.Minute)},
	})
	if len(got) != 1 || got[0].AttachedDeviceID != "ap-b" || !got[0].LastSeen.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("merged = %+v", got)
	}

	ctx := context.Background()
	st := openTestStore(t)
	snap := &driver.Snapshot{TargetID: "t1", CollectedAt: t0.Add(time.Minute), Devices: []model.Device{{ID: "d1", Online: true}}}
	if _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	// A failed attempt that started before the stored (successful) snapshot must not mark it offline.
	if prev, curr, err := st.MarkTargetOffline(ctx, "t1", t0); err != nil || prev != nil || curr != nil {
		t.Fatalf("stale mark applied: prev=%v curr=%v err=%v", prev, curr, err)
	}
	if _, curr, err := st.MarkTargetOffline(ctx, "t1", t0.Add(2*time.Minute)); err != nil || curr == nil || curr.Devices[0].Online {
		t.Fatalf("mark after snapshot: curr=%+v err=%v", curr, err)
	}
}

func TestPruneTargetsKeepsTopologyForConfigOnlyRows(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.UpsertTarget(ctx, driver.TargetConfig{ID: "disabled", Driver: "openwrt"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTopology(ctx, time.Now(), map[string]any{"nodes": []string{}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PruneTargets(ctx, map[string]bool{"disabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.LoadTopologyRaw(ctx); err != nil || !ok {
		t.Fatalf("topology wiped for a target with no data: ok=%v err=%v", ok, err)
	}
}
