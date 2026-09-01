package internal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"github.com/Muxcore-Media/media-tagging/internal"
)

func TestMeshAndHTTPRulesAPI(t *testing.T) {
	off := false
	m := internal.NewModule(internal.Config{
		DataDir:       t.TempDir(),
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

	base := "http://" + m.HTTPListenAddr()

	tagBody, _ := json.Marshal(map[string]string{"name": "anime", "category": "genre", "color": "#f0f"})
	tr, err := http.Post(base+"/api/tags", "application/json", bytes.NewReader(tagBody))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	if tr.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(tr.Body)
		t.Fatalf("create tag status %d: %s", tr.StatusCode, b)
	}
	var tag map[string]any
	if err := json.NewDecoder(tr.Body).Decode(&tag); err != nil {
		t.Fatal(err)
	}
	tagID, _ := tag["id"].(string)
	if tagID == "" {
		t.Fatal("expected tag id")
	}

	enabled := true
	ruleBody, _ := json.Marshal(map[string]any{
		"tag_id": tagID, "field": "title", "match": "contains", "pattern": "naruto", "enabled": enabled,
	})
	rr, err := http.Post(base+"/api/rules", "application/json", bytes.NewReader(ruleBody))
	if err != nil {
		t.Fatal(err)
	}
	defer rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(rr.Body)
		t.Fatalf("create rule status %d: %s", rr.StatusCode, b)
	}

	lr, err := http.Get(base + "/api/rules")
	if err != nil {
		t.Fatal(err)
	}
	defer lr.Body.Close()
	var rules []map[string]any
	if err := json.NewDecoder(lr.Body).Decode(&rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(m.GRPCListenAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	mesh := meshv1.NewModuleMeshClient(conn)
	resp, err := mesh.Call(ctx, &meshv1.CallRequest{
		TargetModule: "media-tagging",
		Method:       "ListRules",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetError() != "" {
		t.Fatalf("mesh error: %s", resp.GetError())
	}
	rules = nil
	if err := json.Unmarshal(resp.GetPayload(), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("mesh expected 1 rule, got %d", len(rules))
	}
}

func TestHealthEndpoints(t *testing.T) {
	off := false
	m := internal.NewModule(internal.Config{
		DataDir:       t.TempDir(),
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

	base := "http://" + m.HTTPListenAddr()
	for _, path := range []string{"/health", "/healthz"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}
}

func TestHTTPItemTagsAndDeleteNotFound(t *testing.T) {
	off := false
	m := internal.NewModule(internal.Config{
		DataDir:       t.TempDir(),
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

	base := "http://" + m.HTTPListenAddr()

	tagBody, _ := json.Marshal(map[string]string{"name": "sci-fi", "category": "genre"})
	tr, err := http.Post(base+"/api/tags", "application/json", bytes.NewReader(tagBody))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Body.Close()
	var tag map[string]any
	if err := json.NewDecoder(tr.Body).Decode(&tag); err != nil {
		t.Fatal(err)
	}
	tagID := tag["id"].(string)

	setBody, _ := json.Marshal(map[string]any{"tag_ids": []string{tagID}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, base+"/api/items/mv_1/tags", bytes.NewReader(setBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	setResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer setResp.Body.Close()
	if setResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(setResp.Body)
		t.Fatalf("set tags status %d: %s", setResp.StatusCode, b)
	}

	getResp, err := http.Get(base + "/api/items/mv_1/tags")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get tags status %d", getResp.StatusCode)
	}

	delReq, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, base+"/api/tags/does-not-exist", nil)
	if err != nil {
		t.Fatal(err)
	}
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing tag status=%d", delResp.StatusCode)
	}

	classBody, _ := json.Marshal(map[string]any{
		"media_id": "mv_2", "title": "Blade Runner", "merge": true,
	})
	cr, err := http.Post(base+"/api/classify", "application/json", bytes.NewReader(classBody))
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Body.Close()
	if cr.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(cr.Body)
		t.Fatalf("classify status %d: %s", cr.StatusCode, b)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(m.GRPCListenAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mesh := meshv1.NewModuleMeshClient(conn)

	getPayload, _ := json.Marshal(map[string]string{"media_id": "mv_1"})
	meshResp, err := mesh.Call(ctx, &meshv1.CallRequest{
		TargetModule: "media-tagging",
		Method:       "GetItemTags",
		Payload:      getPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if meshResp.GetError() != "" {
		t.Fatalf("mesh GetItemTags: %s", meshResp.GetError())
	}

	delPayload, _ := json.Marshal(map[string]string{"id": tagID})
	delMesh, err := mesh.Call(ctx, &meshv1.CallRequest{
		TargetModule: "media-tagging",
		Method:       "DeleteTag",
		Payload:      delPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if delMesh.GetError() != "" {
		t.Fatalf("mesh DeleteTag: %s", delMesh.GetError())
	}
}
