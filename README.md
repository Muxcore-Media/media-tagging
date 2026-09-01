# Media Tagging

Content tagging and rule-based classification for MuxCore.

Exposes `muxcore.tagging.v1.TaggingService` with **SQLite persistence** and auto-apply on `media.file.imported` / library add events.

## Ports

| Service | Default |
|---------|---------|
| gRPC | `127.0.0.1:9740` |
| Health / HTTP API | `127.0.0.1:9741` |

Set `TAGGING_DATA_DIR` to the MVP data volume (e.g. `$DATA/tagging` from `run-host.sh`) so `tagging.db` persists across restarts.

## Persistence

| Path | Contents |
|------|----------|
| `$TAGGING_DATA_DIR/tagging.db` (default `./data/tagging.db`) | Tags, rules, item tag assignments (WAL) |

## Events

When `TAGGING_EVENTS_ENABLED` is true (default), the module dials core and classifies:

- `media.file.imported`
- `media.movie.added` / `media.movie.updated` / `media.movie.removed`
- `media.tv.added` / `media.tv.updated` / `media.tv.removed`

Offline fixtures under `internal/testdata/events/` drive unit tests without a live mesh.

## Build / test

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o bin/media-tagging ./cmd/module
```

## Status

v0.2.1 — SQLite persistence + event auto-tag fixtures + HTTP/mesh rules API for admin-ui.
