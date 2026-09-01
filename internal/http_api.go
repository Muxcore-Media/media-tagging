package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (m *Module) registerTaggingHTTPAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tags", m.handleListTagsHTTP)
	mux.HandleFunc("POST /api/tags", m.handleCreateTagHTTP)
	mux.HandleFunc("DELETE /api/tags/{id}", m.handleDeleteTagHTTP)
	mux.HandleFunc("GET /api/items/{media_id}/tags", m.handleGetItemTagsHTTP)
	mux.HandleFunc("PUT /api/items/{media_id}/tags", m.handleSetItemTagsHTTP)
	mux.HandleFunc("POST /api/classify", m.handleClassifyHTTP)
	mux.HandleFunc("GET /api/rules", m.handleListRulesHTTP)
	mux.HandleFunc("POST /api/rules", m.handleUpsertRuleHTTP)
	mux.HandleFunc("DELETE /api/rules/{id}", m.handleDeleteRuleHTTP)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if isNotFound(err) {
		http.Error(w, fmtJSONError(err), http.StatusNotFound)
		return
	}
	http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
}

func (m *Module) handleListTagsHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	items, err := m.store.ListTags(r.Context(), r.URL.Query().Get("category"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]tagJSON, 0, len(items))
	for _, t := range items {
		out = append(out, toTagJSON(t))
	}
	writeJSON(w, out)
}

func (m *Module) handleCreateTagHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Color    string `json:"color"`
	}
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}
	cat := req.Category
	if cat == "" {
		m.cfgMu.RLock()
		cat = m.defaultCategory
		m.cfgMu.RUnlock()
	}
	t, err := m.store.CreateTag(r.Context(), Tag{Name: req.Name, Category: cat, Color: req.Color})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, toTagJSON(t))
}

func (m *Module) handleDeleteTagHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	if err := m.store.DeleteTag(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (m *Module) handleGetItemTagsHTTP(w http.ResponseWriter, r *http.Request) {
	mediaID := r.PathValue("media_id")
	if mediaID == "" {
		http.Error(w, `{"error":"media_id required"}`, http.StatusBadRequest)
		return
	}
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	items, err := m.store.GetItemTags(r.Context(), mediaID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]tagJSON, 0, len(items))
	for _, t := range items {
		out = append(out, toTagJSON(t))
	}
	writeJSON(w, map[string]any{"media_id": mediaID, "tags": out})
}

func (m *Module) handleSetItemTagsHTTP(w http.ResponseWriter, r *http.Request) {
	mediaID := r.PathValue("media_id")
	if mediaID == "" {
		http.Error(w, `{"error":"media_id required"}`, http.StatusBadRequest)
		return
	}
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req struct {
		TagIDs []string `json:"tag_ids"`
	}
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	ids, err := m.store.SetItemTags(r.Context(), mediaID, req.TagIDs)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, map[string]any{"media_id": mediaID, "tag_ids": ids})
}

func (m *Module) handleClassifyHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req struct { //nolint:govet // fieldalignment: JSON field order for classify request
		MediaID   string   `json:"media_id"`
		Title     string   `json:"title"`
		Genres    []string `json:"genres"`
		Path      string   `json:"path"`
		MediaType string   `json:"media_type"`
		Merge     bool     `json:"merge"`
	}
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.MediaID) == "" {
		http.Error(w, `{"error":"media_id required"}`, http.StatusBadRequest)
		return
	}
	tags, matched, err := m.store.Classify(r.Context(), ClassifyInput{
		MediaID: req.MediaID, Title: req.Title, Genres: req.Genres,
		Path: req.Path, MediaType: req.MediaType, Merge: req.Merge,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]tagJSON, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagJSON(t))
	}
	writeJSON(w, map[string]any{
		"media_id": req.MediaID, "tags": out, "matched_rule_ids": matched,
	})
}

func (m *Module) handleListRulesHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	items, err := m.store.ListRules(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]ruleJSON, 0, len(items))
	for _, rule := range items {
		out = append(out, toRuleJSON(rule))
	}
	writeJSON(w, out)
}

func (m *Module) handleUpsertRuleHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var req struct { //nolint:govet // fieldalignment: JSON field order for API requests
		ID      string `json:"id"`
		TagID   string `json:"tag_id"`
		Field   string `json:"field"`
		Match   string `json:"match"`
		Pattern string `json:"pattern"`
		Enabled *bool  `json:"enabled"`
	}
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.TagID) == "" || strings.TrimSpace(req.Pattern) == "" {
		http.Error(w, `{"error":"tag_id and pattern required"}`, http.StatusBadRequest)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	field := req.Field
	if field == "" {
		field = "title"
	}
	match := req.Match
	if match == "" {
		match = "contains"
	}
	rule, err := m.store.UpsertRule(r.Context(), Rule{
		ID: req.ID, TagID: req.TagID, Field: field,
		Match: match, Pattern: req.Pattern, Enabled: enabled,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, toRuleJSON(rule))
}

func (m *Module) handleDeleteRuleHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	if err := m.store.DeleteRule(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

type tagJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Color    string `json:"color"`
}

type ruleJSON struct {
	ID      string `json:"id"`
	TagID   string `json:"tag_id"`
	Field   string `json:"field"`
	Match   string `json:"match"`
	Pattern string `json:"pattern"`
	Enabled bool   `json:"enabled"`
}

func toTagJSON(t *Tag) tagJSON {
	return tagJSON{ID: t.ID, Name: t.Name, Category: t.Category, Color: t.Color}
}

func toRuleJSON(r *Rule) ruleJSON {
	return ruleJSON{
		ID: r.ID, TagID: r.TagID, Field: r.Field,
		Match: r.Match, Pattern: r.Pattern, Enabled: r.Enabled,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func fmtJSONError(err error) string {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(b)
}
