package inspector

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"hgdb_monitor/core/internal/datasource"
)

// TestLockWaitsIntegration verifies the complete PostgreSQL blocking-chain query
// without changing persistent database objects. It runs only when the HGDB test
// connection environment variables are supplied explicitly.
func TestLockWaitsIntegration(t *testing.T) {
	host := os.Getenv("HGDB_TEST_HOST")
	password := os.Getenv("HGDB_TEST_PASSWORD")
	if host == "" || password == "" {
		t.Skip("HGDB integration connection is not configured")
	}
	port, err := strconv.ParseUint(os.Getenv("HGDB_TEST_PORT"), 10, 16)
	if err != nil {
		t.Fatalf("invalid HGDB_TEST_PORT: %v", err)
	}
	config := datasource.TestConfig{
		Host: host, Port: uint16(port), Database: os.Getenv("HGDB_TEST_DATABASE"),
		User: os.Getenv("HGDB_TEST_USER"), Password: password, SSLMode: "prefer",
		ConnectTimeout: 5,
	}

	testCtx, cancelTest := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelTest()
	holder, _, err := datasource.Open(testCtx, config)
	if err != nil {
		t.Fatalf("open lock holder: %v", err)
	}
	defer holder.Close(context.Background())
	waiter, _, err := datasource.Open(testCtx, config)
	if err != nil {
		t.Fatalf("open lock waiter: %v", err)
	}
	defer waiter.Close(context.Background())

	var holderPID, waiterPID int32
	if err := holder.QueryRow(testCtx, "select pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("read holder pid: %v", err)
	}
	if err := waiter.QueryRow(testCtx, "select pg_backend_pid()").Scan(&waiterPID); err != nil {
		t.Fatalf("read waiter pid: %v", err)
	}
	const advisoryKey int64 = 7_604_211_913
	if _, err := holder.Exec(testCtx, "select pg_advisory_lock($1)", advisoryKey); err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}
	defer holder.Exec(context.Background(), "select pg_advisory_unlock($1)", advisoryKey) //nolint:errcheck

	waitCtx, cancelWait := context.WithCancel(testCtx)
	defer cancelWait()
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := waiter.Exec(waitCtx, "select pg_advisory_lock($1)", advisoryKey)
		waitDone <- waitErr
	}()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		items, queryErr := LockWaits(testCtx, config, 10)
		if queryErr != nil {
			t.Fatalf("query lock waits: %v", queryErr)
		}
		for _, item := range items {
			if item.BlockedPID == waiterPID && item.BlockingPID == holderPID {
				if item.LockType != "advisory" || item.RequestedMode != "ExclusiveLock" {
					t.Fatalf("unexpected lock metadata: %#v", item)
				}
				cancelWait()
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancelWait()
	select {
	case waitErr := <-waitDone:
		t.Fatalf("blocking query ended before detection: %v", waitErr)
	default:
		t.Fatalf("expected blocking pair holder=%d waiter=%d was not returned", holderPID, waiterPID)
	}
}
