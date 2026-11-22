// SPDX-License-Identifier: AGPL-3.0-or-later
package driver

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewHTTPClientRefusesCrossHostRedirect(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		if r.Header.Get("X-API-Key") != "" {
			t.Errorf("other host received X-API-Key")
		}
	}))
	defer other.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		if r.URL.Path == "/ok" {
			return
		}
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer target.Close()

	c := NewHTTPClient(TargetConfig{}, 5*time.Second)
	defer c.CloseIdleConnections()

	req, _ := http.NewRequest(http.MethodGet, target.URL+"/api", nil)
	req.Header.Set("X-API-Key", "secret")
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("expected cross-host redirect to be refused")
	}
	if n := otherHits.Load(); n != 0 {
		t.Fatalf("other host hit %d times", n)
	}

	resp, err := c.Get(target.URL + "/same")
	if err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/ok" {
		t.Fatalf("same-host redirect not followed: %s", resp.Request.URL.Path)
	}
}

func TestNewHTTPClientRefusesHTTPSDowngrade(t *testing.T) {
	c := NewHTTPClient(TargetConfig{}, time.Second)
	from, _ := http.NewRequest(http.MethodGet, "https://192.0.2.1/api", nil)
	to, _ := http.NewRequest(http.MethodGet, "http://192.0.2.1/api", nil)
	if err := c.CheckRedirect(to, []*http.Request{from}); err == nil {
		t.Fatal("expected https -> http redirect to be refused")
	}
}

func TestNewHTTPClientRefusesDowngradeAfterUpgrade(t *testing.T) {
	c := NewHTTPClient(TargetConfig{}, time.Second)
	first, _ := http.NewRequest(http.MethodGet, "http://192.0.2.1/", nil)
	second, _ := http.NewRequest(http.MethodGet, "https://192.0.2.1/login", nil)
	third, _ := http.NewRequest(http.MethodGet, "http://192.0.2.1/api", nil)
	if err := c.CheckRedirect(second, []*http.Request{first}); err != nil {
		t.Fatalf("http -> https upgrade refused: %v", err)
	}
	if err := c.CheckRedirect(third, []*http.Request{first, second}); err == nil {
		t.Fatal("expected https -> http hop to be refused")
	}
}
