package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ExportState implements contracts.Backupable — snapshot tagging.db after WAL checkpoint.
func (m *Module) ExportState(ctx context.Context) ([]byte, error) {
	if m.store == nil {
		return nil, fmt.Errorf("store not open")
	}
	path := m.dbPath()
	if path == "" {
		return nil, fmt.Errorf("db path not set")
	}
	if err := m.store.Checkpoint(ctx); err != nil {
		return nil, err
	}
	return os.ReadFile(path) //nolint:gosec // path is module-controlled tagging.db
}

// ImportState replaces tagging.db from a backup snapshot.
func (m *Module) ImportState(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty backup payload")
	}
	path := m.dbPath()
	if path == "" {
		return fmt.Errorf("db path not set")
	}
	if m.store != nil {
		_ = m.store.Close()
		m.store = nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write db: %w", err)
	}
	store, err := OpenStore(ctx, path)
	if err != nil {
		return err
	}
	m.store = store
	return nil
}

func (m *Module) dbPath() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "tagging.db")
}
