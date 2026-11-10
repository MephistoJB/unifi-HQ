package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testInventory(t *testing.T) *inventory {
	t.Helper()
	i, err := newInventory(inventoryConfig{URL: "https://controller.invalid", BasePath: "/integration/v1", Site: "site", Key: "secret-not-for-clients", Poll: 15 * time.Second, Stale: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func TestInventoryMissingAndFailure(t *testing.T) {
	i := testInventory(t)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	d := inventoryDevice{ID: "site:client:aa", Name: "Quiet client", Kind: "client", Status: "online", ParentID: "site:device:switch"}
	i.applySuccess("site", []inventoryDevice{d}, nil, now)
	if r := i.snapshot(now); r.Counts.Online != 1 || r.Stale {
		t.Fatalf("fresh connected client: %+v", r)
	}
	i.applySuccess("site", nil, nil, now.Add(15*time.Second))
	if r := i.snapshot(now.Add(15 * time.Second)); r.Devices[0].Status != "unknown" || r.Devices[0].LastStatus != "online" || !r.Devices[0].HistoricalAttachment {
		t.Fatalf("first absence must be provisional: %+v", r.Devices)
	}
	i.applySuccess("site", nil, nil, now.Add(30*time.Second))
	r := i.snapshot(now.Add(30 * time.Second))
	if r.Devices[0].Status != "offline" || r.Devices[0].LastSeen != now.Format(time.RFC3339) {
		t.Fatalf("second confirmed absence: %+v", r.Devices)
	}
	i.lastErr = "Controller nicht erreichbar"
	r = i.snapshot(now.Add(31 * time.Second))
	if r.Devices[0].Status != "unknown" || r.Devices[0].LastStatus != "offline" || !r.Stale {
		t.Fatalf("failure is not offline: %+v", r)
	}
	i.applySuccess("site", []inventoryDevice{d}, nil, now.Add(45*time.Second))
	r = i.snapshot(now.Add(106 * time.Second))
	if r.Devices[0].Status != "unknown" || r.Sources.Controller.Status != "stale" {
		t.Fatalf("freshness expiry: %+v", r)
	}
}
func TestInventoryIdentityAndDocumentedTopology(t *testing.T) {
	now := time.Now()
	ds := []apiObject{{"id": "gateway", "name": "Gateway", "state": "ONLINE", "features": []any{"gateway", "switching"}}, {"id": "switch", "model": "USW-24", "name": "Switch", "state": "ONLINE", "uplink": map[string]any{"deviceId": "gateway"}}}
	cs := []apiObject{{"id": "one", "macAddress": "AA:00:00:00:00:01", "name": "<script>name</script>", "ipAddress": "192.0.2.4", "type": "WIRED", "uplinkDeviceId": "switch"}, {"id": "two", "macAddress": "aa:00:00:00:00:02", "name": "other", "ipAddress": "192.0.2.4", "type": "WIRELESS", "uplinkDeviceId": "switch"}, {"id": "one-again", "macAddress": "aa:00:00:00:00:01", "name": "duplicate"}}
	dsout, _, err := normalizeInventory("site", ds, cs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(dsout) != 4 {
		t.Fatalf("MAC dedup, not IP dedup: %d", len(dsout))
	}
	if dsout[0].Kind != "gateway" || dsout[1].ParentID != "site:device:gateway" || dsout[2].ParentID != "site:device:switch" {
		t.Fatalf("parents/kinds: %+v", dsout)
	}
	if dsout[2].Port != "" || dsout[2].LinkSpeedMbps != 0 {
		t.Fatal("undocumented ports and rates must remain unknown")
	}
	if dsout[2].Name != "<script>name</script>" {
		t.Fatal("names must be preserved as data, never treated as markup")
	}
	if dsout[2].ID == dsout[3].ID {
		t.Fatal("shared address must not merge identities")
	}
}
func TestInventoryPaginationAndFailureSanitization(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      func(int) string
		wantError bool
		want      int
	}{
		{"pages", func(off int) string {
			if off == 0 {
				return `{"count":1,"totalCount":2,"offset":0,"data":[{"id":"a"}]}`
			}
			return `{"count":1,"totalCount":2,"offset":1,"data":[{"id":"b"}]}`
		}, false, 2},
		{"incomplete", func(off int) string { return `{"count":0,"totalCount":2,"data":[]}` }, true, 0},
		{"duplicatePage", func(off int) string { return `{"count":1,"totalCount":2,"data":[{"id":"a"}]}` }, true, 0},
		{"changedTotal", func(off int) string {
			if off == 0 {
				return `{"count":1,"totalCount":2,"data":[{"id":"a"}]}`
			}
			return `{"count":1,"totalCount":3,"data":[{"id":"b"}]}`
		}, true, 0},
		{"missingMetadata", func(off int) string { return `{"data":[{"id":"a"}]}` }, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-API-Key") != "secret-not-for-clients" {
					t.Error("read-only authenticated API expected")
				}
				off := 0
				if r.URL.Query().Get("offset") == "1" {
					off = 1
				}
				w.Write([]byte(tc.body(off)))
			}))
			defer srv.Close()
			i := testInventory(t)
			i.cfg.URL = srv.URL
			i.client = srv.Client()
			v, err := i.list(context.Background(), "/sites/site/clients")
			if (err != nil) != tc.wantError || len(v) != tc.want {
				t.Fatalf("records=%d err=%v", len(v), err)
			}
		})
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("secret-not-for-clients"))
	}))
	defer srv.Close()
	i := testInventory(t)
	i.cfg.URL = srv.URL
	i.client = srv.Client()
	var value any
	err := i.get(context.Background(), "/sites", &value)
	if err == nil || strings.Contains(err.Error(), i.cfg.Key) {
		t.Fatal("error body or credentials leaked")
	}
}
func TestInventoryPartialPollCannotDisconnect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/devices") {
			w.Write([]byte(`{"count":0,"totalCount":0,"data":[]}`))
			return
		}
		w.WriteHeader(503)
	}))
	defer srv.Close()
	i := testInventory(t)
	i.cfg.URL = srv.URL
	i.client = srv.Client()
	now := time.Now()
	i.applySuccess("site", []inventoryDevice{{ID: "site:client:known", Kind: "client", Status: "online"}}, nil, now)
	for range 3 {
		if err := i.poll(context.Background(), now.Add(time.Second)); err == nil {
			t.Fatal("expected failed client collection")
		}
	}
	r := i.snapshot(now.Add(time.Second))
	if r.Devices[0].Status != "unknown" || r.Devices[0].LastStatus != "online" || i.missing["site:client:known"] != 0 {
		t.Fatalf("partial collection fabricated offline: %+v", r)
	}
}
func TestInventorySnapshotRoundTripAndSecretIsolation(t *testing.T) {
	i := testInventory(t)
	i.cfg.File = filepath.Join(t.TempDir(), "state", "inventory.json")
	now := time.Now().UTC()
	i.applySuccess("site", []inventoryDevice{{ID: "site:client:a", Name: "name", Kind: "client", Status: "online", ParentID: "switch"}}, nil, now)
	if err := i.persist(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(i.cfg.File)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), i.cfg.Key) {
		t.Fatal("secret in state")
	}
	info, _ := os.Stat(i.cfg.File)
	if info.Mode().Perm() != 0600 {
		t.Fatal("snapshot permissions")
	}
	j, err := newInventory(i.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := j.snapshot(now)
	if len(r.Devices) != 1 || r.Devices[0].Status != "unknown" {
		t.Fatalf("restart snapshot: %+v", r)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), i.cfg.Key) || strings.Contains(string(encoded), i.cfg.URL) {
		t.Fatal("private configuration in endpoint")
	}
}
func TestInventoryRuntimeSecretsAndAuthGuard(t *testing.T) {
	t.Setenv("UNIFI_URL", "")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_API_KEY_FILE", "")
	t.Setenv("INVENTORY_FILE", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("UNIFI_POLL_SECONDS", "15")
	t.Setenv("UNIFI_STALE_SECONDS", "60")
	hash, err := hashPassword("example long password", 10000)
	if err != nil {
		t.Fatal(err)
	}
	a := newAuth(hash, "session-secret")
	mux := http.NewServeMux()
	startInventory(mux, a)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/inventory", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inventory=%d", w.Code)
	}
	req := httptest.NewRequest("GET", "/api/inventory", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: a.newToken()})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("configured state: %s", w.Body.String())
	}
	t.Setenv("UNIFI_API_KEY_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err = secretValue("UNIFI_API_KEY"); err == nil {
		t.Fatal("missing secret file must not fall back")
	}
}
func TestInventoryRejectsUnsafeConfig(t *testing.T) {
	t.Setenv("UNIFI_API_KEY_FILE", "")
	t.Setenv("UNIFI_API_KEY", "some-key")
	t.Setenv("UNIFI_POLL_SECONDS", "15")
	t.Setenv("UNIFI_STALE_SECONDS", "60")
	for _, raw := range []string{"http://controller", "https://user:password@controller", "https://controller?token=bad"} {
		t.Setenv("UNIFI_URL", raw)
		if _, err := inventoryConfigFromEnv(); err == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestInventoryRejectsIncompleteLocalClientsAndCycles(t *testing.T) {
	_, _, err := normalizeInventory("site", nil, []apiObject{{"id": "a", "type": "WIRELESS", "name": "missing required mac and parent"}}, time.Now())
	if err == nil {
		t.Fatal("incomplete documented local-client response accepted")
	}
	ds, limits, err := normalizeInventory("site", []apiObject{{"id": "a", "uplink": map[string]any{"deviceId": "b"}}, {"id": "b", "uplink": map[string]any{"deviceId": "a"}}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ds[0].ParentID != "" || ds[1].ParentID != "" || len(limits) < 2 {
		t.Fatalf("cyclic topology not detached: %+v", ds)
	}
	if deviceKind(apiObject{"features": []any{"switching", "accessPoint"}}) != "ap" || deviceKind(apiObject{"model": "unknown future hardware"}) != "device" {
		t.Fatal("device classification must prioritize AP feature and avoid fictitious switch fallback")
	}
}

func TestInventoryFullOfficialPollUsesOverviewClientAttachment(t *testing.T) {
	var paths []string
	var pathsMu sync.Mutex
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.URL.Path)
		pathsMu.Unlock()
		switch r.URL.Path {
		case "/integration/v1/sites/site/devices":
			w.Write([]byte(`{"count":2,"totalCount":2,"data":[{"id":"gw","name":"Gateway","state":"ONLINE","features":["gateway","switching"]},{"id":"sw","name":"Switch","state":"ONLINE","features":["switching"]}]}`))
		case "/integration/v1/sites/site/devices/gw":
			w.Write([]byte(`{"id":"gw","name":"Gateway","state":"ONLINE","features":{"switching":{}}}`))
		case "/integration/v1/sites/site/devices/sw":
			w.Write([]byte(`{"id":"sw","name":"Switch","state":"ONLINE","features":{"switching":{}},"uplink":{"deviceId":"gw"}}`))
		case "/integration/v1/sites/site/clients":
			w.Write([]byte(`{"count":1,"totalCount":1,"data":[{"id":"c1","name":"Client","macAddress":"AA:00:00:00:00:01","uplinkDeviceId":"sw","type":"WIRED","ipAddress":"192.0.2.1"}]}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	i := testInventory(t)
	i.cfg.URL = srv.URL
	i.client = srv.Client()
	now := time.Now()
	if err := i.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	r := i.snapshot(now)
	if r.Counts.Online != 3 || r.Counts.Clients != 1 || r.Counts.Infrastructure != 2 {
		t.Fatalf("unexpected inventory: %+v", r)
	}
	found := false
	for _, d := range r.Devices {
		if d.Kind == "client" {
			found = true
			if d.ID != "site:client:aa:00:00:00:00:01" || d.ParentID != "site:device:sw" || d.ConnectionType != "wired" {
				t.Fatalf("client overview identity/attachment: %+v", d)
			}
		}
		if d.ID == "site:device:gw" && d.Kind != "gateway" {
			t.Fatal("detail object must not erase overview gateway feature")
		}
	}
	if !found {
		t.Fatal("no client")
	}
	if len(paths) != 4 {
		t.Fatalf("unnecessary fan-out requests: %v", paths)
	}
}
