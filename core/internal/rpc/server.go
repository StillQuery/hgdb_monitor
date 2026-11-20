package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"hgdb_monitor/core/internal/collector"
	"hgdb_monitor/core/internal/datasource"
	"hgdb_monitor/core/internal/inspector"
	"hgdb_monitor/core/internal/service"
	"hgdb_monitor/core/internal/storage"
)

const ProtocolVersion = 1

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

type Server struct {
	registry  *collector.Registry
	storage   *storage.SQLite
	runtime   *service.Runtime
	stop      context.CancelFunc
	writeMu   sync.Mutex
	requestMu sync.Mutex
	requests  map[string]context.CancelFunc
}

func NewServer(registry *collector.Registry, store *storage.SQLite, runtime *service.Runtime, stop context.CancelFunc) *Server {
	return &Server{registry: registry, storage: store, runtime: runtime, stop: stop, requests: make(map[string]context.CancelFunc)}
}

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var active sync.WaitGroup
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var request Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			s.write(output, Response{ID: "", Error: &Error{Code: "INVALID_REQUEST", Message: "request is not valid JSON"}})
			continue
		}
		if request.ID == "" || request.Method == "" {
			s.write(output, s.dispatch(ctx, request))
			continue
		}
		switch request.Method {
		case "system.handshake", "system.health", "system.cancel":
			s.write(output, s.dispatch(ctx, request))
		case "system.shutdown":
			s.cancelAllRequests()
			active.Wait()
			s.write(output, s.dispatch(ctx, request))
			return nil
		default:
			requestCtx, cancel := context.WithCancel(ctx)
			if !s.registerRequest(request.ID, cancel) {
				cancel()
				s.write(output, Response{ID: request.ID, Error: &Error{Code: "DUPLICATE_REQUEST", Message: "request id is already running"}})
				continue
			}
			active.Add(1)
			go func(request Request, requestCtx context.Context) {
				defer active.Done()
				defer s.finishRequest(request.ID)
				s.write(output, s.dispatch(requestCtx, request))
			}(request, requestCtx)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read protocol input: %w", err)
	}
	active.Wait()
	return nil
}

func (s *Server) dispatch(ctx context.Context, request Request) Response {
	if request.ID == "" || request.Method == "" {
		return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "id and method are required"}}
	}
	switch request.Method {
	case "system.handshake":
		var params struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ProtocolVersion != ProtocolVersion {
			return Response{ID: request.ID, Error: &Error{Code: "PROTOCOL_MISMATCH", Message: "unsupported protocol version"}}
		}
		return Response{ID: request.ID, Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"service":         "hgdb_monitor_core",
			"capabilities": []string{
				"collector.registry", "collector.snapshot", "datasource.management",
				"datasource.test", "scheduler.persistent", "monitor.history", "monitor.ai-safe",
				"monitor.activity", "monitor.explain", "request.cancel", "storage.sqlite", "lifecycle.shutdown",
			},
		}}
	case "system.health":
		return Response{ID: request.ID, Result: map[string]any{"ready": s.storage.Ready(ctx)}}
	case "system.shutdown":
		defer s.stop()
		return Response{ID: request.ID, Result: map[string]any{"accepted": true}}
	case "system.cancel":
		var params struct {
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.RequestID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "request id is required"}}
		}
		return Response{ID: request.ID, Result: map[string]bool{"cancelled": s.cancelRequest(params.RequestID)}}
	case "collector.list":
		return Response{ID: request.ID, Result: s.registry.List()}
	case "collector.snapshot":
		var params struct {
			InstanceID string                `json:"instanceId,omitempty"`
			Datasource datasource.TestConfig `json:"datasource"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid collector snapshot request"}}
		}
		result, err := collector.CollectSnapshot(ctx, params.Datasource)
		if err != nil {
			publicError := datasource.AsPublicError(err)
			return Response{ID: request.ID, Error: &Error{Code: publicError.Code, Message: publicError.Message}}
		}
		if params.InstanceID != "" {
			if err := s.storage.SaveSnapshot(ctx, params.InstanceID, result); err != nil {
				return Response{ID: request.ID, Error: &Error{Code: "STORAGE_WRITE_FAILED", Message: "could not persist metric snapshot"}}
			}
		}
		return Response{ID: request.ID, Result: result}
	case "datasource.create":
		var params service.CreateInput
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid datasource creation request"}}
		}
		result, err := s.runtime.Create(ctx, params)
		return serviceResponse(request.ID, result, err)
	case "datasource.list":
		result, err := s.runtime.List(ctx)
		return serviceResponse(request.ID, result, err)
	case "datasource.update":
		var params service.UpdateInput
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid datasource update request"}}
		}
		result, err := s.runtime.Update(ctx, params)
		return serviceResponse(request.ID, result, err)
	case "datasource.test":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "datasource id is required"}}
		}
		result, err := s.runtime.Test(ctx, params.ID)
		return serviceResponse(request.ID, result, err)
	case "datasource.setPersistent":
		var params struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid persistent collection request"}}
		}
		result, err := s.runtime.SetPersistent(ctx, params.ID, params.Enabled)
		return serviceResponse(request.ID, result, err)
	case "datasource.delete":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "datasource id is required"}}
		}
		err := s.runtime.Delete(ctx, params.ID)
		return serviceResponse(request.ID, map[string]bool{"deleted": err == nil}, err)
	case "scheduler.status":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "datasource id is required"}}
		}
		return Response{ID: request.ID, Result: s.runtime.Status(params.ID)}
	case "monitor.history":
		var params struct {
			ID   string    `json:"id"`
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid history request"}}
		}
		result, err := s.runtime.QueryHistory(ctx, params.ID, params.From, params.To)
		return serviceResponse(request.ID, result, err)
	case "monitor.latest":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "datasource id is required"}}
		}
		result, err := s.runtime.Latest(ctx, params.ID)
		return serviceResponse(request.ID, result, err)
	case "monitor.current":
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "datasource id is required"}}
		}
		result, err := s.runtime.Current(ctx, params.ID)
		return serviceResponse(request.ID, result, err)
	case "monitor.activities":
		var params struct {
			ID    string                 `json:"id"`
			Kind  inspector.ActivityKind `json:"kind"`
			Limit int                    `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid activity request"}}
		}
		result, err := s.runtime.Activities(ctx, params.ID, params.Kind, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "monitor.lockWaits":
		var params struct {
			ID    string `json:"id"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid lock-wait request"}}
		}
		result, err := s.runtime.LockWaits(ctx, params.ID, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "monitor.topSQL":
		var params struct {
			ID    string                `json:"id"`
			Order inspector.TopSQLOrder `json:"order"`
			Limit int                   `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid Top SQL request"}}
		}
		result, err := s.runtime.TopSQL(ctx, params.ID, params.Order, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "monitor.replication":
		var params struct {
			ID           string `json:"id"`
			IncludeSlots bool   `json:"includeSlots"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.ID == "" {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid replication request"}}
		}
		result, err := s.runtime.Replication(ctx, params.ID, params.IncludeSlots)
		return serviceResponse(request.ID, result, err)
	case "ai.instances":
		result, err := s.runtime.ListAIInstances(ctx)
		return serviceResponse(request.ID, map[string]any{"instances": result}, err)
	case "ai.latest":
		var params struct {
			InstanceRef string `json:"instanceRef"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid latest-state request"}}
		}
		result, err := s.runtime.AILatest(ctx, params.InstanceRef)
		return serviceResponse(request.ID, result, err)
	case "ai.current":
		var params struct {
			InstanceRef   string `json:"instanceRef"`
			IncludeDeltas *bool  `json:"includeDeltas"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid current-state request"}}
		}
		includeDeltas := true
		if params.IncludeDeltas != nil {
			includeDeltas = *params.IncludeDeltas
		}
		result, err := s.runtime.AIOverview(ctx, params.InstanceRef, includeDeltas)
		return serviceResponse(request.ID, result, err)
	case "ai.diagnose":
		var params struct {
			InstanceRef     string `json:"instanceRef"`
			Focus           string `json:"focus"`
			WindowMinutes   int    `json:"windowMinutes"`
			ComparePrevious *bool  `json:"comparePrevious"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid diagnosis request"}}
		}
		compare := true
		if params.ComparePrevious != nil {
			compare = *params.ComparePrevious
		}
		result, err := s.runtime.AIDiagnoseDatabase(ctx, params.InstanceRef, params.Focus, params.WindowMinutes, compare)
		return serviceResponse(request.ID, result, err)
	case "ai.queryMetrics":
		var params struct {
			InstanceRef     string     `json:"instanceRef"`
			MetricKeys      []string   `json:"metricKeys"`
			WindowMinutes   int        `json:"windowMinutes"`
			EndTime         *time.Time `json:"endTime,omitempty"`
			ComparePrevious bool       `json:"comparePrevious"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid metric query"}}
		}
		var end time.Time
		if params.EndTime != nil {
			end = *params.EndTime
		}
		result, err := s.runtime.AIQueryMetrics(ctx, params.InstanceRef, params.MetricKeys, params.WindowMinutes, end, params.ComparePrevious)
		return serviceResponse(request.ID, result, err)
	case "ai.history":
		var params struct {
			InstanceRef string   `json:"instanceRef"`
			Minutes     int      `json:"minutes"`
			Metrics     []string `json:"metrics,omitempty"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid history-summary request"}}
		}
		result, err := s.runtime.AIHistory(ctx, params.InstanceRef, params.Minutes, params.Metrics)
		return serviceResponse(request.ID, result, err)
	case "ai.activities":
		var params struct {
			InstanceRef        string                 `json:"instanceRef"`
			Kind               inspector.ActivityKind `json:"kind"`
			MinDurationSeconds int                    `json:"minDurationSeconds"`
			Limit              int                    `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid activity request"}}
		}
		result, err := s.runtime.AIActivitiesSafeWithMinimum(ctx, params.InstanceRef, params.Kind, params.MinDurationSeconds, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "ai.lockWaits":
		var params struct {
			InstanceRef string `json:"instanceRef"`
			Limit       int    `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid lock-wait request"}}
		}
		result, err := s.runtime.AILockWaitsSafe(ctx, params.InstanceRef, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "ai.topSQL":
		var params struct {
			InstanceRef string                `json:"instanceRef"`
			Order       inspector.TopSQLOrder `json:"order"`
			Limit       int                   `json:"limit"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid Top SQL request"}}
		}
		result, err := s.runtime.AITopSQL(ctx, params.InstanceRef, params.Order, params.Limit)
		return serviceResponse(request.ID, result, err)
	case "ai.replication":
		var params struct {
			InstanceRef  string `json:"instanceRef"`
			IncludeSlots bool   `json:"includeSlots"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid replication request"}}
		}
		result, err := s.runtime.AIReplication(ctx, params.InstanceRef, params.IncludeSlots)
		return serviceResponse(request.ID, result, err)
	case "ai.explain":
		var params struct {
			InstanceRef string `json:"instanceRef"`
			SQL         string `json:"sql"`
			Format      string `json:"format"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "INVALID_REQUEST", Message: "invalid explain request"}}
		}
		result, err := s.runtime.AIExplainSafe(ctx, params.InstanceRef, params.SQL, params.Format)
		return serviceResponse(request.ID, result, err)
	case "storage.status":
		health := s.runtime.StorageHealth()
		snapshotCount, err := s.storage.SnapshotCount(ctx)
		if err != nil {
			return Response{ID: request.ID, Error: &Error{Code: "STORAGE_READ_FAILED", Message: "could not read storage status"}}
		}
		return Response{ID: request.ID, Result: map[string]any{
			"engine":        "sqlite",
			"schemaVersion": storage.SchemaVersion,
			"databaseReady": s.storage.Ready(ctx),
			"runtime":       health,
			"snapshotCount": snapshotCount,
		}}
	default:
		return Response{ID: request.ID, Error: &Error{Code: "METHOD_NOT_FOUND", Message: "unknown method"}}
	}
}

func (s *Server) cancelRequest(id string) bool {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	cancel := s.requests[id]
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Server) registerRequest(id string, cancel context.CancelFunc) bool {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if id == "" || s.requests[id] != nil {
		return false
	}
	s.requests[id] = cancel
	return true
}

func (s *Server) finishRequest(id string) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if cancel := s.requests[id]; cancel != nil {
		cancel()
		delete(s.requests, id)
	}
}

func (s *Server) cancelAllRequests() {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	for _, cancel := range s.requests {
		cancel()
	}
}

func serviceResponse(id string, result any, err error) Response {
	if err == nil {
		return Response{ID: id, Result: result}
	}
	publicError := service.AsPublicError(err)
	return Response{ID: id, Error: &Error{Code: publicError.Code, Message: publicError.Message}}
}

func (s *Server) write(output io.Writer, response Response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	_, _ = output.Write(append(encoded, '\n'))
}
