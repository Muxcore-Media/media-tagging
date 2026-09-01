package internal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mediacontracts "github.com/Muxcore-Media/contracts-media/events"
)

type eventFixture struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func loadEventFixture(t *testing.T, name string) eventFixture {
	t.Helper()
	path := filepath.Join("testdata", "events", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f eventFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	if f.Type == "" || len(f.Payload) == 0 {
		t.Fatalf("empty fixture %s", name)
	}
	return f
}

func newTestModule(t *testing.T) *Module {
	t.Helper()
	return newTestModuleWithLookup(t, nil)
}

func newTestModuleWithLookup(t *testing.T, lookup LibraryLookup) *Module {
	t.Helper()
	off := false
	m := NewModule(Config{
		DataDir:       t.TempDir(),
		GRPCAddr:      "127.0.0.1:0",
		HTTPAddr:      "127.0.0.1:0",
		EventsEnabled: &off,
		Lookup:        lookup,
	})
	if err := m.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	return m
}

func seedAnimeHorrorRules(t *testing.T, m *Module) (animeID, horrorID string) {
	t.Helper()
	anime, err := m.store.CreateTag(t.Context(), Tag{Name: "Anime", Category: "format", Color: "#88f"})
	if err != nil {
		t.Fatal(err)
	}
	horror, err := m.store.CreateTag(t.Context(), Tag{Name: "Horror", Category: "genre", Color: "#f44"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(t.Context(), Rule{
		TagID: anime.ID, Field: "path", Match: "contains", Pattern: "/anime/", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(t.Context(), Rule{
		TagID: anime.ID, Field: "title", Match: "contains", Pattern: "Cowboy", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(t.Context(), Rule{
		TagID: horror.ID, Field: "title", Match: "contains", Pattern: "Thing", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return anime.ID, horror.ID
}

func TestApplyFileImportedFixture(t *testing.T) {
	m := newTestModule(t)
	animeID, _ := seedAnimeHorrorRules(t, m)
	f := loadEventFixture(t, "file_imported_anime.json")
	if f.Type != mediacontracts.EventFileImported {
		t.Fatalf("type=%q", f.Type)
	}

	mediaID, tags, matched, err := m.ApplyFileImported(t.Context(), f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if mediaID != "tmdb:movie:129" {
		t.Fatalf("media_id=%q", mediaID)
	}
	if len(matched) < 1 {
		t.Fatalf("expected path rule match, matched=%v", matched)
	}
	if !hasTagID(tags, animeID) {
		t.Fatalf("expected Anime tag, got %+v", tags)
	}
	stored, err := m.store.GetItemTags(t.Context(), mediaID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagID(stored, animeID) {
		t.Fatalf("persisted tags=%+v", stored)
	}
}

func TestImportThenMovieAddedShareItemTags(t *testing.T) {
	m := newTestModule(t)
	_, horrorID := seedAnimeHorrorRules(t, m)

	imported := loadEventFixture(t, "file_imported_thing.json")
	if _, _, _, err := m.ApplyFileImported(t.Context(), imported.Payload); err != nil {
		t.Fatal(err)
	}
	aliasTags, err := m.store.GetItemTags(t.Context(), "tmdb:movie:1091")
	if err != nil || !hasTagID(aliasTags, horrorID) {
		t.Fatalf("import tags=%+v err=%v", aliasTags, err)
	}

	movie := loadEventFixture(t, "movie_added_horror.json")
	mediaID, tags, _, err := m.ApplyLibraryEvent(t.Context(), movie.Type, movie.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if mediaID != "mv_thing" {
		t.Fatalf("media_id=%q", mediaID)
	}
	if !hasTagID(tags, horrorID) {
		t.Fatalf("movie.added tags=%+v", tags)
	}
	stored, err := m.store.GetItemTags(t.Context(), "mv_thing")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagID(stored, horrorID) {
		t.Fatalf("canonical tags=%+v", stored)
	}
	viaAlias, err := m.store.GetItemTags(t.Context(), "tmdb:movie:1091")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagID(viaAlias, horrorID) {
		t.Fatalf("alias resolve tags=%+v", viaAlias)
	}
}

func TestApplyLibraryEventFixtures(t *testing.T) {
	m := newTestModuleWithLookup(t, &MockLookup{
		MovieGenres: map[string][]string{"mv_thing": {"Horror"}},
		TVGenres:    map[string][]string{"tv_bebop": {"Animation", "Anime"}},
	})
	animeID, horrorID := seedAnimeHorrorRules(t, m)
	if _, err := m.store.UpsertRule(t.Context(), Rule{
		TagID: horrorID, Field: "genre", Match: "equals", Pattern: "Horror", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	movie := loadEventFixture(t, "movie_added_horror.json")
	mediaID, tags, matched, err := m.ApplyLibraryEvent(t.Context(), movie.Type, movie.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if mediaID != "mv_thing" {
		t.Fatalf("movie media_id=%q", mediaID)
	}
	if len(matched) < 1 || !hasTagID(tags, horrorID) {
		t.Fatalf("horror apply: tags=%+v matched=%v", tags, matched)
	}

	tv := loadEventFixture(t, "tv_added_anime.json")
	mediaID, tags, matched, err = m.ApplyLibraryEvent(t.Context(), tv.Type, tv.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if mediaID != "tv_bebop" {
		t.Fatalf("tv media_id=%q", mediaID)
	}
	if len(matched) < 1 || !hasTagID(tags, animeID) {
		t.Fatalf("anime apply: tags=%+v matched=%v", tags, matched)
	}
}

func TestApplyRemovedEventFixtures(t *testing.T) {
	m := newTestModule(t)
	_, horrorID := seedAnimeHorrorRules(t, m)
	movie := loadEventFixture(t, "movie_added_horror.json")
	if _, _, _, err := m.ApplyLibraryEvent(t.Context(), movie.Type, movie.Payload); err != nil {
		t.Fatal(err)
	}
	if tags, _ := m.store.GetItemTags(t.Context(), "mv_thing"); len(tags) == 0 {
		t.Fatal("expected tags before removal")
	}

	removed := loadEventFixture(t, "movie_removed_thing.json")
	if err := m.ApplyRemovedEvent(t.Context(), removed.Type, removed.Payload); err != nil {
		t.Fatal(err)
	}
	stored, err := m.store.GetItemTags(t.Context(), "mv_thing")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected tags cleared, got %+v", stored)
	}
	_ = horrorID

	tvAdded := loadEventFixture(t, "tv_added_anime.json")
	if _, _, _, err := m.ApplyLibraryEvent(t.Context(), tvAdded.Type, tvAdded.Payload); err != nil {
		t.Fatal(err)
	}
	tvRemoved := loadEventFixture(t, "tv_removed_anime.json")
	if err := m.ApplyRemovedEvent(t.Context(), tvRemoved.Type, tvRemoved.Payload); err != nil {
		t.Fatal(err)
	}
	stored, err = m.store.GetItemTags(t.Context(), "tv_bebop")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected tv tags cleared, got %+v", stored)
	}
}

func TestMediaIDFromImport(t *testing.T) {
	if got := mediaIDFromImport(mediacontracts.FileImportedPayload{
		MediaType: "movie", TMDBID: 550, Title: "Fight Club",
	}); got != "tmdb:movie:550" {
		t.Fatalf("got %q", got)
	}
	if got := mediaIDFromImport(mediacontracts.FileImportedPayload{
		MediaType: "tv", TMDBID: 1396, SeasonNumber: 1, EpisodeNumber: 2,
	}); got != "tmdb:tv:1396:s01e02" {
		t.Fatalf("got %q", got)
	}
	if got := mediaIDFromImport(mediacontracts.FileImportedPayload{
		Title: "Local", DestinationPath: "/lib/Local.mkv",
	}); got != "path:/lib/Local.mkv" {
		t.Fatalf("got %q", got)
	}
}

func TestEventsEnabledToggle(t *testing.T) {
	off := false
	m := NewModule(Config{
		DataDir:       t.TempDir(),
		GRPCAddr:      "127.0.0.1:0",
		HTTPAddr:      "127.0.0.1:0",
		EventsEnabled: &off,
	})
	if err := m.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := contextWithCancel(t)
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = m.Stop(t.Context()) })

	if err := m.UpdateSetting("events_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	if !m.eventsEnabledSnapshot() {
		t.Fatal("expected events enabled")
	}
	m.eventMu.Lock()
	started := m.eventLoopCancel != nil
	m.eventMu.Unlock()
	if !started {
		t.Fatal("expected event loop started")
	}

	if err := m.UpdateSetting("events_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if m.eventsEnabledSnapshot() {
		t.Fatal("expected events disabled")
	}
	m.eventMu.Lock()
	stopped := m.eventLoopCancel == nil
	m.eventMu.Unlock()
	if !stopped {
		t.Fatal("expected event loop stopped")
	}
}

func hasTagID(tags []*Tag, id string) bool {
	for _, t := range tags {
		if t.ID == id {
			return true
		}
	}
	return false
}

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(t.Context())
}
