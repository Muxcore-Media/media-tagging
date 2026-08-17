# Changelog

## [Unreleased]

### Added
- ModuleMesh methods `ListTags` / `CreateTag` / `ListRules` / `UpsertRule` / `DeleteRule` (JSON) for admin-ui soft proxy
- HTTP JSON API on health port: `GET|POST /api/tags`, `GET|POST /api/rules`, `DELETE /api/rules/{id}`

## [v0.2.0] — 2026-08-10

### Added
- SQLite persistence for tags, classification rules, and item tags (`TAGGING_DATA_DIR` / `tagging.db`, WAL)
- Auto-apply rules on `media.file.imported` and movie/TV library add/update events
- Offline event fixtures under `internal/testdata/events/`

### Changed
- Replaced in-memory store with `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`)

## [v0.1.1] — 2026-08-10

- Move default ports to :9740/:9741 (avoid database-sqlite :9700 collision)

## [v0.1.0] — 2026-08-10

### Added
- `TaggingService` (tags, item tags, classification rules, Classify)
- In-memory store + SettingsProvider (`default_category`)
- Health `:9741`
