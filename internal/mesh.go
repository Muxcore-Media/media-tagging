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
	meshMethodListTags   = "ListTags"
	meshMethodCreateTag  = "CreateTag"
	meshMethodListRules  = "ListRules"
	meshMethodUpsertRule = "UpsertRule"
	meshMethodDeleteRule = "DeleteRule"
)

type taggingMeshServer struct {
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
			return &meshv1.CallResponse{Error: err.Error()}, nil
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
			return &meshv1.CallResponse{Error: err.Error()}, nil
		}
		return &meshv1.CallResponse{Payload: []byte(`{"ok":true}`)}, nil
	case meshMethodListTags:
		return s.listTags(req.GetPayload())
	case meshMethodCreateTag:
		return s.createTag(req.GetPayload())
	case meshMethodListRules:
		return s.listRules()
	case meshMethodUpsertRule:
		return s.upsertRule(req.GetPayload())
	case meshMethodDeleteRule:
		return s.deleteRule(req.GetPayload())
	default:
		return &meshv1.CallResponse{Error: fmt.Sprintf("unknown method %q", req.GetMethod())}, nil
	}
}

func (s *taggingMeshServer) StreamCall(stream meshv1.ModuleMesh_StreamCallServer) error {
	return fmt.Errorf("StreamCall not supported for tagging mesh handler")
}

func (s *taggingMeshServer) listTags(payload []byte) (*meshv1.CallResponse, error) {
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
	items, err := s.m.store.ListTags(body.Category)
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	out := make([]tagJSON, 0, len(items))
	for _, t := range items {
		out = append(out, toTagJSON(t))
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) createTag(payload []byte) (*meshv1.CallResponse, error) {
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
	t, err := s.m.store.CreateTag(Tag{Name: body.Name, Category: cat, Color: body.Color})
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	raw, err := json.Marshal(toTagJSON(t))
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) listRules() (*meshv1.CallResponse, error) {
	if s.m.store == nil {
		return &meshv1.CallResponse{Error: "store not open"}, nil
	}
	items, err := s.m.store.ListRules()
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	out := make([]ruleJSON, 0, len(items))
	for _, r := range items {
		out = append(out, toRuleJSON(r))
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) upsertRule(payload []byte) (*meshv1.CallResponse, error) {
	var body struct {
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
	rule, err := s.m.store.UpsertRule(Rule{
		ID: body.ID, TagID: body.TagID, Field: field,
		Match: match, Pattern: body.Pattern, Enabled: enabled,
	})
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	raw, err := json.Marshal(toRuleJSON(rule))
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	return &meshv1.CallResponse{Payload: raw}, nil
}

func (s *taggingMeshServer) deleteRule(payload []byte) (*meshv1.CallResponse, error) {
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
	if err := s.m.store.DeleteRule(body.ID); err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}
	return &meshv1.CallResponse{Payload: []byte(`{"success":true}`)}, nil
}
