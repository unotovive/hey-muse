package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	remoBase        = "https://api.nature.global"
	remoCacheTTL    = 60 * time.Second
	remoDefaultWait = 300 * time.Second
)

type remoAppliance struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Nickname string `json:"nickname"`
	Settings struct {
		Mode string `json:"mode"`
		Temp string `json:"temp"`
	} `json:"settings"`
}

type remoDevice struct {
	ID           string `json:"id"`
	NewestEvents map[string]struct {
		Val *float64 `json:"val"`
	} `json:"newest_events"`
}

// natureRemo は Nature Remo Cloud API 経由の家電バックエンド。
type natureRemo struct{ h *Home }

func (h *Home) remoToken() (string, error) {
	raw, err := os.ReadFile(filepath.Join(h.dir, "natureremo.env"))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(name)), "EXPORT ")
		if name == "NATURE_REMO_TOKEN" {
			if v := strings.Trim(strings.TrimSpace(value), "'\x22"); v != "" {
				return v, nil
			}
		}
	}
	return "", errors.New("missing Nature Remo token")
}

func (b natureRemo) api(ctx context.Context, method, path string, form url.Values, dst any) error {
	h := b.h
	h.remoMu.Lock()
	limited := time.Now().Before(h.remoRetryAfter)
	h.remoMu.Unlock()
	if limited {
		return errors.New("Nature Remo rate limited")
	}
	token, err := h.remoToken()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, remoBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// A failed POST may already have moved a device. Do not replay it.
	resp, err := h.client.Do(req)
	if err != nil {
		return errors.New("Nature Remo connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		wait := remoDefaultWait
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			wait = time.Duration(seconds) * time.Second
		}
		h.remoMu.Lock()
		h.remoRetryAfter = time.Now().Add(wait)
		h.remoMu.Unlock()
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("Nature Remo HTTP %d", resp.StatusCode)
	}
	if dst == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(dst)
}

func (b natureRemo) appliances(ctx context.Context) ([]remoAppliance, error) {
	h := b.h
	h.remoMu.Lock()
	if time.Since(h.remoAppliancesAt) < remoCacheTTL && h.remoAppliances != nil {
		list := h.remoAppliances
		h.remoMu.Unlock()
		return list, nil
	}
	h.remoMu.Unlock()
	var list []remoAppliance
	if err := b.api(ctx, http.MethodGet, "/1/appliances", nil, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []remoAppliance{}
	}
	h.remoMu.Lock()
	h.remoAppliances, h.remoAppliancesAt = list, time.Now()
	h.remoMu.Unlock()
	return list, nil
}

func (b natureRemo) devices(ctx context.Context) ([]remoDevice, error) {
	h := b.h
	h.remoMu.Lock()
	if time.Since(h.remoDevicesAt) < remoCacheTTL && h.remoDevices != nil {
		list := h.remoDevices
		h.remoMu.Unlock()
		return list, nil
	}
	h.remoMu.Unlock()
	var list []remoDevice
	if err := b.api(ctx, http.MethodGet, "/1/devices", nil, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []remoDevice{}
	}
	h.remoMu.Lock()
	h.remoDevices, h.remoDevicesAt = list, time.Now()
	h.remoMu.Unlock()
	return list, nil
}

func remoTarget(cfg settings, key string) (target, error) {
	switch key {
	case "hall":
		return cfg.Hall, nil
	case "living", "fan", "plug", "ac", "vacuum":
		return cfg.Devices[key], nil
	}
	return target{}, errors.New("unknown appliance")
}

func remoFind(list []remoAppliance, t target) (remoAppliance, error) {
	if t.Name == "" || t.Type == "" {
		return remoAppliance{}, errors.New("device not configured")
	}
	var found []remoAppliance
	for _, a := range list {
		if a.Nickname == t.Name && a.Type == t.Type && a.ID != "" {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		return remoAppliance{}, errors.New("device unavailable or ambiguous")
	}
	return found[0], nil
}

func (b natureRemo) appliance(ctx context.Context, cfg settings, key, action string, value int) error {
	t, err := remoTarget(cfg, key)
	if err != nil {
		return err
	}
	if value < 0 {
		return errors.New("invalid temperature")
	}
	list, err := b.appliances(ctx)
	if err != nil {
		return err
	}
	a, err := remoFind(list, t)
	if err != nil {
		return err
	}
	form := url.Values{}
	var path string
	switch a.Type {
	case "LIGHT":
		if value != 0 || (action != "on" && action != "off") {
			return errors.New("invalid power direction")
		}
		path = "/1/appliances/" + segment(a.ID) + "/light"
		form.Set("button", action)
	case "AC":
		path = "/1/appliances/" + segment(a.ID) + "/aircon_settings"
		mode := a.Settings.Mode
		if mode == "" {
			mode = "auto"
		}
		switch action {
		case "off":
			if value != 0 {
				return errors.New("invalid power direction")
			}
			form.Set("button", "power-off")
		case "on":
			form.Set("operation_mode", mode)
		case "cool":
			form.Set("operation_mode", "cool")
		case "heat":
			form.Set("operation_mode", "warm")
		case "temp":
			if value == 0 {
				return errors.New("temperature required")
			}
			form.Set("operation_mode", mode)
		default:
			return errors.New("invalid air conditioner action")
		}
		if value > 0 {
			form.Set("temperature", strconv.Itoa(value))
		}
	default:
		return errors.New("unsupported appliance type")
	}
	return b.api(ctx, http.MethodPost, path, form, nil)
}

func (b natureRemo) environment(ctx context.Context, cfg settings) (*Environment, error) {
	devices, err := b.devices(ctx)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, errors.New("no Nature Remo device")
	}
	events := devices[0].NewestEvents
	env := &Environment{}
	if e, ok := events["te"]; ok {
		env.Temperature = e.Val
	}
	if e, ok := events["hu"]; ok {
		env.Humidity = e.Val
	}
	return env, nil
}

func (b natureRemo) check(ctx context.Context, cfg settings) (int, error) {
	list, err := b.appliances(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	if cfg.Hall.Name != "" {
		if _, err := remoFind(list, cfg.Hall); err != nil {
			return 0, err
		}
		count++
	}
	for _, t := range cfg.Devices {
		if _, err := remoFind(list, t); err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}
