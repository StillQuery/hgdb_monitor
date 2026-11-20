package collector

import (
	"fmt"
	"sort"
	"sync"
)

// Definition describes one independently queryable metric group.
type Definition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Metrics     []string `json:"metrics"`
}

// Registry owns collector metadata without coupling scheduling or storage to SQL.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Definition
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Definition)}
}

func (r *Registry) Register(def Definition) error {
	if def.Name == "" {
		return fmt.Errorf("collector name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[def.Name]; exists {
		return fmt.Errorf("collector %q already registered", def.Name)
	}
	def.Metrics = append([]string(nil), def.Metrics...)
	r.items[def.Name] = def
	return nil
}

func (r *Registry) List() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Definition, 0, len(r.items))
	for _, item := range r.items {
		item.Metrics = append([]string(nil), item.Metrics...)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func RegisterBuiltins(registry *Registry) error {
	definitions := []Definition{
		{Name: "activity", Description: "connections and transaction activity", Metrics: []string{"sessions_total", "sessions_active", "sessions_idle_transaction", "sessions_waiting", "long_queries", "long_transactions"}},
		{Name: "database", Description: "database workload counters", Metrics: []string{"commits", "rollbacks", "blocks_hit", "blocks_read", "rows_inserted", "rows_updated", "rows_deleted", "temp_files", "temp_bytes", "deadlocks"}},
		{Name: "locks", Description: "lock waits and blocking state", Metrics: []string{"locks_not_granted", "blocked_sessions"}},
		{Name: "wal", Description: "WAL and checkpoint counters", Metrics: []string{"wal_bytes", "checkpoints_timed", "checkpoints_requested"}},
		{Name: "replication", Description: "recovery and downstream replication state", Metrics: []string{"replication_connections", "max_replay_lag_bytes", "max_replay_lag_seconds"}},
	}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			return err
		}
	}
	return nil
}
