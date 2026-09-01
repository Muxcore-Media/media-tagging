package internal_test

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muxcore-Media/media-tagging/internal"
)

func openTempStore(t *testing.T) (*internal.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tagging.db")
	s, err := internal.OpenStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestClassifyAppliesRules(t *testing.T) {
	s, _ := openTempStore(t)
	anime, err := s.CreateTag(t.Context(), internal.Tag{Name: "Anime", Category: "format", Color: "#88f"})
	if err != nil {
		t.Fatal(err)
	}
	horror, err := s.CreateTag(t.Context(), internal.Tag{Name: "Horror", Category: "genre", Color: "#f44"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRule(t.Context(), internal.Rule{
		TagID: anime.ID, Field: "path", Match: "contains", Pattern: "/anime/", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRule(t.Context(), internal.Rule{
		TagID: horror.ID, Field: "genre", Match: "equals", Pattern: "Horror", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	tags, matched, err := s.Classify(t.Context(), internal.ClassifyInput{
		MediaID: "mv_1", Title: "Example", Genres: []string{"Horror", "Thriller"},
		Path: "/library/anime/Example.mkv", MediaType: "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 2 {
		t.Fatalf("matched=%v", matched)
	}
	if len(tags) != 2 {
		t.Fatalf("tags=%d", len(tags))
	}
	got, err := s.GetItemTags(t.Context(), "mv_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stored=%d", len(got))
	}
}

func TestStorePersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tagging.db")
	s1, err := internal.OpenStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := s1.CreateTag(t.Context(), internal.Tag{Name: "Sci-Fi", Category: "genre", Color: "#0af"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s1.UpsertRule(t.Context(), internal.Rule{
		TagID: tag.ID, Field: "title", Match: "contains", Pattern: "Matrix", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s1.Classify(t.Context(), internal.ClassifyInput{
		MediaID: "mv_matrix", Title: "The Matrix", MediaType: "movie",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := internal.OpenStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	tags, err := s2.ListTags(t.Context(), "genre")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Name != "Sci-Fi" {
		t.Fatalf("tags=%+v", tags)
	}
	rules, err := s2.ListRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("rules=%+v", rules)
	}
	got, err := s2.GetItemTags(t.Context(), "mv_matrix")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Sci-Fi" {
		t.Fatalf("item tags=%+v", got)
	}
}

func TestDeleteTagCascades(t *testing.T) {
	s, _ := openTempStore(t)
	tag, err := s.CreateTag(t.Context(), internal.Tag{Name: "Temp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRule(t.Context(), internal.Rule{
		TagID: tag.ID, Field: "title", Pattern: "x", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetItemTags(t.Context(), "m1", []string{tag.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTag(t.Context(), tag.ID); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListRules(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected rules cleared, got %d", len(rules))
	}
	got, err := s.GetItemTags(t.Context(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected item tags cleared, got %d", len(got))
	}
}

func TestCreateTagUniqueNameCategory(t *testing.T) {
	s, _ := openTempStore(t)
	if _, err := s.CreateTag(t.Context(), internal.Tag{Name: "Horror", Category: "genre"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(t.Context(), internal.Tag{Name: "horror", Category: "genre"}); err == nil {
		t.Fatal("expected duplicate tag error")
	}
}

func TestUpsertRuleValidation(t *testing.T) {
	s, _ := openTempStore(t)
	tag, err := s.CreateTag(t.Context(), internal.Tag{Name: "T"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		rule internal.Rule
		want string
	}{
		{"bad field", internal.Rule{TagID: tag.ID, Field: "rating", Pattern: "x"}, "invalid field"},
		{"bad match", internal.Rule{TagID: tag.ID, Field: "title", Match: "fuzzy", Pattern: "x"}, "invalid match"},
		{"bad regex", internal.Rule{TagID: tag.ID, Field: "title", Match: "regex", Pattern: "(?"}, "invalid regex"},
		{"missing tag", internal.Rule{TagID: "nope", Field: "title", Pattern: "x"}, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.UpsertRule(t.Context(), tc.rule)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

func TestBackupRoundTrip(t *testing.T) {
	off := false
	dir := t.TempDir()
	m := internal.NewModule(internal.Config{
		DataDir:       dir,
		GRPCAddr:      "127.0.0.1:0",
		HTTPAddr:      "127.0.0.1:0",
		EventsEnabled: &off,
	})
	if err := m.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(t.Context()) })

	tagBody := []byte(`{"name":"backup-tag","category":"test"}`)
	resp, err := http.Post("http://"+m.HTTPListenAddr()+"/api/tags", "application/json", bytes.NewReader(tagBody))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create tag %d", resp.StatusCode)
	}

	snap, err := m.ExportState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) == 0 {
		t.Fatal("empty snapshot")
	}

	if err := m.ImportState(t.Context(), snap); err != nil {
		t.Fatal(err)
	}

	listResp, err := http.Get("http://" + m.HTTPListenAddr() + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list tags %d", listResp.StatusCode)
	}
}
