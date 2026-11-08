package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const Directory = "/data/misc/techo5/standalone-home"

var Device = &Home{dir: Directory, client: &http.Client{
	Timeout:       18 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}}

type Home struct {
	dir        string
	client     *http.Client
	powerMu    sync.Mutex
	limitMu    sync.Mutex
	retryAfter time.Time

	remoMu           sync.Mutex
	remoAppliances   []remoAppliance
	remoAppliancesAt time.Time
	remoDevices      []remoDevice
	remoDevicesAt    time.Time
	remoRetryAfter   time.Time

	googleMu          sync.Mutex
	googleAccessToken string
	googleUntil       time.Time
}

func (h *Home) Enabled() bool {
	_, err := os.Stat(filepath.Join(h.dir, "enabled"))
	return err == nil
}

type Scene struct{ ID, Label, Icon string }
type Event struct {
	Title    string    `json:"title"`
	Calendar string    `json:"calendar"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Day      bool      `json:"day"`
}
type Environment struct{ Temperature, Humidity, CO2, Battery *float64 }
type Result struct {
	OK, Stale   bool
	Fetched     time.Time
	Events      []Event
	Controls    []Scene
	Environment *Environment
}

type target struct{ Name, Type string }
type control struct{ Label, Icon, Toggle, On, Off string }
type settings struct {
	Backend     string `json:"backend"`
	RemoLocal   bool   `json:"remo_local"` // Remo ローカル API の枠。v1 は読み取るのみで送信は Cloud。
	Calendars   []struct{ ID, Name string }
	Scenes      []string
	Sensor      string             `json:"environment_device"`
	Controls    map[string]control `json:"power_controls"`
	Hall        target
	Devices     map[string]target            `json:"voice_devices"`
	VoiceScenes map[string]map[string]string `json:"voice_scenes"`
}

func (h *Home) read(name string, dst any) error {
	raw, err := os.ReadFile(filepath.Join(h.dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func (h *Home) save(name string, data any) error {
	f, err := os.CreateTemp(h.dir, ".home-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(data)
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
	return os.Rename(f.Name(), filepath.Join(h.dir, name))
}

func (h *Home) Dashboard(ctx context.Context, op, id string) (Result, error) {
	var cfg settings
	if err := h.read("home-local.json", &cfg); err != nil {
		return Result{}, err
	}
	switch op {
	case "agenda":
		result, err := h.agenda(ctx, cfg)
		buttons, buttonErr := h.power(ctx, cfg, "")
		if buttonErr == nil {
			result.Controls = buttons.Controls
		}
		// Even when the agenda has no usable cache the display can refresh its buttons.
		if err != nil && buttonErr != nil {
			return result, err
		}
		return result, nil
	case "power_status":
		return h.power(ctx, cfg, "")
	case "scene":
		return h.scene(ctx, cfg, id)
	case "environment":
		b, err := h.backend(cfg)
		if err != nil {
			return Result{}, err
		}
		env, err := b.environment(ctx, cfg)
		if err != nil {
			return Result{}, err
		}
		return Result{OK: env.Temperature != nil || env.Humidity != nil || env.CO2 != nil || env.Battery != nil, Environment: env}, nil
	}
	return Result{}, errors.New("unknown home operation")
}
