package internal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Tag struct {
	ID       string
	Name     string
	Category string
	Color    string
}

type Rule struct {
	ID      string
	TagID   string
	Field   string
	Match   string
	Pattern string
	Enabled bool
}

// Store persists tags, rules, and item tag assignments in SQLite.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the SQLite database at path (WAL mode).
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tags (
			id       TEXT PRIMARY KEY,
			name     TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT '',
			color    TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS rules (
			id         TEXT PRIMARY KEY,
			tag_id     TEXT NOT NULL,
			field      TEXT NOT NULL DEFAULT 'title',
			match_mode TEXT NOT NULL DEFAULT 'contains',
			pattern    TEXT NOT NULL,
			enabled    INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
		);
		CREATE TABLE IF NOT EXISTS item_tags (
			media_id TEXT NOT NULL,
			tag_id   TEXT NOT NULL,
			PRIMARY KEY (media_id, tag_id),
			FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_tags_category ON tags(category);
		CREATE INDEX IF NOT EXISTS idx_rules_tag ON rules(tag_id);
		CREATE INDEX IF NOT EXISTS idx_item_tags_media ON item_tags(media_id);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) CreateTag(t Tag) (*Tag, error) {
	if strings.TrimSpace(t.Name) == "" {
		return nil, fmt.Errorf("tag name required")
	}
	if t.ID == "" {
		t.ID = "tag_" + uuid.NewString()[:8]
	}
	_, err := s.db.Exec(
		`INSERT INTO tags (id, name, category, color) VALUES (?, ?, ?, ?)`,
		t.ID, t.Name, t.Category, t.Color,
	)
	if err != nil {
		return nil, fmt.Errorf("insert tag: %w", err)
	}
	out := t
	return &out, nil
}

func (s *Store) ListTags(category string) ([]*Tag, error) {
	var rows *sql.Rows
	var err error
	if category != "" {
		rows, err = s.db.Query(
			`SELECT id, name, category, color FROM tags WHERE lower(category) = lower(?) ORDER BY name`,
			category,
		)
	} else {
		rows, err = s.db.Query(`SELECT id, name, category, color FROM tags ORDER BY name`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Tag, 0)
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Category, &t.Color); err != nil {
			return nil, err
		}
		cp := t
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTag(id string) error {
	res, err := s.db.Exec(`DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("tag %q not found", id)
	}
	return nil
}

func (s *Store) SetItemTags(mediaID string, tagIDs []string) ([]string, error) {
	if strings.TrimSpace(mediaID) == "" {
		return nil, fmt.Errorf("media_id required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	seen := map[string]struct{}{}
	unique := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM tags WHERE id = ?`, id).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("tag %q not found", id)
			}
			return nil, err
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	if _, err := tx.Exec(`DELETE FROM item_tags WHERE media_id = ?`, mediaID); err != nil {
		return nil, err
	}
	for _, id := range unique {
		if _, err := tx.Exec(`INSERT INTO item_tags (media_id, tag_id) VALUES (?, ?)`, mediaID, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return unique, nil
}

func (s *Store) GetItemTags(mediaID string) ([]*Tag, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.name, t.category, t.color
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.media_id = ?
		ORDER BY t.name`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Tag, 0)
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Category, &t.Color); err != nil {
			return nil, err
		}
		cp := t
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *Store) UpsertRule(r Rule) (*Rule, error) {
	if strings.TrimSpace(r.TagID) == "" || strings.TrimSpace(r.Pattern) == "" {
		return nil, fmt.Errorf("tag_id and pattern required")
	}
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM tags WHERE id = ?`, r.TagID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("tag %q not found", r.TagID)
		}
		return nil, err
	}
	if r.ID == "" {
		r.ID = "rule_" + uuid.NewString()[:8]
	}
	if r.Field == "" {
		r.Field = "title"
	}
	if r.Match == "" {
		r.Match = "contains"
	}
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO rules (id, tag_id, field, match_mode, pattern, enabled)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			tag_id = excluded.tag_id,
			field = excluded.field,
			match_mode = excluded.match_mode,
			pattern = excluded.pattern,
			enabled = excluded.enabled`,
		r.ID, r.TagID, r.Field, r.Match, r.Pattern, enabled,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert rule: %w", err)
	}
	out := r
	return &out, nil
}

func (s *Store) ListRules() ([]*Rule, error) {
	rows, err := s.db.Query(`
		SELECT id, tag_id, field, match_mode, pattern, enabled FROM rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Rule, 0)
	for rows.Next() {
		var r Rule
		var enabled int
		if err := rows.Scan(&r.ID, &r.TagID, &r.Field, &r.Match, &r.Pattern, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		cp := r
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRule(id string) error {
	res, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("rule %q not found", id)
	}
	return nil
}

type ClassifyInput struct {
	MediaID   string
	Title     string
	Genres    []string
	Path      string
	MediaType string
	Merge     bool
}

func (s *Store) Classify(in ClassifyInput) (tags []*Tag, matched []string, err error) {
	if strings.TrimSpace(in.MediaID) == "" {
		return nil, nil, fmt.Errorf("media_id required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	hit := map[string]struct{}{}
	if in.Merge {
		rows, err := tx.Query(`SELECT tag_id FROM item_tags WHERE media_id = ?`, in.MediaID)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, nil, err
			}
			hit[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}

	rules, err := listRulesTx(tx)
	if err != nil {
		return nil, nil, err
	}
	matched = make([]string, 0)
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if !ruleMatches(r, in) {
			continue
		}
		hit[r.TagID] = struct{}{}
		matched = append(matched, r.ID)
	}

	if _, err := tx.Exec(`DELETE FROM item_tags WHERE media_id = ?`, in.MediaID); err != nil {
		return nil, nil, err
	}
	for id := range hit {
		if _, err := tx.Exec(`INSERT INTO item_tags (media_id, tag_id) VALUES (?, ?)`, in.MediaID, id); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	tags = make([]*Tag, 0, len(hit))
	for id := range hit {
		t, err := s.getTag(id)
		if err != nil {
			continue
		}
		tags = append(tags, t)
	}
	return tags, matched, nil
}

func listRulesTx(tx *sql.Tx) ([]*Rule, error) {
	rows, err := tx.Query(`SELECT id, tag_id, field, match_mode, pattern, enabled FROM rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Rule, 0)
	for rows.Next() {
		var r Rule
		var enabled int
		if err := rows.Scan(&r.ID, &r.TagID, &r.Field, &r.Match, &r.Pattern, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		cp := r
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *Store) getTag(id string) (*Tag, error) {
	var t Tag
	err := s.db.QueryRow(
		`SELECT id, name, category, color FROM tags WHERE id = ?`, id,
	).Scan(&t.ID, &t.Name, &t.Category, &t.Color)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func ruleMatches(r *Rule, in ClassifyInput) bool {
	var haystack string
	switch strings.ToLower(r.Field) {
	case "title":
		haystack = in.Title
	case "path":
		haystack = in.Path
	case "media_type", "type":
		haystack = in.MediaType
	case "genre":
		for _, g := range in.Genres {
			if fieldMatch(r.Match, g, r.Pattern) {
				return true
			}
		}
		return false
	default:
		haystack = in.Title
	}
	return fieldMatch(r.Match, haystack, r.Pattern)
}

func fieldMatch(mode, value, pattern string) bool {
	switch strings.ToLower(mode) {
	case "equals":
		return strings.EqualFold(value, pattern)
	case "prefix":
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(pattern))
	case "regex":
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	default: // contains
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
	}
}
