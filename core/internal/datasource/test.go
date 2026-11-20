package datasource

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	defaultConnectTimeout = 5 * time.Second
	defaultQueryTimeout   = 10 * time.Second
	maxConnectTimeout     = 30 * time.Second
	maxQueryTimeout       = 30 * time.Second

	// applicationName is stable for every plugin connection so DBAs can tell
	// collector, inspector and connection-test sessions apart from other clients.
	applicationName = "hgdb_monitor"
)

var allowedSSLModes = map[string]struct{}{
	"disable":     {},
	"allow":       {},
	"prefer":      {},
	"require":     {},
	"verify-ca":   {},
	"verify-full": {},
}

// TestConfig is accepted only by the local core process. Password is never
// included in results or errors returned to the Harness adapter.
type TestConfig struct {
	Host           string `json:"host"`
	Port           uint16 `json:"port"`
	Database       string `json:"database"`
	User           string `json:"user"`
	Password       string `json:"password"`
	SSLMode        string `json:"sslMode"`
	ConnectTimeout int    `json:"connectTimeoutSeconds,omitempty"`
	QueryTimeout   int    `json:"queryTimeoutSeconds,omitempty"`
}

type Capabilities struct {
	PGMonitor        bool `json:"pgMonitor"`
	PGReadAllStats   bool `json:"pgReadAllStats"`
	CitusExtension   bool `json:"citusExtension"`
	PGStatStatements bool `json:"pgStatStatements"`
	Sessions         bool `json:"sessions"`
	Blocking         bool `json:"blocking"`
	WAL              bool `json:"wal"`
	Checkpoint       bool `json:"checkpoint"`
	Replication      bool `json:"replication"`
	TableStats       bool `json:"tableStats"`
}

// TestResult deliberately omits endpoint, username and server version.
type TestResult struct {
	Connected    bool         `json:"connected"`
	InRecovery   bool         `json:"inRecovery"`
	ServerPort   uint16       `json:"serverPort"`
	LatencyMS    int64        `json:"latencyMs"`
	Capabilities Capabilities `json:"capabilities"`
}

type PublicError struct {
	Code    string
	Message string
}

func (e *PublicError) Error() string { return e.Code }

func TestConnection(ctx context.Context, input TestConfig) (TestResult, error) {
	conn, timeout, err := Open(ctx, input)
	if err != nil {
		return TestResult{}, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()

	tx, err := conn.BeginTx(probeCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return TestResult{}, &PublicError{Code: "READ_ONLY_SETUP_FAILED", Message: "could not start a read-only database transaction"}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var result TestResult
	var serverPort int32
	result.Connected = true
	if err := tx.QueryRow(probeCtx, `
		SELECT pg_is_in_recovery(), inet_server_port(),
		       pg_has_role(current_user, 'pg_monitor', 'member'),
		       pg_has_role(current_user, 'pg_read_all_stats', 'member'),
		       EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extname = 'citus'),
		       EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extname = 'pg_stat_statements'),
		       has_table_privilege(current_user, 'pg_catalog.pg_stat_activity', 'SELECT'),
		       has_function_privilege(current_user, 'pg_catalog.pg_blocking_pids(integer)', 'EXECUTE'),
		       has_table_privilege(current_user, 'pg_catalog.pg_stat_wal', 'SELECT'),
		       has_table_privilege(current_user, 'pg_catalog.pg_stat_bgwriter', 'SELECT'),
		       has_table_privilege(current_user, 'pg_catalog.pg_stat_replication', 'SELECT'),
		       has_table_privilege(current_user, 'pg_catalog.pg_stat_user_tables', 'SELECT')
	`).Scan(
		&result.InRecovery,
		&serverPort,
		&result.Capabilities.PGMonitor,
		&result.Capabilities.PGReadAllStats,
		&result.Capabilities.CitusExtension,
		&result.Capabilities.PGStatStatements,
		&result.Capabilities.Sessions,
		&result.Capabilities.Blocking,
		&result.Capabilities.WAL,
		&result.Capabilities.Checkpoint,
		&result.Capabilities.Replication,
		&result.Capabilities.TableStats,
	); err != nil {
		return TestResult{}, SanitizeProbeError(err)
	}
	if serverPort < 1 || serverPort > 65535 {
		return TestResult{}, &PublicError{Code: "CAPABILITY_PROBE_FAILED", Message: "database capability probe failed"}
	}
	result.ServerPort = uint16(serverPort)
	result.LatencyMS = time.Since(started).Milliseconds()
	return result, nil
}

// Open establishes one bounded connection without retaining or exposing its
// endpoint credentials. Callers own Close and apply the returned timeout to
// their query context.
func Open(ctx context.Context, input TestConfig) (*pgx.Conn, time.Duration, error) {
	config, connectTimeout, queryTimeout, err := connectionConfig(input)
	if err != nil {
		return nil, 0, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(connectCtx, config)
	if err != nil {
		return nil, 0, classifyConnectionError(err)
	}
	return conn, queryTimeout, nil
}

func connectionConfig(input TestConfig) (*pgx.ConnConfig, time.Duration, time.Duration, error) {
	sslMode, err := ValidateConfig(input)
	if err != nil {
		return nil, 0, 0, err
	}
	timeout := defaultConnectTimeout
	if input.ConnectTimeout > 0 {
		timeout = time.Duration(input.ConnectTimeout) * time.Second
	}
	if timeout > maxConnectTimeout {
		return nil, 0, 0, &PublicError{Code: "INVALID_DATASOURCE", Message: "connect timeout exceeds 30 seconds"}
	}
	queryTimeout := defaultQueryTimeout
	if input.QueryTimeout > 0 {
		queryTimeout = time.Duration(input.QueryTimeout) * time.Second
	}
	if queryTimeout > maxQueryTimeout {
		return nil, 0, 0, &PublicError{Code: "INVALID_DATASOURCE", Message: "query timeout exceeds 30 seconds"}
	}

	endpoint := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(input.User, input.Password),
		Host:   net.JoinHostPort(input.Host, strconv.Itoa(int(input.Port))),
		Path:   input.Database,
	}
	query := endpoint.Query()
	query.Set("sslmode", sslMode)
	query.Set("connect_timeout", strconv.Itoa(int(timeout/time.Second)))
	query.Set("application_name", applicationName)
	endpoint.RawQuery = query.Encode()
	config, err := pgx.ParseConfig(endpoint.String())
	if err != nil {
		return nil, 0, 0, &PublicError{Code: "INVALID_DATASOURCE", Message: "database connection settings are invalid"}
	}
	return config, timeout, queryTimeout, nil
}

// ValidateConfig validates connection fields without contacting the database.
// The returned SSL mode is normalized for persistence.
func ValidateConfig(input TestConfig) (string, error) {
	if input.Host == "" || input.Database == "" || input.User == "" || input.Port == 0 {
		return "", &PublicError{Code: "INVALID_DATASOURCE", Message: "host, port, database and user are required"}
	}
	if len(input.Host) > 255 || len(input.Database) > 128 || len(input.User) > 128 {
		return "", &PublicError{Code: "INVALID_DATASOURCE", Message: "database connection settings are too long"}
	}
	sslMode := input.SSLMode
	if sslMode == "" {
		sslMode = "prefer"
	}
	if _, ok := allowedSSLModes[sslMode]; !ok {
		return "", &PublicError{Code: "INVALID_DATASOURCE", Message: "unsupported SSL mode"}
	}
	return sslMode, nil
}

func classifyConnectionError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &PublicError{Code: "ABORTED", Message: "database connection was canceled"}
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "28P01":
			return &PublicError{Code: "AUTHENTICATION_FAILED", Message: "database authentication failed"}
		case "28000":
			return &PublicError{Code: "ACCESS_REJECTED", Message: "database access policy rejected this connection"}
		case "3D000":
			return &PublicError{Code: "DATABASE_NOT_FOUND", Message: "database does not exist"}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &PublicError{Code: "CONNECTION_TIMEOUT", Message: "database connection timed out"}
	}
	return &PublicError{Code: "CONNECTION_FAILED", Message: "database connection failed"}
}

func SanitizeProbeError(err error) error {
	if errors.Is(err, context.Canceled) {
		return &PublicError{Code: "ABORTED", Message: "database query was canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &PublicError{Code: "QUERY_TIMEOUT", Message: "database query timed out"}
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "42501" {
		return &PublicError{Code: "INSUFFICIENT_PRIVILEGE", Message: "account cannot read required monitoring metadata"}
	}
	return &PublicError{Code: "CAPABILITY_PROBE_FAILED", Message: "database capability probe failed"}
}

func AsPublicError(err error) *PublicError {
	var publicError *PublicError
	if errors.As(err, &publicError) {
		return publicError
	}
	return &PublicError{Code: "INTERNAL_ERROR", Message: "database test failed"}
}
