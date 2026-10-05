# Changelog

## [0.2.4] - 2026-10-05


### Security
- gRPC server and peer dials use mesh TLS (meshtls, sdk/go/module v0.6.5) unless the dev insecure flag is set (ADR-0016/0017).

## [0.2.3] - 2026-10-05

### Changed
- Built on core v0.6.14 / sdk/go/module v0.6.4: unregisters on shutdown and re-registers after core restarts (ADR-0022).

## [0.2.2] - 2026-10-05


### Changed
- Reported version comes from muxcore.json (ADR-0021); built on core v0.6.12 / sdk/go/module v0.6.3 (mesh enrollment, ADR-0017).

## [0.2.1] - 2026-10-05

### Changed
- CI runs on GitHub-hosted runners from the umbrella template; retired-origin workflows removed.
- Dependencies resolve from published GitHub tags (no filesystem `replace`); requires core v0.6.0.

## [v0.2.1] — 2026-08-31

### Added
- Media id aliases unify `tmdb:*` / `path:*` import keys with library `movie_id` / `series_id`
- Genre-aware auto-classify via mesh lookup to media-movies / media-tvshows (mock in tests)
- Mesh + HTTP: `DeleteTag`, `GetItemTags`, `SetItemTags`, `Classify`
- `media.movie.removed` / `media.tv.removed` cleanup of item tag assignments
- `contracts.Backupable` export/import of `tagging.db`
- Live `events_enabled` setting toggles mesh subscribe with reconnect backoff
- Rule/tag validation (field/match allowlists, regex compile at upsert, unique name+category)
- Honest health (`Ping` SQLite, `/health` + `/healthz`, 404 for not-found)
- Removal event fixtures under `internal/testdata/events/`

### Changed
- Default bind addresses: `127.0.0.1:9740` / `127.0.0.1:9741`
- Events import `github.com/Muxcore-Media/contracts-media/events` directly
- Tag/rule ids use full UUIDs
- CI clones sibling modules, runs golangci-lint + race tests

## [v0.2.0] — 2026-08-10

### Added
- ModuleMesh methods `ListTags` / `CreateTag` / `ListRules` / `UpsertRule` / `DeleteRule` (JSON) for admin-ui soft proxy
- HTTP JSON API on health port: `GET|POST /api/tags`, `GET|POST /api/rules`, `DELETE /api/rules/{id}`
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
