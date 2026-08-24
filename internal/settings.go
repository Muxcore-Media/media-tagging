package internal

import (
	"fmt"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func (m *Module) Settings() []contracts.SettingDef {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return []contracts.SettingDef{
		{
			Key: "default_category", Label: "Default tag category", Type: contracts.SettingTypeString,
			Value: m.defaultCategory, Default: "general",
			Description: "Category applied when CreateTag omits category", Group: "Tagging",
		},
		{
			Key:         "data_dir",
			Label:       "Data directory",
			Type:        contracts.SettingTypeString,
			Value:       m.dataDir,
			Description: "Directory for SQLite database (tagging.db)",
			Group:       "Tagging",
		},
		{
			Key: "events_enabled", Label: "Auto-tag on library events", Type: contracts.SettingTypeBool,
			Value: fmt.Sprintf("%t", m.eventsEnabled), Default: "true",
			Description: "Apply rules on media.file.imported / movie|tv added events", Group: "Tagging",
		},
	}
}

func (m *Module) UpdateSetting(key, value string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	switch key {
	case "default_category":
		if value == "" {
			return fmt.Errorf("default_category must not be empty")
		}
		m.defaultCategory = value
	case "data_dir":
		return fmt.Errorf("data_dir is set at startup (TAGGING_DATA_DIR); restart to change")
	case "events_enabled":
		m.eventsEnabled = value == "1" || value == "true" || value == "TRUE"
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}
