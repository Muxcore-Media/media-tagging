package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var (
	allowedRuleFields = map[string]struct{}{
		"title": {}, "genre": {}, "path": {}, "media_type": {},
	}
	allowedRuleMatches = map[string]struct{}{
		"contains": {}, "equals": {}, "prefix": {}, "regex": {},
	}
)

type Tag struct {
	ID       string
	Name     string
	Category string
	Color    string
}

type Rule struct { //nolint:govet // fieldalignment: persisted rule fields grouped for readability
	ID      string
	TagID   string
	Field   string
	Match   string
	Pattern string
	Enabled bool
	regex   *regexp.Regexp
}

// Store persists tags, rules, and item tag assignments in SQLite.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the SQLite database at path (WAL mode).
func OpenStore(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
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
		CREATE TABLE IF NOT EXISTS media_aliases (
			alias    TEXT PRIMARY KEY,
			media_id TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tags_category ON tags(category);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_name_category ON tags(name COLLATE NOCASE, category COLLATE NOCASE);
		CREATE INDEX IF NOT EXISTS idx_rules_tag ON rules(tag_id);
		CREATE INDEX IF NOT EXISTS idx_item_tags_media ON item_tags(media_id);
		CREATE INDEX IF NOT EXISTS idx_media_aliases_media ON media_aliases(media_id);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Ping verifies the database connection is alive.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not open")
	}
	return s.db.PingContext(ctx)
}

// Checkpoint flushes WAL pages for backup snapshots.
func (s *Store) Checkpoint(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not open")
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func newTagID() string  { return "tag_" + uuid.NewString() }
func newRuleID() string { return "rule_" + uuid.NewString() }

func validateRuleField(field string) error {
	if _, ok := allowedRuleFields[strings.ToLower(strings.TrimSpace(field))]; !ok {
		return fmt.Errorf("invalid field %q (allowed: title, genre, path, media_type)", field)
	}
	return nil
}

func validateRuleMatch(match string) error {
	if _, ok := allowedRuleMatches[strings.ToLower(strings.TrimSpace(match))]; !ok {
		return fmt.Errorf("invalid match %q (allowed: contains, equals, prefix, regex)", match)
	}
	return nil
}

func compileRuleRegex(pattern string) (*regexp.Regexp, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	return re, nil
}

func (s *Store) CreateTag(ctx context.Context, t Tag) (*Tag, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return nil, fmt.Errorf("tag name required")
	}
	category := strings.TrimSpace(t.Category)
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM tags WHERE name = ? COLLATE NOCASE AND category = ? COLLATE NOCASE`,
		name, category,
	).Scan(&exists)
	if err == nil {
		return nil, fmt.Errorf("tag %q already exists in category %q", name, category)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if t.ID == "" {
		t.ID = newTagID()
	}
	t.Name = name
	t.Category = category
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tags (id, name, category, color) VALUES (?, ?, ?, ?)`,
		t.ID, t.Name, t.Category, t.Color,
	)
	if err != nil {
		return nil, fmt.Errorf("insert tag: %w", err)
	}
	out := t
	return &out, nil
}

func (s *Store) ListTags(ctx context.Context, category string) ([]*Tag, error) {
	var rows *sql.Rows
	var err error
	if category != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, name, category, color FROM tags WHERE lower(category) = lower(?) ORDER BY name`,
			category,
		)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id, name, category, color FROM tags ORDER BY name`)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func (s *Store) DeleteTag(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: tag %q", ErrNotFound, id)
	}
	return nil
}

func (s *Store) RegisterAlias(ctx context.Context, alias, mediaID string) error {
	alias = strings.TrimSpace(alias)
	mediaID = strings.TrimSpace(mediaID)
	if alias == "" || mediaID == "" || alias == mediaID {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_aliases (alias, media_id) VALUES (?, ?)
		ON CONFLICT(alias) DO UPDATE SET media_id = excluded.media_id`,
		alias, mediaID,
	)
	return err
}

func (s *Store) ResolveMediaID(ctx context.Context, mediaID string) (string, error) {
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" {
		return "", fmt.Errorf("media_id required")
	}
	var canonical string
	err := s.db.QueryRowContext(ctx, `SELECT media_id FROM media_aliases WHERE alias = ?`, mediaID).Scan(&canonical)
	if err == sql.ErrNoRows {
		return mediaID, nil
	}
	if err != nil {
		return "", err
	}
	return canonical, nil
}

func (s *Store) aliasesForMedia(ctx context.Context, mediaID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT alias FROM media_aliases WHERE media_id = ?`, mediaID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		out = append(out, alias)
	}
	return out, rows.Err()
}

func (s *Store) MergeItemTags(ctx context.Context, fromAlias, toMediaID string) error {
	fromAlias = strings.TrimSpace(fromAlias)
	toMediaID = strings.TrimSpace(toMediaID)
	if fromAlias == "" || toMediaID == "" || fromAlias == toMediaID {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `SELECT tag_id FROM item_tags WHERE media_id = ?`, fromAlias)
	if err != nil {
		return err
	}
	tagIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		tagIDs = append(tagIDs, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		_, _ = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO item_tags (media_id, tag_id) VALUES (?, ?)`, toMediaID, tagID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE media_id = ?`, fromAlias); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetItemTags(ctx context.Context, mediaID string, tagIDs []string) ([]string, error) {
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" {
		return nil, fmt.Errorf("media_id required")
	}
	resolved, err := s.ResolveMediaID(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	mediaID = resolved

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	seen := map[string]struct{}{}
	unique := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id = ?`, id).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("%w: tag %q", ErrNotFound, id)
			}
			return nil, err
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE media_id = ?`, mediaID); err != nil {
		return nil, err
	}
	for _, id := range unique {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags (media_id, tag_id) VALUES (?, ?)`, mediaID, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return unique, nil
}

func (s *Store) GetItemTags(ctx context.Context, mediaID string) ([]*Tag, error) {
	resolved, err := s.ResolveMediaID(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.category, t.color
		FROM item_tags it
		JOIN tags t ON t.id = it.tag_id
		WHERE it.media_id = ?
		ORDER BY t.name`, resolved)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func (s *Store) DeleteItemTagsForMedia(ctx context.Context, mediaID string, aliases ...string) error {
	mediaID = strings.TrimSpace(mediaID)
	ids := []string{mediaID}
	ids = append(ids, aliases...)
	known, err := s.aliasesForMedia(ctx, mediaID)
	if err != nil {
		return err
	}
	ids = append(ids, known...)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE media_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_aliases WHERE alias = ? OR media_id = ?`, id, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertRule(ctx context.Context, r Rule) (*Rule, error) {
	if strings.TrimSpace(r.TagID) == "" || strings.TrimSpace(r.Pattern) == "" {
		return nil, fmt.Errorf("tag_id and pattern required")
	}
	field := strings.ToLower(strings.TrimSpace(r.Field))
	if field == "" {
		field = "title"
	}
	if err := validateRuleField(field); err != nil {
		return nil, err
	}
	match := strings.ToLower(strings.TrimSpace(r.Match))
	if match == "" {
		match = "contains"
	}
	if err := validateRuleMatch(match); err != nil {
		return nil, err
	}
	var compiled *regexp.Regexp
	if match == "regex" {
		var err error
		compiled, err = compileRuleRegex(r.Pattern)
		if err != nil {
			return nil, err
		}
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE id = ?`, r.TagID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: tag %q", ErrNotFound, r.TagID)
		}
		return nil, err
	}
	if r.ID == "" {
		r.ID = newRuleID()
	}
	r.Field = field
	r.Match = match
	r.regex = compiled
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx, `
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

func (s *Store) ListRules(ctx context.Context) ([]*Rule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tag_id, field, match_mode, pattern, enabled FROM rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*Rule, 0)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRule(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: rule %q", ErrNotFound, id)
	}
	return nil
}

type ClassifyInput struct { //nolint:govet // fieldalignment: input fields grouped for readability
	MediaID   string
	Title     string
	Genres    []string
	Path      string
	MediaType string
	Merge     bool
}

func (s *Store) Classify(ctx context.Context, in ClassifyInput) (tags []*Tag, matched []string, err error) {
	if strings.TrimSpace(in.MediaID) == "" {
		return nil, nil, fmt.Errorf("media_id required")
	}
	resolved, err := s.ResolveMediaID(ctx, in.MediaID)
	if err != nil {
		return nil, nil, err
	}
	in.MediaID = resolved

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	hit := map[string]struct{}{}
	if in.Merge {
		mergeRows, mergeErr := tx.QueryContext(ctx, `SELECT tag_id FROM item_tags WHERE media_id = ?`, in.MediaID)
		if mergeErr != nil {
			return nil, nil, mergeErr
		}
		for mergeRows.Next() {
			var id string
			if scanErr := mergeRows.Scan(&id); scanErr != nil {
				_ = mergeRows.Close()
				return nil, nil, scanErr
			}
			hit[id] = struct{}{}
		}
		_ = mergeRows.Close()
		if mergeErr := mergeRows.Err(); mergeErr != nil {
			return nil, nil, mergeErr
		}
	}

	rules, err := listRulesTx(ctx, tx)
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE media_id = ?`, in.MediaID); err != nil {
		return nil, nil, err
	}
	for id := range hit {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags (media_id, tag_id) VALUES (?, ?)`, in.MediaID, id); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	tags = make([]*Tag, 0, len(hit))
	for id := range hit {
		t, err := s.getTag(ctx, id)
		if err != nil {
			continue
		}
		tags = append(tags, t)
	}
	return tags, matched, nil
}

func listRulesTx(ctx context.Context, tx *sql.Tx) ([]*Rule, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, tag_id, field, match_mode, pattern, enabled FROM rules`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*Rule, 0)
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRule(rows rowScanner) (*Rule, error) {
	var r Rule
	var enabled int
	if err := rows.Scan(&r.ID, &r.TagID, &r.Field, &r.Match, &r.Pattern, &enabled); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	if strings.EqualFold(r.Match, "regex") {
		re, err := compileRuleRegex(r.Pattern)
		if err != nil {
			return nil, err
		}
		r.regex = re
	}
	cp := r
	return &cp, nil
}

func (s *Store) getTag(ctx context.Context, id string) (*Tag, error) {
	var t Tag
	err := s.db.QueryRowContext(ctx,
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
			if fieldMatch(r, g) {
				return true
			}
		}
		return false
	default:
		haystack = in.Title
	}
	return fieldMatch(r, haystack)
}

func fieldMatch(r *Rule, value string) bool {
	switch strings.ToLower(r.Match) {
	case "equals":
		return strings.EqualFold(value, r.Pattern)
	case "prefix":
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(r.Pattern))
	case "regex":
		if r.regex == nil {
			return false
		}
		return r.regex.MatchString(value)
	default: // contains
		return strings.Contains(strings.ToLower(value), strings.ToLower(r.Pattern))
	}
}
