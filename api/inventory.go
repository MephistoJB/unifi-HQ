// UniFi HQ extension, 2026-11-10: read-only official local Network API inventory.
// The controller key never leaves this process. Failed or incomplete collections
// cannot declare clients offline. An IP address is never used as device identity.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type inventoryDevice struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	MAC                  string   `json:"mac,omitempty"`
	IP                   string   `json:"ip,omitempty"`
	Kind                 string   `json:"kind"`
	Status               string   `json:"status"`
	LastStatus           string   `json:"last_status,omitempty"`
	ParentID             string   `json:"parent_id,omitempty"`
	Port                 string   `json:"port,omitempty"`
	LinkSpeedMbps        float64  `json:"link_speed_mbps,omitempty"`
	ConnectionType       string   `json:"connection_type"`
	LastSeen             string   `json:"last_seen,omitempty"`
	Model                string   `json:"model,omitempty"`
	Network              string   `json:"network,omitempty"`
	Addresses            []string `json:"addresses"`
	HistoricalAttachment bool     `json:"historical_attachment"`
	ControllerID         string   `json:"-"`
}

type inventorySource struct {
	Status        string `json:"status"`
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	Error         string `json:"error,omitempty"`
}
type topologySource struct {
	Status      string   `json:"status"`
	Limitations []string `json:"limitations"`
}
type inventorySources struct {
	Controller inventorySource `json:"controller"`
	Topology   topologySource  `json:"topology"`
}
type inventoryCounts struct {
	Online         int `json:"online"`
	Offline        int `json:"offline"`
	Unknown        int `json:"unknown"`
	Infrastructure int `json:"infrastructure"`
	Clients        int `json:"clients"`
}
type inventoryResponse struct {
	Configured          bool              `json:"configured"`
	GeneratedAt         string            `json:"generated_at"`
	LastSuccessAt       string            `json:"last_success_at,omitempty"`
	Stale               bool              `json:"stale"`
	Error               string            `json:"error,omitempty"`
	Devices             []inventoryDevice `json:"devices"`
	Sources             inventorySources  `json:"sources"`
	Counts              inventoryCounts   `json:"counts"`
	PollIntervalSeconds int               `json:"poll_interval_seconds"`
	StaleAfterSeconds   int               `json:"stale_after_seconds"`
}
type inventoryDisk struct {
	Version       int               `json:"version"`
	Site          string            `json:"site"`
	LastSuccessAt time.Time         `json:"last_success_at"`
	Devices       []inventoryDevice `json:"devices"`
}
type inventoryConfig struct {
	URL      string
	BasePath string
	Site     string
	Key      string
	File     string
	CAFile   string
	Poll     time.Duration
	Stale    time.Duration
}
type inventory struct {
	mu          sync.RWMutex
	cfg         inventoryConfig
	client      *http.Client
	devices     map[string]inventoryDevice
	missing     map[string]int
	site        string
	lastSuccess time.Time
	lastAttempt time.Time
	lastErr     string
	configErr   string
	limitations []string
	persistErr  bool
	verified    bool
}

// secretValue supports read-only mounted runtime secrets; values are never logged.
func secretValue(name string) (string, error) {
	if file := os.Getenv(name + "_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("%s_FILE kann nicht gelesen werden", name)
		}
		if len(b) > 64*1024 {
			return "", fmt.Errorf("%s_FILE ist zu groß", name)
		}
		value := strings.TrimSpace(string(b))
		if value == "" {
			return "", fmt.Errorf("%s_FILE ist leer", name)
		}
		return value, nil
	}
	return os.Getenv(name), nil
}
func inventoryConfigFromEnv() (inventoryConfig, error) {
	c := inventoryConfig{URL: strings.TrimRight(os.Getenv("UNIFI_URL"), "/"), BasePath: env("UNIFI_API_BASE_PATH", "/proxy/network/integration/v1"), Site: os.Getenv("UNIFI_SITE_ID"), File: env("INVENTORY_FILE", "/state/inventory.json"), CAFile: os.Getenv("UNIFI_CA_FILE"), Poll: 15 * time.Second, Stale: 60 * time.Second}
	var err error
	c.Key, err = secretValue("UNIFI_API_KEY")
	if err != nil {
		return c, err
	}
	for _, v := range []struct {
		name     string
		target   *time.Duration
		min, max int
	}{{"UNIFI_POLL_SECONDS", &c.Poll, 10, 300}, {"UNIFI_STALE_SECONDS", &c.Stale, 30, 900}} {
		if raw := os.Getenv(v.name); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < v.min || n > v.max {
				return c, fmt.Errorf("%s außerhalb des erlaubten Bereichs", v.name)
			}
			*v.target = time.Duration(n) * time.Second
		}
	}
	if c.Stale < c.Poll*2 {
		return c, errors.New("UNIFI_STALE_SECONDS muss mindestens zwei Abfrageintervalle umfassen")
	}
	if c.URL == "" && c.Key == "" {
		return c, nil
	}
	if c.URL == "" || c.Key == "" {
		return c, errors.New("UNIFI_URL und ein UniFi-API-Schlüssel sind erforderlich")
	}
	u, e := url.Parse(c.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || (u.Path != "" && u.Path != "/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("UNIFI_URL muss eine HTTPS-Adresse ohne Zugangsdaten oder Abfrageparameter sein")
	}
	if !strings.HasPrefix(c.BasePath, "/") || strings.ContainsAny(c.BasePath, "?#") {
		return c, errors.New("UNIFI_API_BASE_PATH ist ungültig")
	}
	return c, nil
}
func newInventory(c inventoryConfig) (*inventory, error) {
	i := &inventory{cfg: c, site: c.Site, devices: map[string]inventoryDevice{}, missing: map[string]int{}, limitations: []string{}}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CAFile != "" {
		b, e := os.ReadFile(c.CAFile)
		if e != nil {
			return i, errors.New("UniFi-CA-Datei kann nicht gelesen werden")
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(b) {
			return i, errors.New("UniFi-CA-Datei enthält kein gültiges Zertifikat")
		}
		t.TLSClientConfig.RootCAs = pool
	}
	i.client = &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if c.File != "" {
		b, e := os.ReadFile(c.File)
		if e == nil {
			var disk inventoryDisk
			if json.Unmarshal(b, &disk) != nil || disk.Version != 1 {
				return i, errors.New("Inventar-Snapshot ist ungültig")
			}
			if c.Site == "" || disk.Site == c.Site {
				i.site = disk.Site
				i.lastSuccess = disk.LastSuccessAt
				for _, d := range disk.Devices {
					if d.ID != "" {
						i.devices[d.ID] = d
					}
				}
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return i, errors.New("Inventar-Snapshot kann nicht gelesen werden")
		}
	}
	return i, nil
}
func startInventory(mux *http.ServeMux, auth *authCfg) {
	c, err := inventoryConfigFromEnv()
	i, e := newInventory(c)
	if err == nil {
		err = e
	}
	if err != nil {
		i.configErr = err.Error()
		log.Printf("UniFi-Inventar: %s", i.configErr)
	}
	mux.HandleFunc("GET /api/inventory", auth.require(func(w http.ResponseWriter, r *http.Request) { jsonOut(w, http.StatusOK, i.snapshot(time.Now())) }))
	if err == nil && c.URL != "" && c.Key != "" {
		go i.run(context.Background())
	}
}
func (i *inventory) run(ctx context.Context) {
	for {
		pollCtx, cancel := context.WithTimeout(ctx, min(45*time.Second, i.cfg.Poll-time.Second))
		err := i.poll(pollCtx, time.Now())
		cancel()
		if err != nil {
			log.Printf("UniFi-Inventar: %s", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(i.cfg.Poll):
		}
	}
}

type apiObject map[string]any

func (i *inventory) get(ctx context.Context, p string, out any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, i.cfg.URL+strings.TrimRight(i.cfg.BasePath, "/")+p, nil)
	if err != nil {
		return errors.New("UniFi-Anfrage kann nicht erstellt werden")
	}
	r.Header.Set("X-API-Key", i.cfg.Key)
	r.Header.Set("Accept", "application/json")
	resp, err := i.client.Do(r)
	if err != nil {
		return errors.New("UniFi-Controller nicht erreichbar oder TLS-Prüfung fehlgeschlagen")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("UniFi-API antwortet mit HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024))
	if err := dec.Decode(out); err != nil {
		return errors.New("UniFi-API liefert keine gültige JSON-Antwort")
	}
	return nil
}
func (i *inventory) list(ctx context.Context, p string) ([]apiObject, error) {
	all := []apiObject{}
	expected := -1
	seen := map[string]bool{}
	for offset := 0; offset < 10000; {
		var page struct {
			Data       []apiObject `json:"data"`
			TotalCount *int        `json:"totalCount"`
			Count      *int        `json:"count"`
			Offset     *int        `json:"offset"`
		}
		if err := i.get(ctx, fmt.Sprintf("%s?offset=%d&limit=200", p, offset), &page); err != nil {
			return nil, err
		}
		if page.TotalCount == nil || page.Data == nil {
			return nil, errors.New("UniFi-API-Seitenangaben fehlen")
		}
		if expected < 0 {
			expected = *page.TotalCount
		} else if expected != *page.TotalCount {
			return nil, errors.New("UniFi-Inventar änderte sich während der Abfrage; erneuter Versuch folgt")
		}
		if page.Offset != nil && *page.Offset != offset {
			return nil, errors.New("UniFi-API lieferte eine unerwartete Seite")
		}
		if page.Count != nil && *page.Count != len(page.Data) {
			return nil, errors.New("UniFi-API lieferte eine unvollständige Seite")
		}
		for _, o := range page.Data {
			id := str(o, "id")
			if id == "" {
				return nil, errors.New("UniFi-Datensatz ohne stabile ID")
			}
			if seen[id] {
				return nil, errors.New("UniFi-API lieferte doppelte Seiten oder IDs")
			}
			seen[id] = true
			all = append(all, o)
		}
		offset += len(page.Data)
		if offset == expected {
			return all, nil
		}
		if offset > expected || len(page.Data) == 0 {
			return nil, errors.New("UniFi-API-Paginierung ist unvollständig")
		}
	}
	return nil, errors.New("UniFi-Inventar überschreitet die Sicherheitsgrenze von 10000 Einträgen")
}
func (i *inventory) details(ctx context.Context, p string, objects []apiObject) ([]apiObject, error) {
	out := make([]apiObject, len(objects))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	sem := make(chan struct{}, 6)
	for idx, o := range objects {
		wg.Add(1)
		go func(idx int, o apiObject) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if first == nil {
					first = errors.New("UniFi-Abfragezeit überschritten")
				}
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			d := apiObject{}
			err := i.get(ctx, p+"/"+url.PathEscape(str(o, "id")), &d)
			if err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
				return
			}
			if str(d, "id") != str(o, "id") {
				mu.Lock()
				if first == nil {
					first = errors.New("UniFi-Gerätedetail hat eine unerwartete ID")
				}
				mu.Unlock()
				return
			}
			d["_overviewFeatures"] = o["features"]
			for k, v := range o {
				if _, ok := d[k]; !ok {
					d[k] = v
				}
			}
			out[idx] = d
		}(idx, o)
	}
	wg.Wait()
	return out, first
}
func (i *inventory) poll(ctx context.Context, now time.Time) (err error) {
	i.mu.Lock()
	i.lastAttempt = now
	i.mu.Unlock()
	defer func() {
		if err != nil {
			i.mu.Lock()
			i.lastErr = err.Error()
			i.missing = map[string]int{}
			i.mu.Unlock()
		}
	}()
	site := i.site
	if site == "" {
		sites, e := i.list(ctx, "/sites")
		if e != nil {
			return e
		}
		if len(sites) != 1 {
			return errors.New("UNIFI_SITE_ID erforderlich: Controller enthält nicht genau eine Site")
		}
		site = str(sites[0], "id")
	}
	base := "/sites/" + url.PathEscape(site)
	ds, e := i.list(ctx, base+"/devices")
	if e != nil {
		return e
	}
	cs, e := i.list(ctx, base+"/clients")
	if e != nil {
		return e
	}
	ds, e = i.details(ctx, base+"/devices", ds)
	if e != nil {
		return e
	}
	devices, limitations, e := normalizeInventory(site, ds, cs, now)
	if e != nil {
		return e
	}
	i.applySuccess(site, devices, limitations, now)
	if e = i.persist(); e != nil {
		i.mu.Lock()
		i.persistErr = true
		i.mu.Unlock()
		log.Print("UniFi-Inventar: Snapshot konnte nicht gespeichert werden")
	} else {
		i.mu.Lock()
		i.persistErr = false
		i.mu.Unlock()
	}
	return nil
}

func (i *inventory) applySuccess(site string, observed []inventoryDevice, limitations []string, now time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	seen := map[string]bool{}
	for _, d := range observed {
		seen[d.ID] = true
		i.missing[d.ID] = 0
		old, ok := i.devices[d.ID]
		if d.Status == "online" {
			d.LastSeen = now.UTC().Format(time.RFC3339)
		} else if ok {
			d.LastSeen = old.LastSeen
		}
		d.LastStatus = d.Status
		i.devices[d.ID] = d
	}
	for id, d := range i.devices {
		if seen[id] {
			continue
		}
		i.missing[id]++
		if i.missing[id] >= 2 {
			d.Status = "offline"
			d.LastStatus = "offline"
		} else {
			d.LastStatus = d.Status
			d.Status = "unknown"
		}
		d.HistoricalAttachment = true
		i.devices[id] = d
	}
	i.site = site
	i.lastSuccess = now
	i.lastErr = ""
	i.limitations = limitations
	i.verified = true
}
func (i *inventory) snapshot(now time.Time) inventoryResponse {
	i.mu.RLock()
	defer i.mu.RUnlock()
	r := inventoryResponse{Configured: i.cfg.URL != "" && i.cfg.Key != "" && i.configErr == "", GeneratedAt: now.UTC().Format(time.RFC3339), Devices: []inventoryDevice{}, PollIntervalSeconds: int(i.cfg.Poll / time.Second), StaleAfterSeconds: int(i.cfg.Stale / time.Second)}
	state := "ok"
	r.Error = i.lastErr
	if !i.lastSuccess.IsZero() {
		r.LastSuccessAt = i.lastSuccess.UTC().Format(time.RFC3339)
	}
	switch {
	case i.configErr != "":
		state = "error"
		r.Error = i.configErr
	case !r.Configured:
		state = "unconfigured"
	case i.lastErr != "":
		state = "error"
	case i.lastSuccess.IsZero() || !i.verified:
		state = "loading"
	case now.Sub(i.lastSuccess) > i.cfg.Stale:
		state = "stale"
	}
	r.Stale = state != "ok"
	r.Sources.Controller = inventorySource{Status: state, LastSuccessAt: r.LastSuccessAt, Error: r.Error}
	if !i.lastAttempt.IsZero() {
		r.Sources.Controller.LastAttemptAt = i.lastAttempt.UTC().Format(time.RFC3339)
	}
	limitations := append([]string{}, i.limitations...)
	limitations = append(limitations, "Offline-Clients werden ab Beginn der eigenen Erfassung gespeichert; ältere Controller-Historie wird nicht importiert.")
	if i.persistErr {
		limitations = append(limitations, "Inventar-Snapshot kann nicht gespeichert werden; Neustart verliert möglicherweise aktuelle Zuordnungen.")
	}
	r.Sources.Topology = topologySource{Status: "partial", Limitations: limitations}
	for _, d := range i.devices {
		if r.Stale {
			if d.Status != "unknown" {
				d.LastStatus = d.Status
			}
			d.Status = "unknown"
			d.HistoricalAttachment = true
		}
		if d.Addresses == nil {
			d.Addresses = []string{}
		}
		r.Devices = append(r.Devices, d)
		switch d.Status {
		case "online":
			r.Counts.Online++
		case "offline":
			r.Counts.Offline++
		default:
			r.Counts.Unknown++
		}
		if d.Kind == "client" {
			r.Counts.Clients++
		} else {
			r.Counts.Infrastructure++
		}
	}
	sort.Slice(r.Devices, func(a, b int) bool {
		if r.Devices[a].Kind != r.Devices[b].Kind {
			return r.Devices[a].Kind < r.Devices[b].Kind
		}
		return strings.ToLower(r.Devices[a].Name) < strings.ToLower(r.Devices[b].Name)
	})
	return r
}
func (i *inventory) persist() error {
	if i.cfg.File == "" {
		return nil
	}
	i.mu.RLock()
	disk := inventoryDisk{Version: 1, Site: i.site, LastSuccessAt: i.lastSuccess, Devices: []inventoryDevice{}}
	for _, d := range i.devices {
		disk.Devices = append(disk.Devices, d)
	}
	i.mu.RUnlock()
	b, err := json.Marshal(disk)
	if err != nil {
		return err
	}
	dir := filepath.Dir(i.cfg.File)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".inventory-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), i.cfg.File)
}

func str(o apiObject, k string) string    { v, _ := o[k].(string); return v }
func obj(o apiObject, k string) apiObject { v, _ := o[k].(map[string]any); return apiObject(v) }
func num(o apiObject, k string) float64   { v, _ := o[k].(float64); return v }
func pick(o apiObject, keys ...string) string {
	for _, k := range keys {
		if s := str(o, k); s != "" {
			return s
		}
	}
	return ""
}
func numberString(o apiObject, k string) string {
	if s := str(o, k); s != "" {
		return s
	}
	if v, ok := o[k].(float64); ok {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func normalizeInventory(site string, ds, cs []apiObject, now time.Time) ([]inventoryDevice, []string, error) {
	out := []inventoryDevice{}
	limits := []string{"Die offizielle API liefert keine verlässlichen Client-Portnummern, VLAN-Zuordnungen oder Uplink-Linkraten."}
	ids := map[string]string{}
	macIDs := map[string]string{}
	seen := map[string]bool{}
	for _, o := range ds {
		cid := str(o, "id")
		if cid == "" {
			return nil, nil, errors.New("UniFi-Gerät ohne stabile ID")
		}
		id := site + ":device:" + cid
		ids[cid] = id
		mac := strings.ToLower(str(o, "macAddress"))
		if mac != "" {
			macIDs[mac] = id
		}
	}
	for _, o := range ds {
		cid := str(o, "id")
		d := inventoryDevice{ID: ids[cid], ControllerID: cid, Name: pick(o, "name", "model", "id"), MAC: strings.ToLower(str(o, "macAddress")), IP: str(o, "ipAddress"), Model: str(o, "model"), Kind: deviceKind(o), Status: deviceStatus(str(o, "state")), ConnectionType: "unknown", Addresses: []string{}}
		if d.IP != "" {
			d.Addresses = append(d.Addresses, d.IP)
		}
		u := obj(o, "uplink")
		parent := pick(u, "deviceId", "uplinkDeviceId")
		if parent == "" {
			parent = str(o, "uplinkDeviceId")
		}
		d.ParentID = ids[parent]
		if d.ParentID == "" {
			d.ParentID = macIDs[strings.ToLower(str(u, "macAddress"))]
		}
		d.Port = numberString(u, "portIdx")
		if d.Port == "" {
			d.Port = numberString(u, "portIndex")
		}
		d.LinkSpeedMbps = num(u, "speedMbps")
		if d.LinkSpeedMbps == 0 {
			d.LinkSpeedMbps = num(u, "linkSpeedMbps")
		}
		if d.ParentID != "" {
			if strings.EqualFold(str(u, "type"), "WIRELESS") {
				d.ConnectionType = "wireless"
			} else if strings.EqualFold(str(u, "type"), "WIRED") {
				d.ConnectionType = "wired"
			}
		}
		if d.ParentID == "" && d.Kind != "gateway" {
			limits = append(limits, "Nicht alle Infrastruktur-Uplinks werden von der offiziellen API geliefert.")
		}
		out = append(out, d)
	}
	for _, o := range cs {
		cid := str(o, "id")
		mac := strings.ToLower(str(o, "macAddress"))
		if cid == "" {
			return nil, nil, errors.New("UniFi-Client ohne stabile ID")
		}
		id := site + ":client:" + cid
		if mac != "" {
			id = site + ":client:" + mac
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if (str(o, "type") == "WIRED" || str(o, "type") == "WIRELESS") && (mac == "" || str(o, "uplinkDeviceId") == "") {
			return nil, nil, errors.New("UniFi-Client-Antwort unvollständig: MAC oder Anschluss-ID fehlt")
		}
		d := inventoryDevice{ID: id, ControllerID: cid, Name: pick(o, "name", "hostname", "macAddress", "id"), MAC: mac, IP: str(o, "ipAddress"), Kind: "client", Status: "online", ConnectionType: "unknown", Addresses: []string{}}
		if d.IP != "" {
			d.Addresses = append(d.Addresses, d.IP)
		}
		d.ParentID = ids[str(o, "uplinkDeviceId")]
		d.Port = numberString(o, "uplinkPortIdx")
		d.Network = pick(o, "networkName", "networkId")
		switch strings.ToUpper(str(o, "type")) {
		case "WIRED":
			d.ConnectionType = "wired"
		case "WIRELESS":
			d.ConnectionType = "wireless"
		}
		if d.ParentID == "" {
			limits = append(limits, "Nicht alle Client-Anschlüsse werden von der offiziellen API geliefert.")
		}
		out = append(out, d)
	}
	// A bad parent graph must not create an endless tree or fabricated link.
	parents := map[string]string{}
	for _, d := range out {
		parents[d.ID] = d.ParentID
	}
	for index, d := range out {
		chain := map[string]bool{d.ID: true}
		parent := d.ParentID
		for parent != "" {
			if chain[parent] {
				out[index].ParentID = ""
				limits = append(limits, "Widersprüchliche zyklische Anschlüsse werden nicht als Topologie dargestellt.")
				break
			}
			chain[parent] = true
			parent = parents[parent]
		}
	}
	uniq := []string{}
	for _, s := range limits {
		found := false
		for _, u := range uniq {
			if s == u {
				found = true
			}
		}
		if !found {
			uniq = append(uniq, s)
		}
	}
	return out, uniq, nil
}
func deviceStatus(s string) string {
	switch strings.ToUpper(s) {
	case "ONLINE", "CONNECTED":
		return "online"
	case "OFFLINE", "DISCONNECTED":
		return "offline"
	default:
		return "unknown"
	}
}
func deviceKind(o apiObject) string {
	features := map[string]bool{}
	for _, k := range []string{"_overviewFeatures", "features", "capabilities"} {
		if a, ok := o[k].([]any); ok {
			for _, v := range a {
				features[strings.ToUpper(fmt.Sprint(v))] = true
			}
		}
	}
	// Gateways with integrated switching/Wi-Fi can expose only those features.
	// Their known product model must take priority over the integrated switch.
	m := strings.ToUpper(strings.TrimSpace(str(o, "model")))
	if features["GATEWAY"] || features["ROUTING"] || knownGatewayModel(m) {
		return "gateway"
	}
	if features["ACCESS_POINT"] || features["ACCESSPOINT"] || features["WIRELESS"] {
		return "ap"
	}
	if features["SWITCHING"] || features["SWITCH"] {
		return "switch"
	}
	switch {
	case strings.HasPrefix(m, "USW"), strings.HasPrefix(m, "USMINI"), strings.HasPrefix(m, "USF"), strings.HasPrefix(m, "USL"):
		return "switch"
	case strings.HasPrefix(m, "UAP"), strings.HasPrefix(m, "U6"), strings.HasPrefix(m, "U7"), strings.HasPrefix(m, "UAL"):
		return "ap"
	}
	return "device"
}

func knownGatewayModel(model string) bool {
	// Accept known product identifiers as well as the human-readable model
	// returned by newer official APIs. Never use the user-editable device name.
	for _, prefix := range []string{"UDM", "UCG", "UXG", "USG", "UDR", "UDW", "EFG"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	if model == "UX" || model == "UX7" {
		return true
	}
	words := strings.Join(strings.FieldsFunc(model, func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == ' ' || r == '\t'
	}), " ")
	for _, family := range []string{"DREAM MACHINE", "DREAM ROUTER", "DREAM WALL", "CLOUD GATEWAY", "SECURITY GATEWAY", "FORTRESS GATEWAY"} {
		// Word boundaries prevent accidentally matching fragments of unknown
		// model identifiers while allowing the normal UniFi brand prefix.
		if strings.Contains(" "+words+" ", " "+family+" ") {
			return true
		}
	}
	return false
}
