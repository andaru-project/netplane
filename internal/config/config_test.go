// SPDX-License-Identifier: AGPL-3.0-or-later
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseAndSecretResolution(t *testing.T) {
	yamlData := []byte(`
server:
  listen: "127.0.0.1:8099"
  db_path: "/tmp/netplane.db"
  retention: "12h"
  trust_ingress_proxy: true
  api_token_secret: "${NETPLANE_TOKEN}"
targets:
  - id: fw-core
    name: Core Firewall
    driver: pfsense
    endpoint: "https://192.0.2.1"
    poll_interval: "10s"
    secrets:
      api_key: "${PFSENSE_API_KEY}"
  - id: sw-disabled
    driver: openwrt
    endpoint: "https://192.0.2.10"
    enabled: false
    secrets:
      password: "MISSING_DISABLED_SECRET"
`)
	env := map[string]string{
		"NETPLANE_TOKEN":  "tok-123",
		"PFSENSE_API_KEY": "pf-secret",
	}
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}

	cfg, err := Parse(yamlData, lookup)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8099" || cfg.Retention != 12*time.Hour || cfg.Server.ResolvedAPIToken != "tok-123" {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if len(cfg.Targets) != 2 {
		t.Fatalf("targets len = %d, want 2", len(cfg.Targets))
	}
	if cfg.Targets[0].ResolvedSecrets["api_key"] != "pf-secret" || cfg.Targets[0].SecretRefs["api_key"].Secret != "PFSENSE_API_KEY" {
		t.Fatalf("unexpected secrets: %+v / %+v", cfg.Targets[0].SecretRefs, cfg.Targets[0].ResolvedSecrets)
	}
	if cfg.Targets[1].Enabled || cfg.Targets[1].Name != "sw-disabled" {
		t.Fatalf("unexpected disabled target: %+v", cfg.Targets[1])
	}

	// Verify LoadFile round-trip.
	p := filepath.Join(t.TempDir(), "netplane.yaml")
	_ = os.WriteFile(p, []byte("targets:\n  - id: t1\n    driver: snmp\n    endpoint: 192.0.2.2\n"), 0o600)
	fc, err := LoadFile(p)
	if err != nil || fc.Server.Listen != ":8099" || len(fc.Targets) != 1 {
		t.Fatalf("LoadFile = (%+v, %v)", fc, err)
	}
}

func TestParseErrors(t *testing.T) {
	empty := func(string) (string, bool) { return "", false }
	cases := []struct {
		name string
		yaml string
	}{
		{"bad_yaml", "server: [unclosed"},
		{"bad_retention", "server:\n  retention: nope"},
		{"missing_api_token", "server:\n  api_token_secret: MISSING_TOK"},
		{"missing_target_id", "targets:\n  - driver: snmp"},
		{"dup_target_id", "targets:\n  - id: a\n    driver: snmp\n  - id: a\n    driver: snmp"},
		{"missing_driver", "targets:\n  - id: a"},
		{"bad_poll_interval", "targets:\n  - id: a\n    driver: snmp\n    poll_interval: -5s"},
		{"missing_enabled_secret", "targets:\n  - id: a\n    driver: snmp\n    secrets:\n      community: SNMP_COMM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml), empty); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTokenEnvFallbackAndHAOptions(t *testing.T) {
	env := map[string]string{TokenEnv: "env-tok"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cfg, err := Parse([]byte("server: {}\n"), lookup)
	if err != nil || cfg.Server.ResolvedAPIToken != "env-tok" {
		t.Fatalf("token fallback = %q, %v", cfg.Server.ResolvedAPIToken, err)
	}

	path := filepath.Join(t.TempDir(), "options.json")
	if err := os.WriteFile(path, []byte(`{"config_file":"/config/netplane.yaml","api_token":"ha-tok","secrets":[{"name":"NP_TEST_SW_TOKEN","value":"s3cret"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TokenEnv, "")
	t.Setenv("NP_TEST_SW_TOKEN", "")
	if err := ApplyHAOptions(path); err != nil {
		t.Fatalf("ApplyHAOptions: %v", err)
	}
	if os.Getenv(TokenEnv) != "ha-tok" || os.Getenv("NP_TEST_SW_TOKEN") != "s3cret" {
		t.Fatalf("env = %q / %q", os.Getenv(TokenEnv), os.Getenv("NP_TEST_SW_TOKEN"))
	}
	if err := os.WriteFile(path, []byte(`{"secrets":[{"name":"","value":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyHAOptions(path); err == nil {
		t.Fatal("expected error for empty secret name")
	}
}
