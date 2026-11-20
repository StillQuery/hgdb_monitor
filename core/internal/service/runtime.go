package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"hgdb_monitor/core/internal/collector"
	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/model"
	"hgdb_monitor/core/internal/scheduler"
	"hgdb_monitor/core/internal/secret"
	"hgdb_monitor/core/internal/storage"
)

const (
	MaxDatasources  = 5
	SampleInterval  = 15 * time.Second
	HistoryWindow   = 2 * time.Hour
	CleanupInterval = 12 * time.Hour
	StatusInterval  = 60 * time.Second
)

type PublicError struct {
	Code    string
	Message string
}

func (e *PublicError) Error() string { return e.Code }

type CreateInput struct {
	DisplayName    string `json:"displayName"`
	Host           string `json:"host"`
	Port           uint16 `json:"port"`
	Database       string `json:"database"`
	User           string `json:"user"`
	Password       string `json:"password"`
	SSLMode        string `json:"sslMode"`
	ConnectTimeout int    `json:"connectTimeoutSeconds,omitempty"`
	QueryTimeout   int    `json:"queryTimeoutSeconds,omitempty"`
}

type UpdateInput struct {
	ID             string `json:"id"`
	DisplayName    string `json:"displayName"`
	Host           string `json:"host"`
	Port           uint16 `json:"port"`
	Database       string `json:"database"`
	User           string `json:"user"`
	Password       string `json:"password,omitempty"`
	SSLMode        string `json:"sslMode"`
	ConnectTimeout int    `json:"connectTimeoutSeconds,omitempty"`
	QueryTimeout   int    `json:"queryTimeoutSeconds,omitempty"`
}

type Repository interface {
	CreateDatasource(context.Context, model.Datasource, int) error
	ListDatasources(context.Context) ([]model.Datasource, error)
	GetDatasource(context.Context, string) (model.Datasource, error)
	UpdateDatasource(context.Context, model.Datasource) error
	DeleteDatasource(context.Context, string) error
	SaveSnapshot(context.Context, string, model.Snapshot) error
	LatestSnapshot(context.Context, string) (model.Snapshot, error)
	QuerySnapshots(context.Context, string, time.Time, time.Time) ([]model.Snapshot, error)
	DeleteSnapshotsBefore(context.Context, time.Time) (int64, error)
	SaveInstanceStatus(context.Context, string, model.InstanceStatus) error
	LatestInstanceStatus(context.Context, string) (model.InstanceStatus, error)
}

type TestFunc func(context.Context, datasource.TestConfig) (datasource.TestResult, error)
type CollectFunc func(context.Context, datasource.TestConfig) (model.Snapshot, error)
type StatusCollectFunc func(context.Context, datasource.TestConfig) (model.InstanceStatus, error)

type Runtime struct {
	ctx                context.Context
	repo               Repository
	secrets            secret.Store
	test               TestFunc
	collect            CollectFunc
	collectStatus      StatusCollectFunc
	scheduler          *scheduler.Manager
	cleanup            time.Duration
	retention          time.Duration
	closeOnce          sync.Once
	cleanupWG          sync.WaitGroup
	cancel             context.CancelFunc
	stateMu            sync.Mutex
	needsProbe         map[string]bool
	slots              map[string]chan struct{}
	lastStatus         map[string]time.Time
	storageReady       bool
	storageError       string
	lastCleanupAt      time.Time
	lastCleanupDeleted int64
}

func NewRuntime(parent context.Context, repo Repository, secrets secret.Store) *Runtime {
	runtime := NewRuntimeWithOptions(parent, repo, secrets, SampleInterval, CleanupInterval, HistoryWindow,
		datasource.TestConnection, func(ctx context.Context, cfg datasource.TestConfig) (model.Snapshot, error) {
			return collector.CollectMetrics(ctx, cfg)
		})
	runtime.collectStatus = collector.CollectStatus
	return runtime
}

func NewRuntimeWithOptions(parent context.Context, repo Repository, secrets secret.Store,
	sample, cleanup, retention time.Duration, test TestFunc, collect CollectFunc) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{ctx: ctx, repo: repo, secrets: secrets, test: test, collect: collect,
		cleanup: cleanup, retention: retention, cancel: cancel, needsProbe: make(map[string]bool), slots: make(map[string]chan struct{}), lastStatus: make(map[string]time.Time), storageReady: true}
	r.scheduler = scheduler.New(ctx, sample, r.runSample)
	return r
}

func (r *Runtime) Start(ctx context.Context) error {
	sources, err := r.repo.ListDatasources(ctx)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.PersistentEnabled {
			r.stateMu.Lock()
			r.needsProbe[source.ID] = true
			r.stateMu.Unlock()
			_ = r.scheduler.Start(source.ID)
		}
	}
	r.cleanupWG.Add(1)
	go r.cleanupLoop()
	return nil
}

func (r *Runtime) cleanupLoop() {
	defer r.cleanupWG.Done()
	r.cleanupExpired(time.Now().UTC())
	ticker := time.NewTicker(r.cleanup)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case now := <-ticker.C:
			r.cleanupExpired(now.UTC())
		}
	}
}

func (r *Runtime) cleanupExpired(now time.Time) {
	deleted, err := r.repo.DeleteSnapshotsBefore(r.ctx, now.Add(-r.retention))
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if err != nil {
		r.storageReady = false
		r.storageError = "STORAGE_CLEANUP_FAILED"
		return
	}
	r.storageReady = true
	r.storageError = ""
	r.lastCleanupAt = now
	r.lastCleanupDeleted = deleted
}

func (r *Runtime) Close() {
	r.closeOnce.Do(func() {
		r.cancel()
		r.scheduler.StopAll()
		r.cleanupWG.Wait()
	})
}

func (r *Runtime) Create(ctx context.Context, input CreateInput) (model.Datasource, error) {
	if err := validateName(input.DisplayName); err != nil {
		return model.Datasource{}, err
	}
	cfg := configFromCreate(input)
	sslMode, err := datasource.ValidateConfig(cfg)
	if err != nil {
		return model.Datasource{}, datasource.AsPublicError(err)
	}
	if input.Password == "" {
		return model.Datasource{}, &PublicError{Code: "INVALID_DATASOURCE", Message: "password is required"}
	}
	now := time.Now().UTC()
	id, err := randomToken("src_")
	if err != nil {
		return model.Datasource{}, &PublicError{Code: "INTERNAL_ERROR", Message: "could not create database instance identity"}
	}
	instanceRef, err := randomToken("db_")
	if err != nil {
		return model.Datasource{}, &PublicError{Code: "INTERNAL_ERROR", Message: "could not create anonymous instance reference"}
	}
	source := model.Datasource{
		ID: id, InstanceRef: instanceRef, DisplayName: strings.TrimSpace(input.DisplayName),
		Host: input.Host, Port: input.Port, Database: input.Database, User: input.User, SSLMode: sslMode,
		ConnectTimeout: normalizedTimeout(input.ConnectTimeout, 5), QueryTimeout: normalizedTimeout(input.QueryTimeout, 10),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := r.secrets.Set(ctx, source.ID, input.Password); err != nil {
		return model.Datasource{}, &PublicError{Code: "CREDENTIAL_STORE_UNAVAILABLE", Message: "could not store database credential securely"}
	}
	if err := r.repo.CreateDatasource(ctx, source, MaxDatasources); err != nil {
		_ = r.secrets.Delete(ctx, source.ID)
		if errors.Is(err, storage.ErrDatasourceLimit) {
			return model.Datasource{}, &PublicError{Code: "DATASOURCE_LIMIT_REACHED", Message: "at most five database instances are supported"}
		}
		return model.Datasource{}, err
	}
	return source, nil
}

func (r *Runtime) List(ctx context.Context) ([]model.Datasource, error) {
	return r.repo.ListDatasources(ctx)
}

func (r *Runtime) Update(ctx context.Context, input UpdateInput) (model.Datasource, error) {
	if err := validateName(input.DisplayName); err != nil {
		return model.Datasource{}, err
	}
	current, err := r.repo.GetDatasource(ctx, input.ID)
	if err != nil {
		return model.Datasource{}, mapStoreError(err)
	}
	cfg := datasource.TestConfig{Host: input.Host, Port: input.Port, Database: input.Database, User: input.User, SSLMode: input.SSLMode, ConnectTimeout: input.ConnectTimeout, QueryTimeout: input.QueryTimeout}
	sslMode, err := datasource.ValidateConfig(cfg)
	if err != nil {
		return model.Datasource{}, datasource.AsPublicError(err)
	}
	connectionChanged := current.Host != input.Host || current.Port != input.Port || current.Database != input.Database ||
		current.User != input.User || current.SSLMode != sslMode || current.ConnectTimeout != normalizedTimeout(input.ConnectTimeout, 5) || current.QueryTimeout != normalizedTimeout(input.QueryTimeout, 10) || input.Password != ""
	var previousPassword string
	if input.Password != "" {
		previousPassword, err = r.secrets.Get(ctx, current.ID)
		if err != nil {
			return model.Datasource{}, &PublicError{Code: "CREDENTIAL_UNAVAILABLE", Message: "existing database credential is unavailable"}
		}
		if err := r.secrets.Set(ctx, current.ID, input.Password); err != nil {
			return model.Datasource{}, &PublicError{Code: "CREDENTIAL_STORE_UNAVAILABLE", Message: "could not store database credential securely"}
		}
	}
	current.DisplayName = strings.TrimSpace(input.DisplayName)
	current.Host, current.Port, current.Database, current.User, current.SSLMode = input.Host, input.Port, input.Database, input.User, sslMode
	current.ConnectTimeout, current.QueryTimeout = normalizedTimeout(input.ConnectTimeout, 5), normalizedTimeout(input.QueryTimeout, 10)
	current.UpdatedAt = time.Now().UTC()
	if connectionChanged {
		current.VerifiedAt = nil
		current.PersistentEnabled = false
	}
	if err := r.repo.UpdateDatasource(ctx, current); err != nil {
		if input.Password != "" {
			_ = r.secrets.Set(ctx, current.ID, previousPassword)
		}
		return model.Datasource{}, mapStoreError(err)
	}
	if connectionChanged {
		r.scheduler.Stop(current.ID)
	}
	return current, nil
}

func (r *Runtime) Test(ctx context.Context, id string) (datasource.TestResult, error) {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return datasource.TestResult{}, err
	}
	defer release()
	source, cfg, err := r.connectionConfig(ctx, id)
	if err != nil {
		return datasource.TestResult{}, err
	}
	result, err := r.test(ctx, cfg)
	if err != nil {
		return datasource.TestResult{}, datasource.AsPublicError(err)
	}
	now := time.Now().UTC()
	source.VerifiedAt = &now
	source.Capabilities = result.Capabilities
	source.UpdatedAt = now
	if err := r.repo.UpdateDatasource(ctx, source); err != nil {
		return datasource.TestResult{}, mapStoreError(err)
	}
	return result, nil
}

func (r *Runtime) SetPersistent(ctx context.Context, id string, enabled bool) (model.Datasource, error) {
	if enabled {
		r.stateMu.Lock()
		ready := r.storageReady
		r.stateMu.Unlock()
		if !ready {
			return model.Datasource{}, &PublicError{Code: "STORAGE_UNAVAILABLE", Message: "local monitoring storage is unavailable"}
		}
	}
	source, err := r.repo.GetDatasource(ctx, id)
	if err != nil {
		return model.Datasource{}, mapStoreError(err)
	}
	if enabled && source.VerifiedAt == nil {
		return model.Datasource{}, &PublicError{Code: "CONNECTION_NOT_VERIFIED", Message: "test the connection successfully before enabling collection"}
	}
	source.PersistentEnabled = enabled
	source.UpdatedAt = time.Now().UTC()
	if err := r.repo.UpdateDatasource(ctx, source); err != nil {
		return model.Datasource{}, mapStoreError(err)
	}
	if enabled {
		if err := r.scheduler.Start(id); err != nil && !errors.Is(err, scheduler.ErrAlreadyRunning) {
			return model.Datasource{}, err
		}
	} else {
		r.scheduler.Stop(id)
	}
	return source, nil
}

func (r *Runtime) Delete(ctx context.Context, id string) error {
	r.scheduler.Stop(id)
	releaseFirst, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer releaseFirst()
	releaseSecond, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer releaseSecond()
	if err := r.repo.DeleteDatasource(ctx, id); err != nil {
		return mapStoreError(err)
	}
	if err := r.secrets.Delete(ctx, id); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return &PublicError{Code: "CREDENTIAL_DELETE_FAILED", Message: "database credential could not be removed from the system credential store"}
	}
	r.stateMu.Lock()
	delete(r.needsProbe, id)
	delete(r.slots, id)
	delete(r.lastStatus, id)
	r.stateMu.Unlock()
	return nil
}

func (r *Runtime) Status(id string) scheduler.Status { return r.scheduler.Status(id) }

func (r *Runtime) runSample(ctx context.Context, id string) error {
	release, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	source, cfg, err := r.connectionConfig(ctx, id)
	if err != nil {
		return err
	}
	r.stateMu.Lock()
	probe := r.needsProbe[id]
	r.stateMu.Unlock()
	if probe {
		result, testErr := r.test(ctx, cfg)
		if testErr != nil {
			return testErr
		}
		now := time.Now().UTC()
		source.Capabilities = result.Capabilities
		source.VerifiedAt = &now
		source.UpdatedAt = now
		if err := r.repo.UpdateDatasource(ctx, source); err != nil {
			r.markStorage(false, "STORAGE_WRITE_FAILED")
			return err
		}
		r.stateMu.Lock()
		delete(r.needsProbe, id)
		r.stateMu.Unlock()
	}
	snapshot, err := r.collect(ctx, cfg)
	if err != nil {
		return err
	}
	if r.collectStatus != nil {
		r.stateMu.Lock()
		statusDue := time.Since(r.lastStatus[id]) >= StatusInterval
		r.stateMu.Unlock()
		if statusDue {
			status, statusErr := r.collectStatus(ctx, cfg)
			if statusErr != nil {
				return statusErr
			}
			if statusErr := r.repo.SaveInstanceStatus(ctx, id, status); statusErr != nil {
				r.markStorage(false, "STORAGE_WRITE_FAILED")
				return statusErr
			}
			r.stateMu.Lock()
			r.lastStatus[id] = status.ObservedAt
			r.stateMu.Unlock()
		}
		if status, statusErr := r.repo.LatestInstanceStatus(ctx, id); statusErr == nil {
			model.ApplyStatus(&snapshot, status)
		} else if !errors.Is(statusErr, storage.ErrStatusNotFound) {
			r.markStorage(false, "STORAGE_READ_FAILED")
			return statusErr
		}
	}
	err = r.repo.SaveSnapshot(ctx, id, snapshot)
	if err != nil {
		r.markStorage(false, "STORAGE_WRITE_FAILED")
		return err
	}
	r.markStorage(true, "")
	return nil
}

type StorageHealth struct {
	Ready              bool      `json:"ready"`
	ErrorCode          string    `json:"errorCode,omitempty"`
	LastCleanupAt      time.Time `json:"lastCleanupAt,omitempty"`
	LastCleanupDeleted int64     `json:"lastCleanupDeleted"`
}

func (r *Runtime) StorageHealth() StorageHealth {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return StorageHealth{Ready: r.storageReady, ErrorCode: r.storageError, LastCleanupAt: r.lastCleanupAt, LastCleanupDeleted: r.lastCleanupDeleted}
}

func (r *Runtime) markStorage(ready bool, code string) {
	r.stateMu.Lock()
	r.storageReady, r.storageError = ready, code
	r.stateMu.Unlock()
}

func (r *Runtime) acquire(ctx context.Context, id string) (func(), error) {
	r.stateMu.Lock()
	semaphore := r.slots[id]
	if semaphore == nil {
		semaphore = make(chan struct{}, 2)
		r.slots[id] = semaphore
	}
	r.stateMu.Unlock()
	select {
	case semaphore <- struct{}{}:
		return func() { <-semaphore }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Runtime) connectionConfig(ctx context.Context, id string) (model.Datasource, datasource.TestConfig, error) {
	source, err := r.repo.GetDatasource(ctx, id)
	if err != nil {
		return model.Datasource{}, datasource.TestConfig{}, mapStoreError(err)
	}
	password, err := r.secrets.Get(ctx, id)
	if err != nil {
		return model.Datasource{}, datasource.TestConfig{}, &PublicError{Code: "CREDENTIAL_UNAVAILABLE", Message: "database credential is unavailable"}
	}
	return source, datasource.TestConfig{Host: source.Host, Port: source.Port, Database: source.Database,
		User: source.User, Password: password, SSLMode: source.SSLMode, ConnectTimeout: source.ConnectTimeout, QueryTimeout: source.QueryTimeout}, nil
}

func configFromCreate(input CreateInput) datasource.TestConfig {
	return datasource.TestConfig{Host: input.Host, Port: input.Port, Database: input.Database,
		User: input.User, Password: input.Password, SSLMode: input.SSLMode, ConnectTimeout: input.ConnectTimeout, QueryTimeout: input.QueryTimeout}
}

func normalizedTimeout(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return &PublicError{Code: "INVALID_DATASOURCE", Message: "display name must contain 1 to 64 characters"}
	}
	return nil
}

func randomToken(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value)), nil
}

func mapStoreError(err error) error {
	if errors.Is(err, storage.ErrDatasourceNotFound) {
		return &PublicError{Code: "DATASOURCE_NOT_FOUND", Message: "database instance does not exist"}
	}
	return err
}

func AsPublicError(err error) *PublicError {
	if errors.Is(err, context.Canceled) {
		return &PublicError{Code: "ABORTED", Message: "operation was canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &PublicError{Code: "QUERY_TIMEOUT", Message: "operation timed out"}
	}
	var serviceError *PublicError
	if errors.As(err, &serviceError) {
		return serviceError
	}
	var datasourceError *datasource.PublicError
	if errors.As(err, &datasourceError) {
		return &PublicError{Code: datasourceError.Code, Message: datasourceError.Message}
	}
	return &PublicError{Code: "INTERNAL_ERROR", Message: "operation failed"}
}
