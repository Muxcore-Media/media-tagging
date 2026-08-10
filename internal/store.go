package internal

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type Tag struct {
	ID       string
	Name     string
	Category string
	Color    string
}

type Rule struct {
	ID      string
	TagID   string
	Field   string
	Match   string
	Pattern string
	Enabled bool
}

type Store struct {
	mu       sync.RWMutex
	tags     map[string]*Tag
	rules    map[string]*Rule
	itemTags map[string]map[string]struct{} // media_id -> tag_id set
}

func NewStore() *Store {
	return &Store{
		tags:     map[string]*Tag{},
		rules:    map[string]*Rule{},
		itemTags: map[string]map[string]struct{}{},
	}
}

func (s *Store) CreateTag(t Tag) (*Tag, error) {
	if strings.TrimSpace(t.Name) == "" {
		return nil, fmt.Errorf("tag name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = "tag_" + uuid.NewString()[:8]
	}
	cp := t
	s.tags[cp.ID] = &cp
	out := cp
	return &out, nil
}

func (s *Store) ListTags(category string) []*Tag {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Tag, 0, len(s.tags))
	for _, t := range s.tags {
		if category != "" && !strings.EqualFold(t.Category, category) {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	return out
}

func (s *Store) DeleteTag(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tags[id]; !ok {
		return fmt.Errorf("tag %q not found", id)
	}
	delete(s.tags, id)
	for mid, set := range s.itemTags {
		delete(set, id)
		if len(set) == 0 {
			delete(s.itemTags, mid)
		}
	}
	for rid, r := range s.rules {
		if r.TagID == id {
			delete(s.rules, rid)
		}
	}
	return nil
}

func (s *Store) SetItemTags(mediaID string, tagIDs []string) ([]string, error) {
	if strings.TrimSpace(mediaID) == "" {
		return nil, fmt.Errorf("media_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]struct{}{}
	for _, id := range tagIDs {
		if _, ok := s.tags[id]; !ok {
			return nil, fmt.Errorf("tag %q not found", id)
		}
		set[id] = struct{}{}
	}
	s.itemTags[mediaID] = set
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out, nil
}

func (s *Store) GetItemTags(mediaID string) []*Tag {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.itemTags[mediaID]
	out := make([]*Tag, 0, len(set))
	for id := range set {
		if t, ok := s.tags[id]; ok {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out
}

func (s *Store) UpsertRule(r Rule) (*Rule, error) {
	if strings.TrimSpace(r.TagID) == "" || strings.TrimSpace(r.Pattern) == "" {
		return nil, fmt.Errorf("tag_id and pattern required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tags[r.TagID]; !ok {
		return nil, fmt.Errorf("tag %q not found", r.TagID)
	}
	if r.ID == "" {
		r.ID = "rule_" + uuid.NewString()[:8]
	}
	if r.Field == "" {
		r.Field = "title"
	}
	if r.Match == "" {
		r.Match = "contains"
	}
	cp := r
	s.rules[cp.ID] = &cp
	out := cp
	return &out, nil
}

func (s *Store) ListRules() []*Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Rule, 0, len(s.rules))
	for _, r := range s.rules {
		cp := *r
		out = append(out, &cp)
	}
	return out
}

func (s *Store) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[id]; !ok {
		return fmt.Errorf("rule %q not found", id)
	}
	delete(s.rules, id)
	return nil
}

type ClassifyInput struct {
	MediaID   string
	Title     string
	Genres    []string
	Path      string
	MediaType string
	Merge     bool
}

func (s *Store) Classify(in ClassifyInput) (tags []*Tag, matched []string, err error) {
	if strings.TrimSpace(in.MediaID) == "" {
		return nil, nil, fmt.Errorf("media_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	hit := map[string]struct{}{}
	if in.Merge {
		for id := range s.itemTags[in.MediaID] {
			hit[id] = struct{}{}
		}
	}
	matched = make([]string, 0)
	for _, r := range s.rules {
		if !r.Enabled {
			continue
		}
		if !ruleMatches(r, in) {
			continue
		}
		hit[r.TagID] = struct{}{}
		matched = append(matched, r.ID)
	}
	s.itemTags[in.MediaID] = hit
	tags = make([]*Tag, 0, len(hit))
	for id := range hit {
		if t, ok := s.tags[id]; ok {
			cp := *t
			tags = append(tags, &cp)
		}
	}
	return tags, matched, nil
}

func ruleMatches(r *Rule, in ClassifyInput) bool {
	var haystack string
	switch strings.ToLower(r.Field) {
	case "title":
		haystack = in.Title
	case "path":
		haystack = in.Path
	case "media_type", "type":
		haystack = in.MediaType
	case "genre":
		for _, g := range in.Genres {
			if fieldMatch(r.Match, g, r.Pattern) {
				return true
			}
		}
		return false
	default:
		haystack = in.Title
	}
	return fieldMatch(r.Match, haystack, r.Pattern)
}

func fieldMatch(mode, value, pattern string) bool {
	switch strings.ToLower(mode) {
	case "equals":
		return strings.EqualFold(value, pattern)
	case "prefix":
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(pattern))
	case "regex":
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	default: // contains
		return strings.Contains(strings.ToLower(value), strings.ToLower(pattern))
	}
}
