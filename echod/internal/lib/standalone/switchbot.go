package standalone

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

func segment(id string) string { return url.PathEscape(id) }

func (h *Home) credentials() (string, string, error) {
	raw, err := os.ReadFile(filepath.Join(h.dir, "switchbot.env"))
	if err != nil {
		return "", "", err
	}
	vars := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "export "), "switchbot_")
		vars[name] = strings.Trim(strings.TrimSpace(value), "'\x22")
	}
	if vars["token"] == "" || vars["secret"] == "" {
		return "", "", errors.New("missing SwitchBot credentials")
	}
	return vars["token"], vars["secret"], nil
}

func (h *Home) api(ctx context.Context, path string, payload any, dst any) error {
	h.limitMu.Lock()
	limited := time.Now().Before(h.retryAfter)
	h.limitMu.Unlock()
	if limited {
		return errors.New("SwitchBot rate limited")
	}
	token, secret, err := h.credentials()
	if err != nil {
		return err
	}
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		method, body = http.MethodPost, bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "https://api.switch-bot.com/v1.1/"+path, body)
	if err != nil {
		return err
	}
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	mac := hmac.New(sha256.New, []byte(secret))
	io.WriteString(mac, token+stamp+nonce)
	req.Header.Set("Authorization", token)
	req.Header.Set("t", stamp)
	req.Header.Set("nonce", nonce)
	req.Header.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	// A failed POST may already have moved a device. Do not replay it.
	resp, err := h.client.Do(req)
	if err != nil {
		return errors.New("SwitchBot connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		seconds, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err != nil {
			seconds = 86400
		}
		h.limitMu.Lock()
		h.retryAfter = time.Now().Add(time.Duration(max(seconds, 60)) * time.Second)
		h.limitMu.Unlock()
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("SwitchBot HTTP %d", resp.StatusCode)
	}
	var reply struct {
		StatusCode int
		Body       json.RawMessage
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&reply); err != nil {
		return err
	}
	if reply.StatusCode != 100 {
		return fmt.Errorf("SwitchBot status %d", reply.StatusCode)
	}
	if dst != nil {
		return json.Unmarshal(reply.Body, dst)
	}
	return nil
}

type botScene struct{ SceneName, SceneID string }
type botDevice struct{ DeviceName, DeviceType, RemoteType, DeviceID string }

func (h *Home) scenes(ctx context.Context) ([]botScene, error) {
	var list []botScene
	err := h.api(ctx, "scenes", nil, &list)
	return list, err
}
func sceneID(list []botScene, name string) string {
	var id string
	for _, s := range list {
		if name != "" && name == s.SceneName {
			if id != "" {
				return ""
			}
			id = s.SceneID
		}
	}
	return id
}

func buttons(defs map[string]control, list []botScene, last map[string]string) ([]Scene, map[string]string) {
	var out []Scene
	ids := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(defs)) {
		def := defs[key]
		on, off, toggle := sceneID(list, def.On), sceneID(list, def.Off), sceneID(list, def.Toggle)
		action, id, label := "setup", "", " 未設定"
		switch {
		case toggle != "":
			action, id, label = "toggle", toggle, "電源"
		case off != "" && (last[key] == "on" || on == ""):
			action, id, label = "off", off, " OFF"
		case on != "":
			action, id, label = "on", on, " ON"
		}
		button := "power/" + key + "/" + action
		icon := def.Icon
		if icon == "" {
			icon = "mdi:power"
		}
		out = append(out, Scene{button, def.Label + label, icon})
		if id != "" {
			ids[button] = id
		}
	}
	return out, ids
}

func (h *Home) executeScene(ctx context.Context, id string) error {
	return h.api(ctx, "scenes/"+segment(id)+"/execute", struct{}{}, nil)
}
func (h *Home) scene(ctx context.Context, cfg settings, id string) (Result, error) {
	if !usesSwitchBot(cfg) {
		return Result{}, errors.New("scene is not configured")
	}
	if strings.HasPrefix(id, "power/") {
		return h.power(ctx, cfg, id)
	}
	if id == "" || !slices.Contains(cfg.Scenes, id) {
		return Result{}, errors.New("scene is not configured")
	}
	list, err := h.scenes(ctx)
	if err != nil {
		return Result{}, err
	}
	count := 0
	for _, entry := range list {
		if entry.SceneID == id {
			count++
		}
	}
	if count != 1 {
		return Result{}, errors.New("scene unavailable or ambiguous")
	}
	err = h.executeScene(ctx, id)
	return Result{OK: err == nil}, err
}

func (h *Home) power(ctx context.Context, cfg settings, action string) (Result, error) {
	if !usesSwitchBot(cfg) {
		// SwitchBot 以外ではシーン・電源ボタンを出さない。
		return Result{OK: true}, nil
	}
	h.powerMu.Lock()
	defer h.powerMu.Unlock()
	list, err := h.scenes(ctx)
	if err != nil {
		return Result{}, err
	}
	last := map[string]string{}
	if err = h.read("power-last.json", &last); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	controls, targets := buttons(cfg.Controls, list, last)
	if action == "" {
		return Result{OK: true, Controls: controls}, nil
	}
	id, ok := targets[action]
	if !ok {
		return Result{Controls: controls}, nil
	}
	if err = h.executeScene(ctx, id); err != nil {
		return Result{}, err
	}
	parts := strings.Split(action, "/")
	if parts[2] != "toggle" {
		last[parts[1]] = parts[2]
		if err = h.save("power-last.json", last); err != nil {
			return Result{}, err
		}
		controls, _ = buttons(cfg.Controls, list, last)
	}
	return Result{OK: true, Controls: controls}, nil
}

func (h *Home) devices(ctx context.Context) ([]botDevice, error) {
	var catalog struct{ DeviceList, InfraredRemoteList []botDevice }
	if err := h.api(ctx, "devices", nil, &catalog); err != nil {
		return nil, err
	}
	return append(catalog.DeviceList, catalog.InfraredRemoteList...), nil
}

func deviceID(devices []botDevice, t target) (string, error) {
	if t.Name == "" || t.Type == "" {
		return "", errors.New("device not configured")
	}
	var matches []string
	for _, d := range devices {
		kind := d.DeviceType
		if kind == "" {
			kind = d.RemoteType
		}
		if d.DeviceName == t.Name && kind == t.Type {
			matches = append(matches, d.DeviceID)
		}
	}
	if len(matches) != 1 || matches[0] == "" {
		return "", errors.New("device unavailable or ambiguous")
	}
	return matches[0], nil
}

type switchBot struct{ h *Home }

func (b switchBot) appliance(ctx context.Context, cfg settings, key, action string, value int) error {
	h := b.h
	if value != 0 {
		return errors.New("SwitchBot backend does not support this action")
	}
	if (key == "monitor" || key == "projector") && action == "toggle" {
		list, err := h.scenes(ctx)
		if err != nil {
			return err
		}
		id := sceneID(list, cfg.VoiceScenes[key][action])
		if id == "" {
			return errors.New("voice scene unavailable or ambiguous")
		}
		return h.executeScene(ctx, id)
	}
	t := cfg.Devices[key]
	switch key {
	case "hall":
		t = cfg.Hall
		if t.Type != "Color Bulb" {
			return errors.New("hall bulb not configured")
		}
	case "curtain":
		switch action {
		case "open":
			action = "on"
		case "close":
			action = "off"
		default:
			return errors.New("invalid curtain direction")
		}
	case "living", "fan", "plug", "ac", "vacuum":
	default:
		return errors.New("unknown appliance")
	}
	command := map[string]string{"on": "turnOn", "off": "turnOff"}[action]
	if command == "" {
		return errors.New("invalid power direction")
	}
	devices, err := h.devices(ctx)
	if err != nil {
		return err
	}
	id, err := deviceID(devices, t)
	if err != nil {
		return err
	}
	return h.api(ctx, "devices/"+segment(id)+"/commands", map[string]string{"commandType": "command", "command": command, "parameter": "default"}, nil)
}

func (b switchBot) environment(ctx context.Context, cfg settings) (*Environment, error) {
	if cfg.Sensor == "" {
		return nil, errors.New("environment sensor is not configured")
	}
	var env Environment
	if err := b.h.api(ctx, "devices/"+segment(cfg.Sensor)+"/status", nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
