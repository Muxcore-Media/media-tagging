package internal

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/pkg/contracts"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	taggingv1 "github.com/Muxcore-Media/media-tagging/proto/gen/muxcore/tagging/v1"
)

type Module struct {
	id, grpcAddr, httpAddr string
	defaultCategory        string
	cfgMu                  sync.RWMutex
	store                  *Store
	grpcSrv                *grpc.Server
	lis                    net.Listener
	httpSrv                *http.Server
}

type Config struct {
	ID, DefaultCategory, GRPCAddr, HTTPAddr string
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "media-tagging"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = ":9700"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":9701"
	}
	if cfg.DefaultCategory == "" {
		cfg.DefaultCategory = "general"
	}
	if v := os.Getenv("TAGGING_DEFAULT_CATEGORY"); v != "" {
		cfg.DefaultCategory = v
	}
	if v := os.Getenv("MUXCORE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	return &Module{
		id: cfg.ID, grpcAddr: cfg.GRPCAddr, httpAddr: cfg.HTTPAddr,
		defaultCategory: cfg.DefaultCategory, store: NewStore(),
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID: m.id, Name: "Content Tagging", Version: "0.1.0",
		Roles:        []string{"media", "tagging"},
		Description:  "Content tagging and rule-based classification (scaffold)",
		Capabilities: []string{"media.tagging", "tagging", "classification", "settings"},
		HTTPAddr:     m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error { return nil }

func (m *Module) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.lis = lis
	m.grpcSrv = grpc.NewServer()
	taggingv1.RegisterTaggingServiceServer(m.grpcSrv, &tagServer{m: m})
	modulesdk.RegisterSettings(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("tagging gRPC listening", "addr", m.grpcAddr)
		if err := m.grpcSrv.Serve(lis); err != nil {
			slog.Error("gRPC serve", "error", err)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	m.httpSrv = &http.Server{Addr: m.httpAddr, Handler: mux}
	go func() {
		slog.Info("health listening", "addr", m.httpAddr)
		if err := m.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health serve", "error", err)
		}
	}()
	return nil
}

func (m *Module) Stop(ctx context.Context) error {
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpSrv != nil {
		_ = m.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (m *Module) Health(ctx context.Context) error { return nil }

type tagServer struct {
	taggingv1.UnimplementedTaggingServiceServer
	m *Module
}

func (s *tagServer) CreateTag(_ context.Context, req *taggingv1.CreateTagRequest) (*taggingv1.CreateTagResponse, error) {
	cat := req.GetCategory()
	if cat == "" {
		s.m.cfgMu.RLock()
		cat = s.m.defaultCategory
		s.m.cfgMu.RUnlock()
	}
	t, err := s.m.store.CreateTag(Tag{Name: req.GetName(), Category: cat, Color: req.GetColor()})
	if err != nil {
		return nil, err
	}
	return &taggingv1.CreateTagResponse{Tag: toPBTag(t)}, nil
}

func (s *tagServer) ListTags(_ context.Context, req *taggingv1.ListTagsRequest) (*taggingv1.ListTagsResponse, error) {
	items := s.m.store.ListTags(req.GetCategory())
	out := make([]*taggingv1.Tag, 0, len(items))
	for _, t := range items {
		out = append(out, toPBTag(t))
	}
	return &taggingv1.ListTagsResponse{Tags: out}, nil
}

func (s *tagServer) DeleteTag(_ context.Context, req *taggingv1.DeleteTagRequest) (*taggingv1.DeleteTagResponse, error) {
	if err := s.m.store.DeleteTag(req.GetId()); err != nil {
		return nil, err
	}
	return &taggingv1.DeleteTagResponse{Success: true}, nil
}

func (s *tagServer) SetItemTags(_ context.Context, req *taggingv1.SetItemTagsRequest) (*taggingv1.SetItemTagsResponse, error) {
	ids, err := s.m.store.SetItemTags(req.GetMediaId(), req.GetTagIds())
	if err != nil {
		return nil, err
	}
	return &taggingv1.SetItemTagsResponse{TagIds: ids}, nil
}

func (s *tagServer) GetItemTags(_ context.Context, req *taggingv1.GetItemTagsRequest) (*taggingv1.GetItemTagsResponse, error) {
	items := s.m.store.GetItemTags(req.GetMediaId())
	out := make([]*taggingv1.Tag, 0, len(items))
	for _, t := range items {
		out = append(out, toPBTag(t))
	}
	return &taggingv1.GetItemTagsResponse{MediaId: req.GetMediaId(), Tags: out}, nil
}

func (s *tagServer) UpsertRule(_ context.Context, req *taggingv1.UpsertRuleRequest) (*taggingv1.UpsertRuleResponse, error) {
	r, err := s.m.store.UpsertRule(Rule{
		ID: req.GetId(), TagID: req.GetTagId(), Field: req.GetField(),
		Match: req.GetMatch(), Pattern: req.GetPattern(), Enabled: req.GetEnabled(),
	})
	if err != nil {
		return nil, err
	}
	return &taggingv1.UpsertRuleResponse{Rule: toPBRule(r)}, nil
}

func (s *tagServer) ListRules(_ context.Context, _ *taggingv1.ListRulesRequest) (*taggingv1.ListRulesResponse, error) {
	items := s.m.store.ListRules()
	out := make([]*taggingv1.ClassificationRule, 0, len(items))
	for _, r := range items {
		out = append(out, toPBRule(r))
	}
	return &taggingv1.ListRulesResponse{Rules: out}, nil
}

func (s *tagServer) DeleteRule(_ context.Context, req *taggingv1.DeleteRuleRequest) (*taggingv1.DeleteRuleResponse, error) {
	if err := s.m.store.DeleteRule(req.GetId()); err != nil {
		return nil, err
	}
	return &taggingv1.DeleteRuleResponse{Success: true}, nil
}

func (s *tagServer) Classify(_ context.Context, req *taggingv1.ClassifyRequest) (*taggingv1.ClassifyResponse, error) {
	tags, matched, err := s.m.store.Classify(ClassifyInput{
		MediaID: req.GetMediaId(), Title: req.GetTitle(), Genres: req.GetGenres(),
		Path: req.GetPath(), MediaType: req.GetMediaType(), Merge: req.GetMerge(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*taggingv1.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, toPBTag(t))
	}
	return &taggingv1.ClassifyResponse{MediaId: req.GetMediaId(), Tags: out, MatchedRuleIds: matched}, nil
}

func toPBTag(t *Tag) *taggingv1.Tag {
	return &taggingv1.Tag{Id: t.ID, Name: t.Name, Category: t.Category, Color: t.Color}
}

func toPBRule(r *Rule) *taggingv1.ClassificationRule {
	return &taggingv1.ClassificationRule{
		Id: r.ID, TagId: r.TagID, Field: r.Field, Match: r.Match, Pattern: r.Pattern, Enabled: r.Enabled,
	}
}
