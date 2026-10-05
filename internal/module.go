package internal

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/sdk/go/client"
	modulesdk "github.com/Muxcore-Media/core/sdk/go/module"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	manifest "github.com/Muxcore-Media/media-tagging"
	taggingv1 "github.com/Muxcore-Media/media-tagging/proto/gen/muxcore/tagging/v1"
)

type Module struct { //nolint:govet // fieldalignment: lifecycle fields grouped for readability
	id, grpcAddr, httpAddr string
	dataDir                string
	defaultCategory        string
	eventsEnabled          bool
	cfgMu                  sync.RWMutex
	store                  *Store
	grpcSrv                *grpc.Server
	lis                    net.Listener
	httpSrv                *http.Server

	startCtx context.Context //nolint:containedctx // module lifecycle ctx for event subscribe

	peerMu     sync.RWMutex
	mc         *client.Client
	testLookup LibraryLookup

	eventMu         sync.Mutex
	eventLoopCancel context.CancelFunc
	subMu           sync.Mutex
	subCancels      []context.CancelFunc
}

type Config struct { //nolint:govet // fieldalignment: config fields grouped for readability
	ID, DefaultCategory, DataDir, GRPCAddr, HTTPAddr string
	EventsEnabled                                    *bool
	Lookup                                           LibraryLookup
}

func NewModule(cfg Config) *Module {
	if cfg.ID == "" {
		cfg.ID = "media-tagging"
	}
	if cfg.GRPCAddr == "" {
		cfg.GRPCAddr = "127.0.0.1:9740"
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:9741"
	}
	if cfg.DefaultCategory == "" {
		cfg.DefaultCategory = "general"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	eventsEnabled := true
	if cfg.EventsEnabled != nil {
		eventsEnabled = *cfg.EventsEnabled
	}
	if v := os.Getenv("TAGGING_DEFAULT_CATEGORY"); v != "" {
		cfg.DefaultCategory = v
	}
	if v := os.Getenv("TAGGING_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("TAGGING_GRPC_ADDR"); v != "" {
		cfg.GRPCAddr = v
	}
	if v := os.Getenv("MUXCORE_HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("TAGGING_EVENTS_ENABLED"); v != "" {
		eventsEnabled = v == "1" || v == "true" || v == "TRUE"
	}
	return &Module{
		id: cfg.ID, grpcAddr: cfg.GRPCAddr, httpAddr: cfg.HTTPAddr,
		dataDir: cfg.DataDir, defaultCategory: cfg.DefaultCategory,
		eventsEnabled: eventsEnabled, testLookup: cfg.Lookup,
	}
}

func (m *Module) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID: m.id, Name: "Content Tagging", Version: modulesdk.ManifestVersion(manifest.ManifestJSON),
		Roles:        []string{"media", "tagging"},
		Description:  "Content tagging and rule-based classification with SQLite persistence",
		Capabilities: []string{"media.tagging", "tagging", "classification", "settings", "backupable"},
		HTTPAddr:     m.grpcAddr,
	}
}

func (m *Module) Init(ctx context.Context) error {
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(m.dataDir, "tagging.db")
	store, err := OpenStore(ctx, dbPath)
	if err != nil {
		return err
	}
	m.store = store
	slog.Info("tagging store open", "db", dbPath)
	return nil
}

func (m *Module) Start(ctx context.Context) error {
	if m.store == nil {
		return fmt.Errorf("store not initialized")
	}
	m.cfgMu.Lock()
	m.startCtx = ctx
	m.cfgMu.Unlock()

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", m.grpcAddr)
	if err != nil {
		return fmt.Errorf("listen gRPC %s: %w", m.grpcAddr, err)
	}
	m.lis = lis
	m.grpcAddr = lis.Addr().String()
	srvOpt, err := meshtls.ServerOption()
	if err != nil {
		return fmt.Errorf("grpc mesh TLS: %w", err)
	}
	m.grpcSrv = grpc.NewServer(srvOpt)
	taggingv1.RegisterTaggingServiceServer(m.grpcSrv, &tagServer{m: m})
	registerTaggingMesh(m.grpcSrv, m.id, m)
	go func() {
		slog.Info("tagging gRPC listening", "addr", m.grpcAddr)
		if serveErr := m.grpcSrv.Serve(lis); serveErr != nil {
			slog.Error("gRPC serve", "error", serveErr)
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", m.handleHealthHTTP)
	mux.HandleFunc("GET /healthz", m.handleHealthHTTP)
	m.registerTaggingHTTPAPI(mux)
	httpLis, err := lc.Listen(ctx, "tcp", m.httpAddr)
	if err != nil {
		return fmt.Errorf("listen HTTP %s: %w", m.httpAddr, err)
	}
	m.httpAddr = httpLis.Addr().String()
	m.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("health listening", "addr", m.httpAddr)
		if serveErr := m.httpSrv.Serve(httpLis); serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("health serve", "error", serveErr)
		}
	}()
	if m.eventsEnabledSnapshot() {
		m.startEventSubscribe(ctx)
	}
	return nil
}

func (m *Module) handleHealthHTTP(w http.ResponseWriter, r *http.Request) {
	if err := m.Health(r.Context()); err != nil {
		http.Error(w, `{"error":"unhealthy"}`, http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// GRPCListenAddr returns the bound gRPC address after Start.
func (m *Module) GRPCListenAddr() string { return m.grpcAddr }

// HTTPListenAddr returns the bound health/HTTP API address after Start.
func (m *Module) HTTPListenAddr() string { return m.httpAddr }

func (m *Module) Stop(ctx context.Context) error {
	m.stopEventSubscribe()
	if m.grpcSrv != nil {
		m.grpcSrv.GracefulStop()
	}
	if m.httpSrv != nil {
		_ = m.httpSrv.Shutdown(ctx)
	}
	if m.store != nil {
		_ = m.store.Close()
		m.store = nil
	}
	return nil
}

func (m *Module) Health(ctx context.Context) error {
	if m.store == nil {
		return fmt.Errorf("store not open")
	}
	return m.store.Ping(ctx)
}

type tagServer struct {
	taggingv1.UnimplementedTaggingServiceServer
	m *Module
}

func grpcStatus(err error) error {
	if isNotFound(err) {
		return status.Error(codes.NotFound, err.Error())
	}
	return err
}

func (s *tagServer) CreateTag(ctx context.Context, req *taggingv1.CreateTagRequest) (*taggingv1.CreateTagResponse, error) {
	cat := req.GetCategory()
	if cat == "" {
		s.m.cfgMu.RLock()
		cat = s.m.defaultCategory
		s.m.cfgMu.RUnlock()
	}
	t, err := s.m.store.CreateTag(ctx, Tag{Name: req.GetName(), Category: cat, Color: req.GetColor()})
	if err != nil {
		return nil, err
	}
	return &taggingv1.CreateTagResponse{Tag: toPBTag(t)}, nil
}

func (s *tagServer) ListTags(ctx context.Context, req *taggingv1.ListTagsRequest) (*taggingv1.ListTagsResponse, error) {
	items, err := s.m.store.ListTags(ctx, req.GetCategory())
	if err != nil {
		return nil, err
	}
	out := make([]*taggingv1.Tag, 0, len(items))
	for _, t := range items {
		out = append(out, toPBTag(t))
	}
	return &taggingv1.ListTagsResponse{Tags: out}, nil
}

func (s *tagServer) DeleteTag(ctx context.Context, req *taggingv1.DeleteTagRequest) (*taggingv1.DeleteTagResponse, error) {
	if err := s.m.store.DeleteTag(ctx, req.GetId()); err != nil {
		return nil, grpcStatus(err)
	}
	return &taggingv1.DeleteTagResponse{Success: true}, nil
}

func (s *tagServer) SetItemTags(ctx context.Context, req *taggingv1.SetItemTagsRequest) (*taggingv1.SetItemTagsResponse, error) {
	ids, err := s.m.store.SetItemTags(ctx, req.GetMediaId(), req.GetTagIds())
	if err != nil {
		return nil, grpcStatus(err)
	}
	return &taggingv1.SetItemTagsResponse{TagIds: ids}, nil
}

func (s *tagServer) GetItemTags(ctx context.Context, req *taggingv1.GetItemTagsRequest) (*taggingv1.GetItemTagsResponse, error) {
	items, err := s.m.store.GetItemTags(ctx, req.GetMediaId())
	if err != nil {
		return nil, err
	}
	out := make([]*taggingv1.Tag, 0, len(items))
	for _, t := range items {
		out = append(out, toPBTag(t))
	}
	return &taggingv1.GetItemTagsResponse{MediaId: req.GetMediaId(), Tags: out}, nil
}

func (s *tagServer) UpsertRule(ctx context.Context, req *taggingv1.UpsertRuleRequest) (*taggingv1.UpsertRuleResponse, error) {
	r, err := s.m.store.UpsertRule(ctx, Rule{
		ID: req.GetId(), TagID: req.GetTagId(), Field: req.GetField(),
		Match: req.GetMatch(), Pattern: req.GetPattern(), Enabled: req.GetEnabled(),
	})
	if err != nil {
		return nil, grpcStatus(err)
	}
	return &taggingv1.UpsertRuleResponse{Rule: toPBRule(r)}, nil
}

func (s *tagServer) ListRules(ctx context.Context, _ *taggingv1.ListRulesRequest) (*taggingv1.ListRulesResponse, error) {
	items, err := s.m.store.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*taggingv1.ClassificationRule, 0, len(items))
	for _, r := range items {
		out = append(out, toPBRule(r))
	}
	return &taggingv1.ListRulesResponse{Rules: out}, nil
}

func (s *tagServer) DeleteRule(ctx context.Context, req *taggingv1.DeleteRuleRequest) (*taggingv1.DeleteRuleResponse, error) {
	if err := s.m.store.DeleteRule(ctx, req.GetId()); err != nil {
		return nil, grpcStatus(err)
	}
	return &taggingv1.DeleteRuleResponse{Success: true}, nil
}

func (s *tagServer) Classify(ctx context.Context, req *taggingv1.ClassifyRequest) (*taggingv1.ClassifyResponse, error) {
	tags, matched, err := s.m.store.Classify(ctx, ClassifyInput{
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
