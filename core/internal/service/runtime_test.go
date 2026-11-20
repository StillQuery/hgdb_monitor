package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/model"
	"hgdb_monitor/core/internal/secret"
	"hgdb_monitor/core/internal/storage"
)

func newTestRuntime(t *testing.T, sample time.Duration) (*Runtime, *storage.SQLite, *secret.MemoryStore) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secrets := secret.NewMemoryStore()
	runtime := NewRuntimeWithOptions(ctx, store, secrets, sample, time.Hour, 2*time.Hour,
		func(context.Context, datasource.TestConfig) (datasource.TestResult, error) {
			return datasource.TestResult{Connected: true}, nil
		},
		func(context.Context, datasource.TestConfig) (model.Snapshot, error) {
			now := time.Now().UTC()
			return model.Snapshot{CollectedAt: now, LastCollectedAt: now}, nil
		})
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	t.Cleanup(func() { _ = store.Close() })
	return runtime, store, secrets
}

func validInput(name, password string) CreateInput {
	return CreateInput{DisplayName: name, Host: "localhost", Port: 5432, Database: "postgres", User: "monitor", Password: password, SSLMode: "prefer"}
}

func TestCreateEnforcesLimitAndDoesNotPersistPassword(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime := NewRuntimeWithOptions(ctx, store, secret.NewMemoryStore(), time.Hour, time.Hour, 2*time.Hour,
		func(context.Context, datasource.TestConfig) (datasource.TestResult, error) {
			return datasource.TestResult{Connected: true}, nil
		},
		func(context.Context, datasource.TestConfig) (model.Snapshot, error) { return model.Snapshot{}, nil })
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	const password = "unique-secret-that-must-not-enter-sqlite"
	refs := map[string]bool{}
	for i := 0; i < MaxDatasources; i++ {
		source, err := runtime.Create(ctx, validInput(string(rune('A'+i)), password))
		if err != nil {
			t.Fatal(err)
		}
		if source.InstanceRef == "" || refs[source.InstanceRef] {
			t.Fatal("instance refs must be non-empty and unique")
		}
		refs[source.InstanceRef] = true
	}
	_, err = runtime.Create(ctx, validInput("overflow", password))
	if AsPublicError(err).Code != "DATASOURCE_LIMIT_REACHED" {
		t.Fatalf("expected limit error, got %v", err)
	}
	count, err := store.DatasourceCount(ctx)
	if err != nil || count != MaxDatasources {
		t.Fatalf("unexpected datasource count %d: %v", count, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		contents, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr == nil && strings.Contains(string(contents), password) {
			t.Fatalf("password found in sqlite artifact %s", entry.Name())
		}
	}
}

func TestPersistentCollectionRequiresVerificationAndStops(t *testing.T) {
	runtime, store, _ := newTestRuntime(t, 5*time.Millisecond)
	ctx := context.Background()
	source, err := runtime.Create(ctx, validInput("primary", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SetPersistent(ctx, source.ID, true); AsPublicError(err).Code != "CONNECTION_NOT_VERIFIED" {
		t.Fatalf("expected verification error, got %v", err)
	}
	if _, err := runtime.Test(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SetPersistent(ctx, source.ID, true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		count, countErr := store.SnapshotCount(ctx)
		if countErr != nil {
			t.Fatal(countErr)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("persistent collector did not save a snapshot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := runtime.SetPersistent(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if runtime.Status(source.ID).Running {
		t.Fatal("collector should be stopped")
	}
}

func TestConnectionChangeInvalidatesVerificationAndStopsCollection(t *testing.T) {
	runtime, _, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	source, _ := runtime.Create(ctx, validInput("primary", "secret"))
	_, _ = runtime.Test(ctx, source.ID)
	_, _ = runtime.SetPersistent(ctx, source.ID, true)
	updated, err := runtime.Update(ctx, UpdateInput{ID: source.ID, DisplayName: "primary", Host: "other-host", Port: 5432, Database: "postgres", User: "monitor", SSLMode: "prefer"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.VerifiedAt != nil || updated.PersistentEnabled || runtime.Status(source.ID).Running {
		t.Fatal("connection change must invalidate verification and stop collection")
	}
}

func TestCurrentCollectsWithoutPersisting(t *testing.T) {
	runtime, store, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	source, err := runtime.Create(ctx, validInput("primary", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.Current(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CollectedAt.IsZero() {
		t.Fatal("current collection should return a timestamped snapshot")
	}
	count, err := store.SnapshotCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("current collection must not persist history, got %d rows", count)
	}
}

func TestRuntimeRestoresEnabledJobs(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	secrets := secret.NewMemoryStore()
	first := NewRuntimeWithOptions(ctx, store, secrets, time.Hour, time.Hour, 2*time.Hour,
		func(context.Context, datasource.TestConfig) (datasource.TestResult, error) {
			return datasource.TestResult{Connected: true}, nil
		},
		func(context.Context, datasource.TestConfig) (model.Snapshot, error) { return model.Snapshot{}, nil })
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	source, _ := first.Create(ctx, validInput("primary", "secret"))
	_, _ = first.Test(ctx, source.ID)
	_, _ = first.SetPersistent(ctx, source.ID, true)
	first.Close()
	var runs int32
	second := NewRuntimeWithOptions(ctx, store, secrets, time.Hour, time.Hour, 2*time.Hour,
		func(context.Context, datasource.TestConfig) (datasource.TestResult, error) {
			return datasource.TestResult{Connected: true}, nil
		},
		func(context.Context, datasource.TestConfig) (model.Snapshot, error) {
			atomic.AddInt32(&runs, 1)
			now := time.Now().UTC()
			return model.Snapshot{CollectedAt: now, LastCollectedAt: now}, nil
		})
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&runs) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&runs) == 0 {
		t.Fatal("enabled collection job was not restored")
	}
}

func TestAIQueriesExposeInstanceNameWithoutConnectionDetails(t *testing.T) {
	runtime, store, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	source, err := runtime.Create(ctx, CreateInput{DisplayName: "private-primary", Host: "10.20.30.40", Port: 5432,
		Database: "private_db", User: "private_user", Password: "secret", SSLMode: "prefer"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	first := model.Snapshot{CollectedAt: base, LastCollectedAt: base, SessionsTotal: 2, Commits: 100}
	second := model.Snapshot{CollectedAt: base.Add(30 * time.Second), LastCollectedAt: base.Add(30 * time.Second), SessionsTotal: 6, Commits: 130}
	if err := store.SaveSnapshot(ctx, source.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(ctx, source.ID, second); err != nil {
		t.Fatal(err)
	}
	instances, err := runtime.ListAIInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(instances)
	if len(instances) != 1 || instances[0].InstanceName != "private-primary" {
		t.Fatalf("AI instance list must expose the configured datasource name: %s", encoded)
	}
	for _, private := range []string{"10.20.30.40", "private_db", "private_user", `"host":`, `"port":`, `"user":`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("AI instance output leaked %q: %s", private, encoded)
		}
	}
	summary, err := runtime.AIHistory(ctx, source.InstanceRef, 2, []string{"sessions_total", "commits"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.PointCount != 2 || summary.Metrics["commits"].Increase != 30 || summary.Metrics["commits"].RatePerSecond != 1 {
		t.Fatalf("unexpected history summary: %#v", summary)
	}
	if summary.Metrics["sessions_total"].Average != 4 {
		t.Fatalf("unexpected gauge average: %#v", summary.Metrics["sessions_total"])
	}
}

func TestCounterSummaryHandlesReset(t *testing.T) {
	definition := historyMetrics["commits"]
	base := time.Now().UTC()
	points := []model.Snapshot{
		{CollectedAt: base, Commits: 100},
		{CollectedAt: base.Add(10 * time.Second), Commits: 120},
		{CollectedAt: base.Add(20 * time.Second), Commits: 4},
	}
	result := summarizeMetric(definition, points)
	if result.Increase != 24 || result.RatePerSecond != 1.2 {
		t.Fatalf("unexpected reset-aware summary: %#v", result)
	}
}

func TestAIOverviewCalculatesRatesFromPersistedBaseline(t *testing.T) {
	runtime, store, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	source, err := runtime.Create(ctx, validInput("overview", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-15 * time.Second).Truncate(time.Millisecond)
	previous := model.Snapshot{CollectedAt: base, LastCollectedAt: base, Commits: 100, Rollbacks: 10, BlocksHit: 900, BlocksRead: 100, WALBytes: 1000}
	if err := store.SaveSnapshot(ctx, source.ID, previous); err != nil {
		t.Fatal(err)
	}
	runtime.collect = func(context.Context, datasource.TestConfig) (model.Snapshot, error) {
		at := base.Add(15 * time.Second)
		return model.Snapshot{CollectedAt: at, LastCollectedAt: at, Commits: 130, Rollbacks: 10, BlocksHit: 990, BlocksRead: 110, WALBytes: 1300}, nil
	}
	result, err := runtime.AIOverview(ctx, source.InstanceRef, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deltas == nil {
		t.Fatal("expected delta result")
	}
	if result.Deltas.TransactionsPerSecond != 2 || result.Deltas.WALBytesPerSecond != 20 || result.Deltas.CacheHitRatio != 0.9 {
		t.Fatalf("unexpected overview deltas: %#v", result.Deltas)
	}
}

func TestAIDiagnoseDatabaseReturnsFactsWithoutInventingHealthStatus(t *testing.T) {
	runtime, _, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	source, err := runtime.Create(ctx, validInput("diagnose", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	runtime.collect = func(context.Context, datasource.TestConfig) (model.Snapshot, error) {
		now := time.Now().UTC()
		return model.Snapshot{CollectedAt: now, LastCollectedAt: now, Connected: true, MaxConnections: 100,
			SessionsTotal: 25, SessionsActive: 4, SessionsWaiting: 2, LongQueries: 1,
			LongTransactions: 1, BlockedSessions: 1, LocksNotGranted: 1}, nil
	}
	result, err := runtime.AIDiagnoseDatabase(ctx, source.InstanceRef, "overall", 30, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.CurrentFacts.Connections.UtilizationRatio == nil || *result.CurrentFacts.Connections.UtilizationRatio != 0.25 {
		t.Fatalf("unexpected connection facts: %#v", result.CurrentFacts.Connections)
	}
	if result.AvailableDetails.History || result.ComparisonAvailable || len(result.Observations) != 4 {
		t.Fatalf("unexpected diagnosis result: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"healthScore", "severity", "private", "localhost", `"host"`, `"port"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("diagnosis output contains forbidden or invented field %q: %s", forbidden, encoded)
		}
	}
}

func TestAIDiagnoseDatabaseValidatesFocusAndComparisonWindow(t *testing.T) {
	runtime, _, _ := newTestRuntime(t, time.Hour)
	ctx := context.Background()
	if _, err := runtime.AIDiagnoseDatabase(ctx, "unused", "unknown", 30, true); AsPublicError(err).Code != "INVALID_FOCUS" {
		t.Fatalf("expected INVALID_FOCUS, got %v", err)
	}
	if _, err := runtime.AIDiagnoseDatabase(ctx, "unused", "overall", 61, true); AsPublicError(err).Code != "INVALID_HISTORY_RANGE" {
		t.Fatalf("expected INVALID_HISTORY_RANGE, got %v", err)
	}
}
