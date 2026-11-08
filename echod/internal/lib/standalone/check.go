package standalone

import (
	"context"
	"errors"
)

// CheckTargets checks discovery and uniqueness without sending a command.
func (h *Home) CheckTargets(ctx context.Context) (int, error) {
	var cfg settings
	if err := h.read("home-local.json", &cfg); err != nil {
		return 0, err
	}
	b, err := h.backend(cfg)
	if err != nil {
		return 0, err
	}
	return b.check(ctx, cfg)
}

func (b switchBot) check(ctx context.Context, cfg settings) (int, error) {
	h := b.h
	devices, err := h.devices(ctx)
	if err != nil {
		return 0, err
	}
	scenes, err := h.scenes(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	if cfg.Hall.Name != "" {
		if _, err := deviceID(devices, cfg.Hall); err != nil {
			return 0, err
		}
		count++
	}
	for _, t := range cfg.Devices {
		if _, err := deviceID(devices, t); err != nil {
			return 0, err
		}
		count++
	}
	for _, actions := range cfg.VoiceScenes {
		if sceneID(scenes, actions["toggle"]) == "" {
			return 0, errors.New("voice scene unavailable or ambiguous")
		}
		count++
	}
	for _, id := range cfg.Scenes {
		matches := 0
		for _, s := range scenes {
			if s.SceneID == id {
				matches++
			}
		}
		if matches != 1 {
			return 0, errors.New("configured scene unavailable or ambiguous")
		}
	}
	return count, nil
}
