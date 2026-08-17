package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
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
	t.Cleanup(func() { _ = m.Stop(t.Context()) })
	return m
}

func seedAnimeHorrorRules(t *testing.T, m *Module) (animeID, horrorID string) {
	t.Helper()
	anime, err := m.store.CreateTag(Tag{Name: "Anime", Category: "format", Color: "#88f"})
	if err != nil {
		t.Fatal(err)
	}
	horror, err := m.store.CreateTag(Tag{Name: "Horror", Category: "genre", Color: "#f44"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(Rule{
		TagID: anime.ID, Field: "path", Match: "contains", Pattern: "/anime/", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(Rule{
		TagID: anime.ID, Field: "title", Match: "contains", Pattern: "Cowboy", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.UpsertRule(Rule{
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
	if f.Type != contracts.EventFileImported {
		t.Fatalf("type=%q", f.Type)
	}

	mediaID, tags, matched, err := m.ApplyFileImported(f.Payload)
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
	stored, err := m.store.GetItemTags(mediaID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasTagID(stored, animeID) {
		t.Fatalf("persisted tags=%+v", stored)
	}
}

func TestApplyLibraryEventFixtures(t *testing.T) {
	m := newTestModule(t)
	animeID, horrorID := seedAnimeHorrorRules(t, m)

	movie := loadEventFixture(t, "movie_added_horror.json")
	mediaID, tags, matched, err := m.ApplyLibraryEvent(movie.Type, movie.Payload)
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
	mediaID, tags, matched, err = m.ApplyLibraryEvent(tv.Type, tv.Payload)
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

func TestMediaIDFromImport(t *testing.T) {
	if got := mediaIDFromImport(contracts.FileImportedPayload{
		MediaType: "movie", TMDBID: 550, Title: "Fight Club",
	}); got != "tmdb:movie:550" {
		t.Fatalf("got %q", got)
	}
	if got := mediaIDFromImport(contracts.FileImportedPayload{
		MediaType: "tv", TMDBID: 1396, SeasonNumber: 1, EpisodeNumber: 2,
	}); got != "tmdb:tv:1396:s01e02" {
		t.Fatalf("got %q", got)
	}
	if got := mediaIDFromImport(contracts.FileImportedPayload{
		Title: "Local", DestinationPath: "/lib/Local.mkv",
	}); got != "path:/lib/Local.mkv" {
		t.Fatalf("got %q", got)
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
