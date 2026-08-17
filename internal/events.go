package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

// mediaIDFromImport builds a stable media id from a file.imported payload.
func mediaIDFromImport(p contracts.FileImportedPayload) string {
	mt := strings.ToLower(strings.TrimSpace(p.MediaType))
	if p.TMDBID > 0 {
		switch mt {
		case "tv", "episode", "series":
			if p.SeasonNumber > 0 && p.EpisodeNumber > 0 {
				return fmt.Sprintf("tmdb:tv:%d:s%02de%02d", p.TMDBID, p.SeasonNumber, p.EpisodeNumber)
			}
			return fmt.Sprintf("tmdb:tv:%d", p.TMDBID)
		default:
			return fmt.Sprintf("tmdb:movie:%d", p.TMDBID)
		}
	}
	path := firstNonEmpty(p.DestinationPath, p.StorageKey, p.OriginalPath)
	if path != "" {
		return "path:" + path
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return ""
	}
	if p.Year > 0 {
		return fmt.Sprintf("title:%s:%d", title, p.Year)
	}
	return "title:" + title
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ApplyFileImported classifies a media.file.imported event payload (merge=true).
func (m *Module) ApplyFileImported(payload []byte) (mediaID string, tags []*Tag, matched []string, err error) {
	var p contracts.FileImportedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", nil, nil, fmt.Errorf("unmarshal file.imported: %w", err)
	}
	mediaID = mediaIDFromImport(p)
	if mediaID == "" {
		return "", nil, nil, fmt.Errorf("file.imported missing media identity")
	}
	path := firstNonEmpty(p.DestinationPath, p.StorageKey, p.OriginalPath)
	tags, matched, err = m.store.Classify(ClassifyInput{
		MediaID:   mediaID,
		Title:     p.Title,
		Path:      path,
		MediaType: p.MediaType,
		Merge:     true,
	})
	return mediaID, tags, matched, err
}

// ApplyLibraryEvent applies classification for library add/update events.
func (m *Module) ApplyLibraryEvent(eventType string, payload []byte) (mediaID string, tags []*Tag, matched []string, err error) {
	switch eventType {
	case contracts.EventMovieAdded, contracts.EventMovieUpdated:
		var p contracts.MovieAddedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return "", nil, nil, err
		}
		mediaID = strings.TrimSpace(p.MovieID)
		if mediaID == "" && p.TMDBID > 0 {
			mediaID = fmt.Sprintf("tmdb:movie:%d", p.TMDBID)
		}
		if mediaID == "" {
			return "", nil, nil, fmt.Errorf("movie event missing id")
		}
		tags, matched, err = m.store.Classify(ClassifyInput{
			MediaID: mediaID, Title: p.Title, MediaType: "movie", Merge: true,
		})
		return mediaID, tags, matched, err
	case contracts.EventTVAdded, contracts.EventTVUpdated:
		var p contracts.TVAddedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return "", nil, nil, err
		}
		mediaID = strings.TrimSpace(p.SeriesID)
		if mediaID == "" && p.TMDBID > 0 {
			mediaID = fmt.Sprintf("tmdb:tv:%d", p.TMDBID)
		}
		if mediaID == "" {
			return "", nil, nil, fmt.Errorf("tv event missing id")
		}
		tags, matched, err = m.store.Classify(ClassifyInput{
			MediaID: mediaID, Title: p.Name, MediaType: "tv", Merge: true,
		})
		return mediaID, tags, matched, err
	case contracts.EventFileImported:
		return m.ApplyFileImported(payload)
	default:
		return "", nil, nil, fmt.Errorf("unsupported event type %q", eventType)
	}
}

func (m *Module) startEventSubscribe() {
	m.cfgMu.RLock()
	enabled := m.eventsEnabled
	m.cfgMu.RUnlock()
	if !enabled {
		slog.Info("media-tagging: event auto-tag disabled (TAGGING_EVENTS_ENABLED)")
		return
	}
	go m.dialCoreAndSubscribe()
}

func (m *Module) dialCoreAndSubscribe() {
	time.Sleep(8 * time.Second)
	meshAddr := os.Getenv("MUXCORE_GRPC_ADDR")
	if meshAddr == "" {
		meshAddr = "localhost:9090"
	}
	insecureMode := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
	var opts []client.Option
	if insecureMode {
		opts = append(opts, client.WithInsecure())
	}
	c, err := client.Dial(meshAddr, opts...)
	if err != nil {
		slog.Error("media-tagging: dial core", "error", err)
		return
	}
	m.peerMu.Lock()
	m.mc = c
	m.peerMu.Unlock()
	slog.Info("media-tagging: connected to core mesh", "addr", meshAddr)

	for _, et := range []string{
		contracts.EventFileImported,
		contracts.EventMovieAdded,
		contracts.EventMovieUpdated,
		contracts.EventTVAdded,
		contracts.EventTVUpdated,
	} {
		ch, cancel, err := c.Events.Subscribe(context.Background(), et)
		if err != nil {
			slog.Warn("media-tagging: subscribe", "type", et, "error", err)
			continue
		}
		go m.handleTagEvents(et, ch, cancel)
		slog.Info("media-tagging: subscribed", "type", et)
	}
}

func (m *Module) handleTagEvents(eventType string, ch <-chan *eventsv1.Event, cancel context.CancelFunc) {
	for evt := range ch {
		mediaID, tags, matched, err := m.ApplyLibraryEvent(eventType, evt.GetPayload())
		if err != nil {
			slog.Debug("media-tagging: apply event", "type", eventType, "error", err)
			continue
		}
		slog.Info("media-tagging: applied rules",
			"type", eventType, "media_id", mediaID, "tags", len(tags), "matched_rules", len(matched))
	}
	cancel()
}
