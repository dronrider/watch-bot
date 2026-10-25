// Пакет конфигурации: чтение TOML, умолчания и валидация.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/robfig/cron/v3"
)

// DefaultSchedule подставляется источнику без своего cron-выражения: раз в
// 15 минут, сброс ловится в среднем через 7 минут после анонса.
const DefaultSchedule = "*/15 * * * *"

// DefaultFreshnessDays подставляется источнику без freshness_days.
const DefaultFreshnessDays = 3

type Source struct {
	Script        string `toml:"script"`
	Schedule      string `toml:"schedule"`
	FreshnessDays int    `toml:"freshness_days"`
	Enabled       bool   `toml:"enabled"`
	// Service это продукт источника (Claude, Codex): из него собирается
	// заголовок уведомления. Пустое поле откатывается к имени источника.
	Service string `toml:"service"`
}

// Name это имя источника в сообщениях, состоянии и логах. Берётся от базового
// имени скрипта без расширения, чтобы не тащить в конфиг ещё одно поле.
func (s Source) Name() string {
	base := filepath.Base(s.Script)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

type State struct {
	Path string `toml:"path"`
}

type Notify struct {
	Kind string `toml:"kind"`
}

type File struct {
	State   State    `toml:"state"`
	Notify  Notify   `toml:"notify"`
	Sources []Source `toml:"source"`
}

func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", path, err)
	}
	var f File
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}
	if f.State.Path == "" {
		return nil, fmt.Errorf("%s: пустой state.path", path)
	}
	if f.Notify.Kind == "" {
		f.Notify.Kind = "telegram"
	}
	if f.Notify.Kind != "telegram" {
		return nil, fmt.Errorf("%s: notify.kind %q не поддерживается, доступен telegram", path, f.Notify.Kind)
	}
	for i := range f.Sources {
		s := &f.Sources[i]
		if s.Script == "" {
			return nil, fmt.Errorf("%s: источник №%d без script", path, i+1)
		}
		if s.Schedule == "" {
			s.Schedule = DefaultSchedule
		}
		if _, err := cron.ParseStandard(s.Schedule); err != nil {
			return nil, fmt.Errorf("%s: источник %s: schedule %q: %w", path, s.Script, s.Schedule, err)
		}
		if s.FreshnessDays <= 0 {
			s.FreshnessDays = DefaultFreshnessDays
		}
	}
	return &f, nil
}
