package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hgdb_monitor/core/internal/collector"
	"hgdb_monitor/core/internal/secret"
	"hgdb_monitor/core/internal/service"
	"hgdb_monitor/core/internal/storage"
)

func TestHandshakeAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	registry := collector.NewRegistry()
	if err := collector.RegisterBuiltins(registry); err != nil {
		t.Fatal(err)
	}
	runtime := service.NewRuntime(ctx, store, secret.NewMemoryStore())
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := NewServer(registry, store, runtime, cancel)
	input := strings.NewReader("{\"id\":\"1\",\"method\":\"system.handshake\",\"params\":{\"protocolVersion\":1}}\n{\"id\":\"2\",\"method\":\"system.shutdown\"}\n")
	var output bytes.Buffer
	if err := server.Serve(ctx, input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two responses, got %q", output.String())
	}
	var response Response
	if err := json.Unmarshal([]byte(lines[0]), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("unexpected handshake error: %#v", response.Error)
	}
}

func TestRequestCancellationRegistry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{requests: make(map[string]context.CancelFunc)}
	if !server.registerRequest("request-one", cancel) {
		t.Fatal("request should register")
	}
	if server.registerRequest("request-one", func() {}) {
		t.Fatal("duplicate request should be rejected")
	}
	if !server.cancelRequest("request-one") {
		t.Fatal("registered request should be cancelled")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("request context was not cancelled")
	}
	server.finishRequest("request-one")
	if server.cancelRequest("request-one") {
		t.Fatal("finished request should not remain registered")
	}
}
