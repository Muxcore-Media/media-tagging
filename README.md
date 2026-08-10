# Media Tagging

Content tagging and rule-based classification for MuxCore.

Exposes `muxcore.tagging.v1.TaggingService` with an in-memory store in **v0.1.0**.

## Ports

| Service | Default |
|---------|---------|
| gRPC | `:9700` |
| Health | `:9701` |

## Build / test

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o bin/media-tagging ./cmd/module
```

## Status

v0.1.0 scaffold — persistence, ML classifiers, and library auto-hooks are follow-ups.
