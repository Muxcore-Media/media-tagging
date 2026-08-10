package internal

import (
	"fmt"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func (m *Module) Settings() []contracts.SettingDef {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return []contracts.SettingDef{{
		Key: "default_category", Label: "Default tag category", Type: contracts.SettingTypeString,
		Value: m.defaultCategory, Default: "general",
		Description: "Category applied when CreateTag omits category", Group: "Tagging",
	}}
}

func (m *Module) UpdateSetting(key, value string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	if key != "default_category" {
		return fmt.Errorf("unknown setting %q", key)
	}
	if value == "" {
		return fmt.Errorf("default_category must not be empty")
	}
	m.defaultCategory = value
	return nil
}
