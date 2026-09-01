package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	mediacontracts "github.com/Muxcore-Media/contracts-media/events"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func tmdbMovieAlias(tmdbID int32) string {
	return fmt.Sprintf("tmdb:movie:%d", tmdbID)
}

func tmdbTVAlias(tmdbID int32) string {
	return fmt.Sprintf("tmdb:tv:%d", tmdbID)
}

// mediaIDFromImport builds a stable alias key from a file.imported payload.
func mediaIDFromImport(p mediacontracts.FileImportedPayload) string {
	mt := strings.ToLower(strings.TrimSpace(p.MediaType))
	if p.TMDBID > 0 {
		switch mt {
		case "tv", "episode", "series":
			if p.SeasonNumber > 0 && p.EpisodeNumber > 0 {
				return fmt.Sprintf("tmdb:tv:%d:s%02de%02d", p.TMDBID, p.SeasonNumber, p.EpisodeNumber)
			}
			return tmdbTVAlias(p.TMDBID)
		default:
			return tmdbMovieAlias(p.TMDBID)
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

func isTVMediaType(mt string) bool {
	switch strings.ToLower(strings.TrimSpace(mt)) {
	case "tv", "episode", "series":
		return true
	default:
		return false
	}
}

func (m *Module) lookup() LibraryLookup {
	if m.testLookup != nil {
		return m.testLookup
	}
	m.peerMu.RLock()
	mc := m.mc
	m.peerMu.RUnlock()
	if mc != nil {
		return newMeshLookup(mc)
	}
	return noopLookup{}
}

func (m *Module) resolveCanonicalMovie(ctx context.Context, movieID string, tmdbID int32) (canonical string, aliases, genres []string) {
	canonical = strings.TrimSpace(movieID)
	if tmdbID > 0 {
		aliases = append(aliases, tmdbMovieAlias(tmdbID))
	}
	if canonical == "" && tmdbID > 0 {
		if id, g, ok := m.lookup().LookupMovieByTMDB(ctx, tmdbID); ok {
			canonical = id
			genres = g
		}
	}
	if canonical == "" && len(aliases) > 0 {
		canonical = aliases[0]
	}
	if canonical != "" && len(genres) == 0 {
		if g, err := m.lookup().GetMovieGenres(ctx, canonical); err == nil {
			genres = g
		}
	}
	return canonical, aliases, genres
}

func (m *Module) resolveCanonicalTV(ctx context.Context, seriesID string, tmdbID int32) (canonical string, aliases, genres []string) {
	canonical = strings.TrimSpace(seriesID)
	if tmdbID > 0 {
		aliases = append(aliases, tmdbTVAlias(tmdbID))
	}
	if canonical == "" && tmdbID > 0 {
		if id, g, ok := m.lookup().LookupTVByTMDB(ctx, tmdbID); ok {
			canonical = id
			genres = g
		}
	}
	if canonical == "" && len(aliases) > 0 {
		canonical = aliases[0]
	}
	if canonical != "" && len(genres) == 0 {
		if g, err := m.lookup().GetTVGenres(ctx, canonical); err == nil {
			genres = g
		}
	}
	return canonical, aliases, genres
}

func (m *Module) classifyWithAliases(ctx context.Context, canonical string, aliases []string, in ClassifyInput) (mediaID string, tags []*Tag, matched []string, err error) {
	for _, alias := range aliases {
		if regErr := m.store.RegisterAlias(ctx, alias, canonical); regErr != nil {
			return "", nil, nil, regErr
		}
		if mergeErr := m.store.MergeItemTags(ctx, alias, canonical); mergeErr != nil {
			return "", nil, nil, mergeErr
		}
	}
	in.MediaID = canonical
	tags, matched, err = m.store.Classify(ctx, in)
	return canonical, tags, matched, err
}

// ApplyFileImported classifies a media.file.imported event payload (merge=true).
func (m *Module) ApplyFileImported(ctx context.Context, payload []byte) (mediaID string, tags []*Tag, matched []string, err error) {
	var p mediacontracts.FileImportedPayload
	if unmarshalErr := json.Unmarshal(payload, &p); unmarshalErr != nil {
		return "", nil, nil, fmt.Errorf("unmarshal file.imported: %w", unmarshalErr)
	}
	rawID := mediaIDFromImport(p)
	if rawID == "" {
		return "", nil, nil, fmt.Errorf("file.imported missing media identity")
	}
	path := firstNonEmpty(p.DestinationPath, p.StorageKey, p.OriginalPath)

	var canonical string
	var aliases []string
	var genres []string
	if p.TMDBID > 0 {
		if isTVMediaType(p.MediaType) {
			canonical, aliases, genres = m.resolveCanonicalTV(ctx, "", p.TMDBID)
		} else {
			canonical, aliases, genres = m.resolveCanonicalMovie(ctx, "", p.TMDBID)
		}
	}
	if canonical == "" {
		canonical = rawID
	}
	aliases = appendUnique(aliases, rawID)

	return m.classifyWithAliases(ctx, canonical, aliases, ClassifyInput{
		MediaID:   canonical,
		Title:     p.Title,
		Path:      path,
		MediaType: p.MediaType,
		Genres:    genres,
		Merge:     true,
	})
}

// ApplyLibraryEvent applies classification for library add/update events.
func (m *Module) ApplyLibraryEvent(ctx context.Context, eventType string, payload []byte) (mediaID string, tags []*Tag, matched []string, err error) {
	switch eventType {
	case mediacontracts.EventMovieAdded, mediacontracts.EventMovieUpdated:
		var p mediacontracts.MovieAddedPayload
		if unmarshalErr := json.Unmarshal(payload, &p); unmarshalErr != nil {
			return "", nil, nil, unmarshalErr
		}
		canonical, aliases, genres := m.resolveCanonicalMovie(ctx, p.MovieID, p.TMDBID)
		if canonical == "" {
			return "", nil, nil, fmt.Errorf("movie event missing id")
		}
		return m.classifyWithAliases(ctx, canonical, aliases, ClassifyInput{
			MediaID: canonical, Title: p.Title, MediaType: "movie", Genres: genres, Merge: true,
		})
	case mediacontracts.EventTVAdded, mediacontracts.EventTVUpdated:
		var p mediacontracts.TVAddedPayload
		if unmarshalErr := json.Unmarshal(payload, &p); unmarshalErr != nil {
			return "", nil, nil, unmarshalErr
		}
		canonical, aliases, genres := m.resolveCanonicalTV(ctx, p.SeriesID, p.TMDBID)
		if canonical == "" {
			return "", nil, nil, fmt.Errorf("tv event missing id")
		}
		return m.classifyWithAliases(ctx, canonical, aliases, ClassifyInput{
			MediaID: canonical, Title: p.Name, MediaType: "tv", Genres: genres, Merge: true,
		})
	case mediacontracts.EventFileImported:
		return m.ApplyFileImported(ctx, payload)
	default:
		return "", nil, nil, fmt.Errorf("unsupported event type %q", eventType)
	}
}

// ApplyRemovedEvent deletes item tag assignments when library items are removed.
func (m *Module) ApplyRemovedEvent(ctx context.Context, eventType string, payload []byte) error {
	switch eventType {
	case mediacontracts.EventMovieRemoved:
		var p mediacontracts.MovieRemovedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		aliases := []string{}
		if p.TMDBID > 0 {
			aliases = append(aliases, tmdbMovieAlias(p.TMDBID))
		}
		return m.store.DeleteItemTagsForMedia(ctx, p.MovieID, aliases...)
	case mediacontracts.EventTVRemoved:
		var p mediacontracts.TVRemovedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return err
		}
		aliases := []string{}
		if p.TMDBID > 0 {
			aliases = append(aliases, tmdbTVAlias(p.TMDBID))
		}
		return m.store.DeleteItemTagsForMedia(ctx, p.SeriesID, aliases...)
	default:
		return fmt.Errorf("unsupported removal event %q", eventType)
	}
}

func appendUnique(list []string, vals ...string) []string {
	seen := map[string]struct{}{}
	for _, v := range list {
		seen[v] = struct{}{}
	}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		list = append(list, v)
	}
	return list
}

var subscribeEventTypes = []string{
	mediacontracts.EventFileImported,
	mediacontracts.EventMovieAdded,
	mediacontracts.EventMovieUpdated,
	mediacontracts.EventMovieRemoved,
	mediacontracts.EventTVAdded,
	mediacontracts.EventTVUpdated,
	mediacontracts.EventTVRemoved,
}

func (m *Module) eventsEnabledSnapshot() bool {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.eventsEnabled
}

func (m *Module) startEventSubscribe(ctx context.Context) {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	if m.eventLoopCancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	m.eventLoopCancel = cancel
	go m.eventLoop(loopCtx)
}

func (m *Module) stopEventSubscribe() {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	if m.eventLoopCancel != nil {
		m.eventLoopCancel()
		m.eventLoopCancel = nil
	}
	m.subMu.Lock()
	for _, c := range m.subCancels {
		c()
	}
	m.subCancels = nil
	m.subMu.Unlock()
}

func (m *Module) updateEventsEnabled(enabled bool) error {
	m.cfgMu.Lock()
	if m.eventsEnabled == enabled {
		m.cfgMu.Unlock()
		return nil
	}
	m.eventsEnabled = enabled
	startCtx := m.startCtx
	m.cfgMu.Unlock()
	if enabled && startCtx != nil {
		m.startEventSubscribe(startCtx)
	} else if !enabled {
		m.stopEventSubscribe()
	}
	return nil
}

func (m *Module) eventLoop(ctx context.Context) {
	backoff := 8 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if !m.eventsEnabledSnapshot() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if err := m.connectAndSubscribe(ctx); err != nil {
			slog.Warn("media-tagging: event subscribe cycle failed", "error", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
			if backoff > 2*time.Minute {
				backoff = 2 * time.Minute
			}
		}
	}
}

func (m *Module) connectAndSubscribe(ctx context.Context) error {
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
		return fmt.Errorf("dial core: %w", err)
	}
	m.peerMu.Lock()
	m.mc = c
	m.peerMu.Unlock()

	for _, et := range subscribeEventTypes {
		ch, cancel, err := c.Events.Subscribe(ctx, et)
		if err != nil {
			slog.Warn("media-tagging: subscribe", "type", et, "error", err)
			cancel()
			continue
		}
		m.subMu.Lock()
		m.subCancels = append(m.subCancels, cancel)
		m.subMu.Unlock()
		go func(eventType string, events <-chan *eventsv1.Event, unsub context.CancelFunc) {
			defer unsub()
			m.handleTagEvents(ctx, eventType, events)
		}(et, ch, cancel)
		slog.Info("media-tagging: subscribed", "type", et)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (m *Module) handleTagEvents(ctx context.Context, eventType string, ch <-chan *eventsv1.Event) {
	for evt := range ch {
		if ctx.Err() != nil {
			return
		}
		switch eventType {
		case mediacontracts.EventMovieRemoved, mediacontracts.EventTVRemoved:
			if err := m.ApplyRemovedEvent(ctx, eventType, evt.GetPayload()); err != nil {
				slog.Debug("media-tagging: apply removal", "type", eventType, "error", err)
			}
		default:
			mediaID, tags, matched, err := m.ApplyLibraryEvent(ctx, eventType, evt.GetPayload())
			if err != nil {
				slog.Debug("media-tagging: apply event", "type", eventType, "error", err)
				continue
			}
			slog.Info("media-tagging: applied rules",
				"type", eventType, "media_id", mediaID, "tags", len(tags), "matched_rules", len(matched))
		}
	}
}
