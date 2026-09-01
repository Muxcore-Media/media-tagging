package internal

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc"

	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
)

const (
	meshMethodListTags    = "ListTags"
	meshMethodCreateTag   = "CreateTag"
	meshMethodDeleteTag   = "DeleteTag"
	meshMethodGetItemTags = "GetItemTags"
	meshMethodSetItemTags = "SetItemTags"
	meshMethodClassify    = "Classify"
	meshMethodListRules   = "ListRules"
	meshMethodUpsertRule  = "UpsertRule"
	meshMethodDeleteRule  = "DeleteRule"
)

type taggingMeshServer struct { //nolint:govet // fieldalignment: mesh handler fields grouped for readability
	meshv1.UnimplementedModuleMeshServer
	moduleID string
	settings modulesdk.SettingsHandler
	m        *Module
}

func registerTaggingMesh(srv *grpc.Server, moduleID string, m *Module) {
	meshv1.RegisterModuleMeshServer(srv, &taggingMeshServer{
		moduleID: moduleID,
		settings: modulesdk.SettingsHandlerFromProvider(m),
		m:        m,
	})
}

func (s *taggingMeshServer) Call(ctx context.Context, req *meshv1.CallRequest) (*meshv1.CallResponse, error) {
	if req.GetTargetModule() != "" && req.GetTargetModule() != s.moduleID {
		return &meshv1.CallResponse{Error: fmt.Sprintf("wrong target module %q", req.GetTargetModule())}, nil
	}
	switch req.GetMethod() {
	case "Settings":
		if s.settings.List == nil {
			return &meshv1.CallResponse{Error: "settings list not implemented"}, nil
		}
		defs := s.settings.List()
		raw, err := json.Marshal(defs)
		if err != nil {
			return meshCallError(err)
		}
		return &meshv1.CallResponse{Payload: raw}, nil
	case "UpdateSetting":
		if s.settings.Update == nil {
			return &meshv1.CallResponse{Error: "settings update not implemented"}, nil
		}
		var body struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		}
		if len(req.GetPayload()) > 0 {
			if err := json.Unmarshal(req.GetPayload(), &body); err != nil {
				return &meshv1.CallResponse{Error: fmt.Sprintf("invalid UpdateSetting payload: %v", err)}, nil
			}
		}
		if body.Key == "" {
			return &meshv1.CallResponse{Error: "UpdateSetting requires Key"}, nil
		}
		if err := s.settings.Update(body.Key, body.Value); err != nil {
			return meshCallError(err)
		}
		return &meshv1.CallResponse{Payload: []byte(`{"ok":true}`)}, nil
	case meshMethodListTags:
		return s.listTags(ctx, req.GetPayload())
	case meshMethodCreateTag:
		return s.createTag(ctx, req.GetPayload())
	case meshMethodDeleteTag:
		return s.deleteTag(ctx, req.GetPayload())
	case meshMethodGetItemTags:
		return s.getItemTags(ctx, req.GetPayload())
	case meshMethodSetItemTags:
		return s.setItemTags(ctx, req.GetPayload())
	case meshMethodClassify:
		return s.classify(ctx, req.GetPayload())
	case meshMethodListRules:
		return s.listRules(ctx)
	case meshMethodUpsertRule:
		return s.upsertRule(ctx, req.GetPayload())
	case meshMethodDeleteRule:
		return s.deleteRule(ctx, req.GetPayload())
	default:
		return &meshv1.CallResponse{Error: fmt.Sprintf("unknown method %q", req.GetMethod())}, nil
	}
}

func (s *taggingMeshServer) StreamCall(stream meshv1.ModuleMesh_StreamCallServer) error {
	return fmt.Errorf("StreamCall not supported for tagging mesh handler")
}

func meshCallError(err error) (*meshv1.CallResponse, error) {
	return &meshv1.CallResponse{Error: err.Error()}, nil
}

func (s *taggingMeshServer) listTags(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		Category string `json:"category"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			return &meshv1.CallResponse{Error: fmt.Sprintf("invalid ListTags payload: %v", err)}, nil
		}
	}
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	items, err := s.m.store.ListTags(ctx, body.Category)
	if err != nil {
		return meshCallError(err)
	}
	out := make([]tagJSON, 0, len(items))
	for _, t := range items {
		out = append(out, toTagJSON(t))
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) createTag(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Color    string `json:"color"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid CreateTag payload: %v", err)}, nil
	}
	if body.Name == "" {
		return &meshv1.CallResponse{Error: "CreateTag requires name"}, nil
	}
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	cat := body.Category
	if cat == "" {
		s.m.cfgMu.RLock()
		cat = s.m.defaultCategory
		s.m.cfgMu.RUnlock()
	}
	t, err := s.m.store.CreateTag(ctx, Tag{Name: body.Name, Category: cat, Color: body.Color})
	if err != nil {
		return meshCallError(err)
	}
	raw, err := json.Marshal(toTagJSON(t))
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) deleteTag(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid DeleteTag payload: %v", err)}, nil
	}
	if body.ID == "" {
		return &meshv1.CallResponse{Error: "DeleteTag requires id"}, nil
	}
	if err := s.m.store.DeleteTag(ctx, body.ID); err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: []byte(`{"success":true}`)}, nil
}

func (s *taggingMeshServer) getItemTags(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid GetItemTags payload: %v", err)}, nil
	}
	if body.MediaID == "" {
		return &meshv1.CallResponse{Error: "GetItemTags requires media_id"}, nil
	}
	items, err := s.m.store.GetItemTags(ctx, body.MediaID)
	if err != nil {
		return meshCallError(err)
	}
	out := make([]tagJSON, 0, len(items))
	for _, t := range items {
		out = append(out, toTagJSON(t))
	}
	raw, err := json.Marshal(map[string]any{"media_id": body.MediaID, "tags": out})
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) setItemTags(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		MediaID string   `json:"media_id"`
		TagIDs  []string `json:"tag_ids"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid SetItemTags payload: %v", err)}, nil
	}
	if body.MediaID == "" {
		return &meshv1.CallResponse{Error: "SetItemTags requires media_id"}, nil
	}
	ids, err := s.m.store.SetItemTags(ctx, body.MediaID, body.TagIDs)
	if err != nil {
		return meshCallError(err)
	}
	raw, err := json.Marshal(map[string]any{"media_id": body.MediaID, "tag_ids": ids})
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) classify(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct { //nolint:govet // fieldalignment: JSON field order for classify request
		MediaID   string   `json:"media_id"`
		Title     string   `json:"title"`
		Genres    []string `json:"genres"`
		Path      string   `json:"path"`
		MediaType string   `json:"media_type"`
		Merge     bool     `json:"merge"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid Classify payload: %v", err)}, nil
	}
	if body.MediaID == "" {
		return &meshv1.CallResponse{Error: "Classify requires media_id"}, nil
	}
	tags, matched, err := s.m.store.Classify(ctx, ClassifyInput{
		MediaID: body.MediaID, Title: body.Title, Genres: body.Genres,
		Path: body.Path, MediaType: body.MediaType, Merge: body.Merge,
	})
	if err != nil {
		return meshCallError(err)
	}
	out := make([]tagJSON, 0, len(tags))
	for _, t := range tags {
		out = append(out, toTagJSON(t))
	}
	raw, err := json.Marshal(map[string]any{
		"media_id": body.MediaID, "tags": out, "matched_rule_ids": matched,
	})
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) listRules(ctx context.Context) (*meshv1.CallResponse, error) {
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	items, err := s.m.store.ListRules(ctx)
	if err != nil {
		return meshCallError(err)
	}
	out := make([]ruleJSON, 0, len(items))
	for _, r := range items {
		out = append(out, toRuleJSON(r))
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) upsertRule(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct { //nolint:govet // fieldalignment: JSON field order for mesh requests
		ID      string `json:"id"`
		TagID   string `json:"tag_id"`
		Field   string `json:"field"`
		Match   string `json:"match"`
		Pattern string `json:"pattern"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid UpsertRule payload: %v", err)}, nil
	}
	if body.TagID == "" || body.Pattern == "" {
		return &meshv1.CallResponse{Error: "UpsertRule requires tag_id and pattern"}, nil
	}
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	field := body.Field
	if field == "" {
		field = "title"
	}
	match := body.Match
	if match == "" {
		match = "contains"
	}
	rule, err := s.m.store.UpsertRule(ctx, Rule{
		ID: body.ID, TagID: body.TagID, Field: field,
		Match: match, Pattern: body.Pattern, Enabled: enabled,
	})
	if err != nil {
		return meshCallError(err)
	}
	raw, err := json.Marshal(toRuleJSON(rule))
	if err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) deleteRule(ctx context.Context, payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return &meshv1.CallResponse{Error: fmt.Sprintf("invalid DeleteRule payload: %v", err)}, nil
	}
	if body.ID == "" {
		return &meshv1.CallResponse{Error: "DeleteRule requires id"}, nil
	}
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	if err := s.m.store.DeleteRule(ctx, body.ID); err != nil {
		return meshCallError(err)
	}
	return &meshv1.CallResponse{Payload: []byte(`{"success":true}`)}, nil
}
