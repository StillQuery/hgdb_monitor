package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"hgdb_monitor/core/internal/collector"
	"hgdb_monitor/core/internal/rpc"
	"hgdb_monitor/core/internal/secret"
	"hgdb_monitor/core/internal/service"
	"hgdb_monitor/core/internal/storage"
)

func main() {
	dataDir := flag.String("data-dir", "", "directory for hgdb_monitor local data")
	flag.Parse()
	if *dataDir == "" {
		_, _ = fmt.Fprintln(os.Stderr, "hgdb_monitor core: --data-dir is required")
		os.Exit(2)
	}

	root, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(root)
	defer cancel()

	store, err := storage.Open(ctx, *dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "hgdb_monitor core: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()
	runtime := service.NewRuntime(ctx, store, secret.NewOSStore())
	if err := runtime.Start(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "hgdb_monitor core: %v\n", err)
		os.Exit(1)
	}
	defer runtime.Close()

	registry := collector.NewRegistry()
	if err := collector.RegisterBuiltins(registry); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "hgdb_monitor core: %v\n", err)
		os.Exit(1)
	}

	server := rpc.NewServer(registry, store, runtime, cancel)
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "hgdb_monitor core: %v\n", err)
		os.Exit(1)
	}
}
