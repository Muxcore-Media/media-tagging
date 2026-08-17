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
	mux.HandleFunc("GET /api/rules", m.handleListRulesHTTP)
	mux.HandleFunc("POST /api/rules", m.handleUpsertRuleHTTP)
	mux.HandleFunc("DELETE /api/rules/{id}", m.handleDeleteRuleHTTP)
}

func (m *Module) handleListTagsHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	items, err := m.store.ListTags(r.URL.Query().Get("category"))
	if err != nil {
		http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
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
	if err := json.Unmarshal(body, &req); err != nil {
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
	t, err := m.store.CreateTag(Tag{Name: req.Name, Category: cat, Color: req.Color})
	if err != nil {
		http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, toTagJSON(t))
}

func (m *Module) handleListRulesHTTP(w http.ResponseWriter, r *http.Request) {
	if m.store == nil {
		http.Error(w, `{"error":"store not open"}`, http.StatusServiceUnavailable)
		return
	}
	items, err := m.store.ListRules()
	if err != nil {
		http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
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
	var req struct {
		ID      string `json:"id"`
		TagID   string `json:"tag_id"`
		Field   string `json:"field"`
		Match   string `json:"match"`
		Pattern string `json:"pattern"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
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
	rule, err := m.store.UpsertRule(Rule{
		ID: req.ID, TagID: req.TagID, Field: field,
		Match: match, Pattern: req.Pattern, Enabled: enabled,
	})
	if err != nil {
		http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
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
	if err := m.store.DeleteRule(id); err != nil {
		http.Error(w, fmtJSONError(err), http.StatusInternalServerError)
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
