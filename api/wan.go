// UniFi HQ extension, 2026-11-21. Approved read-only classic local API telemetry.
// Uses the same local origin, verified TLS and API key as the official inventory.
// WAN failures never change inventory state, and current rates are never inferred
// from exported NetFlow batches or absent fields.
package main

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

type wanResponse struct {
	Configured          bool     `json:"configured"`
	Status              string   `json:"status"`
	Stale               bool     `json:"stale"`
	PublicIP            *string  `json:"public_ip"`
	UploadBps           *float64 `json:"upload_bps"`
	DownloadBps         *float64 `json:"download_bps"`
	SampledAt           *string  `json:"sampled_at"`
	LastSuccessAt       *string  `json:"last_success_at"`
	LastAttemptAt       *string  `json:"last_attempt_at"`
	ActiveInterface     *string  `json:"active_interface"`
	Source              string   `json:"source"`
	Error               string   `json:"error,omitempty"`
	PollIntervalSeconds int      `json:"poll_interval_seconds"`
	StaleAfterSeconds   int      `json:"stale_after_seconds"`
}

type wanTelemetry struct {
	mu          sync.RWMutex
	inventory   *inventory
	legacySite  string
	configErr   string
	value       wanResponse
	sampledAt   time.Time
	lastSuccess time.Time
	lastAttempt time.Time
	lastErr     string
}

func newWANTelemetry(i *inventory, legacySite string) *wanTelemetry {
	w := &wanTelemetry{inventory: i, legacySite: legacySite, value: wanResponse{Status: "loading"}}
	if legacySite == "" || len(legacySite) > 64 || strings.IndexFunc(legacySite, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) >= 0 {
		w.configErr = "UNIFI_LEGACY_SITE muss ein gültiger klassischer Site-Kurzname sein"
	}
	return w
}

func startWANTelemetry(mux *http.ServeMux, auth *authCfg, i *inventory) {
	w := newWANTelemetry(i, env("UNIFI_LEGACY_SITE", "default"))
	mux.HandleFunc("GET /api/wan", auth.require(func(rw http.ResponseWriter, r *http.Request) { jsonOut(rw, http.StatusOK, w.snapshot(time.Now())) }))
	if i.configErr == "" && w.configErr == "" && i.cfg.URL != "" && i.cfg.Key != "" && i.client != nil {
		go w.run(context.Background())
	}
}

func (w *wanTelemetry) run(ctx context.Context) {
	for {
		pollCtx, cancel := context.WithTimeout(ctx, min(10*time.Second, w.inventory.cfg.Poll-time.Second))
		w.poll(pollCtx, time.Now())
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.inventory.cfg.Poll):
		}
	}
}

func (i *inventory) telemetryGatewayMAC() (string, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	macs := map[string]bool{}
	for _, device := range i.devices {
		if device.Kind != "gateway" || device.MAC == "" {
			continue
		}
		mac, err := net.ParseMAC(device.MAC)
		if err != nil || len(mac) != 6 {
			continue
		}
		macs[mac.String()] = true
	}
	if len(macs) == 0 {
		return "", errors.New("Noch kein Gateway aus dem Controller-Inventar verfügbar")
	}
	if len(macs) != 1 {
		return "", errors.New("Mehrere Gateways im Inventar; WAN-Daten sind nicht eindeutig zuordenbar")
	}
	for mac := range macs {
		return mac, nil
	}
	return "", errors.New("Gateway nicht verfügbar")
}

func (w *wanTelemetry) poll(ctx context.Context, now time.Time) {
	w.mu.Lock()
	w.lastAttempt = now
	w.mu.Unlock()
	mac, err := w.inventory.telemetryGatewayMAC()
	if err != nil {
		w.setError(err.Error(), "unavailable")
		return
	}
	var response struct {
		Meta struct {
			RC string `json:"rc"`
		} `json:"meta"`
		Data []apiObject `json:"data"`
	}
	path := "/proxy/network/api/s/" + url.PathEscape(w.legacySite) + "/stat/device/" + url.PathEscape(mac)
	if err = w.inventory.getPath(ctx, path, &response); err != nil {
		w.setError("WAN-Daten konnten nicht vom lokalen UniFi-Controller gelesen werden", "error")
		return
	}
	if response.Meta.RC != "ok" || len(response.Data) != 1 {
		w.setError("UniFi liefert keine eindeutig zuordenbaren Gateway-Daten", "unavailable")
		return
	}
	value, sampledAt := normalizeWANTelemetry(response.Data[0])
	w.mu.Lock()
	w.value = value
	w.sampledAt = sampledAt
	w.lastSuccess = now
	w.lastErr = ""
	w.mu.Unlock()
}

func (w *wanTelemetry) setError(message, status string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastErr = message
	w.value.Status = status
}

func stringTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	value := t.UTC().Format(time.RFC3339)
	return &value
}

func (w *wanTelemetry) snapshot(now time.Time) wanResponse {
	w.mu.RLock()
	defer w.mu.RUnlock()
	r := w.value
	i := w.inventory
	r.Configured = i.cfg.URL != "" && i.cfg.Key != "" && i.configErr == "" && w.configErr == ""
	r.Source = "unifi_local_classic"
	r.PollIntervalSeconds = int(i.cfg.Poll / time.Second)
	r.StaleAfterSeconds = int(i.cfg.Stale / time.Second)
	r.LastSuccessAt = stringTime(w.lastSuccess)
	r.LastAttemptAt = stringTime(w.lastAttempt)
	r.SampledAt = stringTime(w.sampledAt)
	if w.lastErr != "" {
		r.Error = w.lastErr
	}
	switch {
	case w.configErr != "":
		r.Status = "error"
		r.Error = w.configErr
	case i.configErr != "":
		r.Status = "error"
		r.Error = i.configErr
	case !r.Configured:
		r.Status = "unconfigured"
	case w.lastErr != "": // Keep the specific error or unavailable status set above.
	case w.lastSuccess.IsZero():
		r.Status = "loading"
	case w.sampledAt.IsZero():
		r.Status = "unavailable"
		r.Error = "Gateway liefert keinen verlässlichen Messzeitpunkt"
	case now.Sub(w.sampledAt) > i.cfg.Stale || w.sampledAt.After(now.Add(30*time.Second)) || now.Sub(w.lastSuccess) > i.cfg.Stale:
		r.Status = "stale"
	}
	r.Stale = r.Status != "ok" && r.Status != "partial"
	return r
}

func numericField(o apiObject, key string) (float64, bool) {
	value, ok := o[key].(float64)
	return value, ok && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func wanBitRate(o apiObject, key string) *float64 {
	value, ok := numericField(o, key)
	if !ok || value > math.MaxFloat64/8 {
		return nil
	}
	bps := value * 8
	return &bps
}

func publicWANAddress(raw string) *string {
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return nil
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
		return nil
	}
	value := address.String()
	return &value
}

func normalizeWANTelemetry(device apiObject) (wanResponse, time.Time) {
	r := wanResponse{Status: "unavailable"}
	stamp, ok := numericField(device, "last_seen")
	var sampledAt time.Time
	if ok && stamp > 0 && stamp < 1e12 && stamp == math.Trunc(stamp) {
		sampledAt = time.Unix(int64(stamp), 0)
	}
	if state, exists := numericField(device, "state"); !exists || state != 1 {
		r.Error = "Gateway ist nicht als online gemeldet"
		return r, sampledAt
	}
	active := []apiObject{}
	for _, key := range []string{"wan1", "wan2"} {
		wan := obj(device, key)
		up, _ := wan["up"].(bool)
		uplink, _ := wan["is_uplink"].(bool)
		if up && uplink {
			active = append(active, wan)
		}
	}
	if len(active) != 1 {
		r.Error = "Kein eindeutig aktiver WAN-Uplink verfügbar"
		return r, sampledAt
	}
	wan := active[0]
	name := pick(wan, "ifname", "name")
	if name != "" {
		r.ActiveInterface = &name
	}
	r.UploadBps = wanBitRate(wan, "tx_bytes-r")
	r.DownloadBps = wanBitRate(wan, "rx_bytes-r")
	// active_geo_info reports public addresses observed for active WANs; never
	// substitute wan1.ip, which is private behind an upstream router/NAT.
	geo := obj(device, "active_geo_info")
	alive := obj(device, "last_wan_interfaces")
	addresses := []string{}
	for name, raw := range geo {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		if metadata := obj(alive, name); metadata != nil {
			if isAlive, known := metadata["alive"].(bool); known && !isAlive {
				continue
			}
			// Tie the public observation to the selected physical uplink when
			// both interface addresses are available. An alive backup WAN is
			// not evidence of the selected uplink's public address.
			if selectedIP, observedIP := str(wan, "ip"), str(metadata, "ip"); selectedIP != "" && observedIP != "" {
				selected, selectedErr := netip.ParseAddr(selectedIP)
				observed, observedErr := netip.ParseAddr(observedIP)
				if selectedErr != nil || observedErr != nil || selected.Unmap() != observed.Unmap() {
					continue
				}
			}
		}
		if address := publicWANAddress(str(apiObject(item), "address")); address != nil {
			addresses = append(addresses, *address)
		}
	}
	if len(addresses) == 1 {
		r.PublicIP = &addresses[0]
	}
	if r.PublicIP != nil && r.UploadBps != nil && r.DownloadBps != nil {
		r.Status = "ok"
	} else if r.PublicIP != nil || r.UploadBps != nil || r.DownloadBps != nil {
		r.Status = "partial"
	}
	return r, sampledAt
}
