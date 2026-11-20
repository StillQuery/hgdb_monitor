package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/model"
)

func TestOpenCreatesReadyDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if !store.Ready(ctx) {
		t.Fatal("database should be ready")
	}
	if _, err := os.Stat(filepath.Join(dir, "monitor.db")); err != nil {
		t.Fatal(err)
	}
}

func TestDatasourceRoundTripPersistsTimeoutsAndCapabilities(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := model.Datasource{ID: "source_config", InstanceRef: "db_config", DisplayName: "config", Host: "localhost", Port: 5432,
		Database: "postgres", User: "monitor", SSLMode: "prefer", ConnectTimeout: 7, QueryTimeout: 19,
		Capabilities: datasource.Capabilities{Sessions: true, PGStatStatements: true}, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDatasource(ctx, source, 5); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDatasource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectTimeout != 7 || loaded.QueryTimeout != 19 || !loaded.Capabilities.Sessions || !loaded.Capabilities.PGStatStatements {
		t.Fatalf("datasource settings were not persisted: %#v", loaded)
	}
}

func TestListDatasourcesReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	items, err := store.ListDatasources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("expected a non-nil empty datasource list, got %#v", items)
	}
}

func TestSaveSnapshotPersistsWideRow(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	snapshot := model.Snapshot{
		CollectedAt: now, LastCollectedAt: now, PostmasterStart: now.Add(-time.Hour),
		Connected: true, SessionsTotal: 3, Commits: 7, MaxConnections: 100,
		MaxReplayLagBytes: 512, MaxReplayLagBytesAvailable: true,
		MaxReplayLagSeconds: 0.25, MaxReplayLagSecondsAvailable: true,
	}
	if err := store.SaveSnapshot(ctx, "instance_test", snapshot); err != nil {
		t.Fatal(err)
	}
	count, err := store.SnapshotCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one persisted snapshot, got %d", count)
	}
	loaded, err := store.LatestSnapshot(ctx, "instance_test")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.MaxReplayLagSecondsAvailable || loaded.MaxReplayLagSeconds != 0.25 {
		t.Fatalf("replication lag availability was not persisted: %#v", loaded)
	}
	if !loaded.MaxReplayLagBytesAvailable || loaded.MaxReplayLagBytes != 512 {
		t.Fatalf("replication byte lag availability was not persisted: %#v", loaded)
	}
}

func TestSaveSnapshotRejectsInvalidInstanceID(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.SaveSnapshot(ctx, "contains address:5432", model.Snapshot{}); err == nil {
		t.Fatal("expected invalid instance id to be rejected")
	}
}

func TestDeleteSnapshotsBeforeHonorsCutoff(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, collectedAt := range []time.Time{now.Add(-3 * time.Hour), now.Add(-time.Hour)} {
		if err := store.SaveSnapshot(ctx, "instance_test", model.Snapshot{CollectedAt: collectedAt, LastCollectedAt: collectedAt}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.DeleteSnapshotsBefore(ctx, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one expired snapshot deleted, got %d", deleted)
	}
	count, _ := store.SnapshotCount(ctx)
	if count != 1 {
		t.Fatalf("expected one retained snapshot, got %d", count)
	}
}

func TestDeleteSnapshotsBeforeProcessesMoreThanOneBatch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Millisecond)
	for index := 0; index < 1005; index++ {
		at := base.Add(time.Duration(index) * time.Millisecond)
		if err := store.SaveSnapshot(ctx, "instance_batch", model.Snapshot{CollectedAt: at, LastCollectedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.DeleteSnapshotsBefore(ctx, time.Now().UTC().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1005 {
		t.Fatalf("expected 1005 rows deleted in batches, got %d", deleted)
	}
}

func TestInstanceStatusRoundTripAndDatasourceDelete(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := model.Datasource{ID: "source_status", InstanceRef: "db_status", DisplayName: "status", Host: "localhost", Port: 5432,
		Database: "postgres", User: "monitor", SSLMode: "prefer", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDatasource(ctx, source, 5); err != nil {
		t.Fatal(err)
	}
	want := model.InstanceStatus{ObservedAt: now, Connected: true, PostmasterStart: now.Add(-time.Hour), InRecovery: true, MaxConnections: 200}
	if err := store.SaveInstanceStatus(ctx, source.ID, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LatestInstanceStatus(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.InRecovery || got.MaxConnections != 200 || !got.PostmasterStart.Equal(want.PostmasterStart) {
		t.Fatalf("unexpected status: %#v", got)
	}
	if err := store.DeleteDatasource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LatestInstanceStatus(ctx, source.ID); err != ErrStatusNotFound {
		t.Fatalf("status survived datasource deletion: %v", err)
	}
}

func TestDatasourceDeleteAlsoDeletesSnapshots(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := model.Datasource{ID: "source_one", InstanceRef: "db_anonymous", DisplayName: "one", Host: "localhost",
		Port: 5432, Database: "postgres", User: "monitor", SSLMode: "prefer", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDatasource(ctx, source, 5); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(ctx, source.ID, model.Snapshot{CollectedAt: now, LastCollectedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDatasource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	count, _ := store.SnapshotCount(ctx)
	if count != 0 {
		t.Fatalf("expected snapshots removed with datasource, got %d", count)
	}
}
