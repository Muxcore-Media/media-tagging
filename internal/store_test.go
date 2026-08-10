package internal_test

import (
	"testing"

	"github.com/Muxcore-Media/media-tagging/internal"
)

func TestClassifyAppliesRules(t *testing.T) {
	s := internal.NewStore()
	anime, err := s.CreateTag(internal.Tag{Name: "Anime", Category: "format", Color: "#88f"})
	if err != nil {
		t.Fatal(err)
	}
	horror, err := s.CreateTag(internal.Tag{Name: "Horror", Category: "genre", Color: "#f44"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRule(internal.Rule{
		TagID: anime.ID, Field: "path", Match: "contains", Pattern: "/anime/", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertRule(internal.Rule{
		TagID: horror.ID, Field: "genre", Match: "equals", Pattern: "Horror", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	tags, matched, err := s.Classify(internal.ClassifyInput{
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
	got := s.GetItemTags("mv_1")
	if len(got) != 2 {
		t.Fatalf("stored=%d", len(got))
	}
}
