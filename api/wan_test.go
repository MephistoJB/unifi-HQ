package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This fixture preserves the shape verified on the local gateway while using
// documentation-only addresses. It contains no credentials or actual public IP.
func wanFixture(t *testing.T) apiObject {
	t.Helper()
	var value apiObject
	if err := json.Unmarshal([]byte(`{
		"state":1,"last_seen":1795290576,
		"wan1":{"name":"eth8","up":true,"enable":true,"is_uplink":true,"ifname":"eth8","rx_bytes-r":210435,"tx_bytes-r":59937,"rx_rate":1683484,"tx_rate":479500,"ip":"192.168.178.2"},
		"wan2":{"name":"eth9","up":true,"enable":true,"is_uplink":false,"ifname":"eth9","rx_bytes-r":0,"tx_bytes-r":0},
		"active_geo_info":{"WAN":{"address":"198.51.100.23"}},
		"geo_info":{"WAN":{"address":"198.51.100.23"}},
		"last_wan_interfaces":{"WAN":{"ip":"192.168.178.2","alive":true},"WAN2":{"ip":"192.168.188.55","alive":false}}
	}`), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWANActivePublicIPAndByteRateConversion(t *testing.T) {
	value, sampledAt := normalizeWANTelemetry(wanFixture(t))
	if value.Status != "ok" || value.PublicIP == nil || *value.PublicIP != "198.51.100.23" {
		t.Fatalf("public address: %+v", value)
	}
	if value.ActiveInterface == nil || *value.ActiveInterface != "eth8" {
		t.Fatalf("active interface: %+v", value)
	}
	if value.UploadBps == nil || *value.UploadBps != 59937*8 || value.DownloadBps == nil || *value.DownloadBps != 210435*8 {
		t.Fatalf("bytes/sec must convert to bits/sec in correct directions: %+v", value)
	}
	if sampledAt.Unix() != 1795290576 {
		t.Fatalf("controller measurement time: %v", sampledAt)
	}
}

func TestWANMissingIsNotIdleAndPrivateIsNotPublic(t *testing.T) {
	device := wanFixture(t)
	wan := obj(device, "wan1")
	wan["tx_bytes-r"] = float64(0)
	delete(wan, "rx_bytes-r")
	obj(obj(device, "active_geo_info"), "WAN")["address"] = "192.168.178.2"
	value, _ := normalizeWANTelemetry(device)
	if value.UploadBps == nil || *value.UploadBps != 0 || value.DownloadBps != nil || value.PublicIP != nil || value.Status != "partial" {
		t.Fatalf("idle/missing/private: %+v", value)
	}
	wan["tx_bytes-r"] = "0"
	value, _ = normalizeWANTelemetry(device)
	if value.UploadBps != nil || value.Status != "unavailable" {
		t.Fatalf("numeric strings must not become measured rates: %+v", value)
	}
	for _, address := range []string{"100.64.0.1", "127.0.0.1", "169.254.0.1", "::1", "fd00::1", "not an address"} {
		if publicWANAddress(address) != nil {
			t.Errorf("non-public address accepted: %s", address)
		}
	}
	device = wanFixture(t)
	delete(device, "active_geo_info")
	value, _ = normalizeWANTelemetry(device)
	if value.PublicIP != nil {
		t.Fatal("geo cache or private interface must not substitute current public address")
	}
}

func TestWANMultipleActiveUplinksAreNotSilentlyAggregated(t *testing.T) {
	device := wanFixture(t)
	obj(device, "wan2")["is_uplink"] = true
	value, _ := normalizeWANTelemetry(device)
	if value.Status != "unavailable" || value.PublicIP != nil || value.UploadBps != nil || value.DownloadBps != nil {
		t.Fatalf("ambiguous active WANs: %+v", value)
	}
	obj(device, "wan1")["is_uplink"] = false
	obj(device, "active_geo_info")["WAN2"] = map[string]any{"address": "203.0.113.9"}
	obj(obj(device, "last_wan_interfaces"), "WAN2")["alive"] = true
	obj(obj(device, "last_wan_interfaces"), "WAN")["alive"] = false
	value, _ = normalizeWANTelemetry(device)
	if value.ActiveInterface == nil || *value.ActiveInterface != "eth9" || value.PublicIP == nil || *value.PublicIP != "203.0.113.9" {
		t.Fatalf("active backup WAN must not use dead primary address: %+v", value)
	}
}

func TestWANPublicAddressMustMatchSelectedInterface(t *testing.T) {
	device := wanFixture(t)
	obj(device, "active_geo_info")["WAN2"] = map[string]any{"address": "203.0.113.9"}
	obj(obj(device, "last_wan_interfaces"), "WAN2")["alive"] = true
	value, _ := normalizeWANTelemetry(device)
	if value.PublicIP == nil || *value.PublicIP != "198.51.100.23" {
		t.Fatalf("active primary must not use an alive backup's public address: %+v", value)
	}
	delete(obj(device, "active_geo_info"), "WAN")
	value, _ = normalizeWANTelemetry(device)
	if value.PublicIP != nil || value.Status != "partial" || value.UploadBps == nil {
		t.Fatalf("a mismatched single geo entry is not the selected WAN: %+v", value)
	}
}

func TestWANOldMeasurementRemainsStaleAfterSuccessfulHTTP(t *testing.T) {
	i := testInventory(t)
	w := newWANTelemetry(i, "default")
	value, sampledAt := normalizeWANTelemetry(wanFixture(t))
	w.value = value
	w.sampledAt = sampledAt
	w.lastSuccess = sampledAt.Add(3 * time.Minute)
	r := w.snapshot(w.lastSuccess)
	if !r.Stale || r.Status != "stale" || r.SampledAt == nil || *r.SampledAt == *r.LastSuccessAt {
		t.Fatalf("fetch time must not fake measurement freshness: %+v", r)
	}
	w.sampledAt = time.Time{}
	r = w.snapshot(w.lastSuccess)
	if r.Status != "unavailable" || !r.Stale {
		t.Fatal("missing sample time cannot be current")
	}
}

func TestWANPollUsesDiscoveredGatewayAndDoesNotAffectInventory(t *testing.T) {
	device := wanFixture(t)
	sampleTime := time.Unix(1795290576, 0)
	var responseCode atomic.Int32
	responseCode.Store(http.StatusOK)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/proxy/network/api/s/default/stat/device/02:00:00:00:00:01" || r.Header.Get("X-API-Key") != "secret-not-for-clients" {
			t.Errorf("unexpected telemetry request: method=%s path=%s", r.Method, r.URL.Path)
		}
		if code := int(responseCode.Load()); code != http.StatusOK {
			rw.WriteHeader(code)
			rw.Write([]byte("upstream body must not leak"))
			return
		}
		json.NewEncoder(rw).Encode(map[string]any{"meta": map[string]any{"rc": "ok"}, "data": []apiObject{device}})
	}))
	defer srv.Close()
	i := testInventory(t)
	i.cfg.URL = srv.URL
	i.client = srv.Client()
	i.applySuccess("site", []inventoryDevice{{ID: "discovered-gateway", Name: "Gateway", MAC: "02:00:00:00:00:01", Kind: "gateway", Status: "online"}}, nil, sampleTime)
	w := newWANTelemetry(i, "default")
	w.poll(context.Background(), sampleTime)
	r := w.snapshot(sampleTime)
	if r.Status != "ok" || r.Stale || r.UploadBps == nil {
		t.Fatalf("fresh telemetry: %+v", r)
	}
	responseCode.Store(http.StatusForbidden)
	w.poll(context.Background(), sampleTime.Add(time.Second))
	r = w.snapshot(sampleTime.Add(time.Second))
	if r.Status != "error" || !r.Stale || strings.Contains(r.Error, "upstream body") {
		t.Fatalf("sanitized telemetry failure: %+v", r)
	}
	if state := i.snapshot(sampleTime.Add(time.Second)); state.Stale || state.Sources.Controller.Status != "ok" || state.Counts.Online != 1 {
		t.Fatalf("WAN failure changed inventory state: %+v", state)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), i.cfg.Key) || strings.Contains(string(encoded), srv.URL) {
		t.Fatal("private origin or key leaked")
	}
}

func TestWANGatewayDiscoveryAndSiteValidation(t *testing.T) {
	i := testInventory(t)
	i.devices["switch"] = inventoryDevice{Kind: "switch", MAC: "02:00:00:00:00:02"}
	if _, err := i.telemetryGatewayMAC(); err == nil {
		t.Fatal("switch must not substitute a missing gateway")
	}
	i.devices["gateway"] = inventoryDevice{Kind: "gateway", MAC: "02:00:00:00:00:01"}
	if mac, err := i.telemetryGatewayMAC(); err != nil || mac != "02:00:00:00:00:01" {
		t.Fatalf("gateway discovery: %s %v", mac, err)
	}
	i.devices["second"] = inventoryDevice{Kind: "gateway", MAC: "02:00:00:00:00:03"}
	if _, err := i.telemetryGatewayMAC(); err == nil {
		t.Fatal("two distinct gateways require selection")
	}
	for _, site := range []string{"../default", "default/other", "default?secret=x", ""} {
		if newWANTelemetry(i, site).configErr == "" {
			t.Errorf("unsafe site accepted: %s", site)
		}
	}
}

func TestWANEndpointUsesExistingAuthentication(t *testing.T) {
	i := testInventory(t)
	i.cfg.URL = ""
	i.cfg.Key = ""
	hash, err := hashPassword("example long password", 10000)
	if err != nil {
		t.Fatal(err)
	}
	a := newAuth(hash, "session-secret")
	mux := http.NewServeMux()
	startWANTelemetry(mux, a, i)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/wan", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WAN=%d", w.Code)
	}
	req := httptest.NewRequest("GET", "/api/wan", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: a.newToken()})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var r wanResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Status != "unconfigured" || r.PublicIP != nil || r.UploadBps != nil || !r.Stale {
		t.Fatalf("unconfigured telemetry must not show idle: %s", w.Body.String())
	}
}
