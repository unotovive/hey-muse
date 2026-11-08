package standalone

import (
	"context"
	"fmt"
)

// backend は家電の操作・環境センサー・疎通確認を提供する接続先（SwitchBot / Nature Remo）。
type backend interface {
	appliance(ctx context.Context, cfg settings, key, action string, value int) error
	environment(ctx context.Context, cfg settings) (*Environment, error)
	check(ctx context.Context, cfg settings) (int, error)
}

// backend は設定の backend 名に対応する実装を返す。空は switchbot。
func (h *Home) backend(cfg settings) (backend, error) {
	switch cfg.Backend {
	case "", "switchbot":
		return switchBot{h}, nil
	case "nature_remo":
		return natureRemo{h}, nil
	}
	return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
}

// usesSwitchBot はシーン・電源ボタンなど SwitchBot 固有の機能を使えるかを返す。
func usesSwitchBot(cfg settings) bool { return cfg.Backend == "" || cfg.Backend == "switchbot" }

// BackendName は設定されたバックエンド名を返す（home-check 表示用）。
func (h *Home) BackendName() (string, error) {
	var cfg settings
	if err := h.read("home-local.json", &cfg); err != nil {
		return "", err
	}
	if _, err := h.backend(cfg); err != nil {
		return "", err
	}
	name := cfg.Backend
	if name == "" {
		name = "switchbot"
	}
	if cfg.RemoLocal {
		name += " (remo_local: 未使用、送信は Cloud のみ)"
	}
	return name, nil
}

// Appliance は家電を操作する。value は温度（0 は未指定）。
func (h *Home) Appliance(ctx context.Context, key, action string, value int) error {
	var cfg settings
	if err := h.read("home-local.json", &cfg); err != nil {
		return err
	}
	b, err := h.backend(cfg)
	if err != nil {
		return err
	}
	return b.appliance(ctx, cfg, key, action, value)
}
