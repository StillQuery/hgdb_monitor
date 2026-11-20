package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrAlreadyRunning = errors.New("collection job already running")

type RunFunc func(context.Context, string) error

type Status struct {
	Running             bool      `json:"running"`
	LastRunAt           time.Time `json:"lastRunAt,omitempty"`
	LastSuccess         time.Time `json:"lastSuccessAt,omitempty"`
	LastError           string    `json:"lastErrorCode,omitempty"`
	ConsecutiveFailures int       `json:"consecutiveFailures"`
	NextRunAt           time.Time `json:"nextRunAt,omitempty"`
}

type job struct {
	cancel context.CancelFunc
	done   chan struct{}
	status Status
}

type Manager struct {
	root     context.Context
	interval time.Duration
	run      RunFunc
	mu       sync.RWMutex
	jobs     map[string]*job
}

func New(root context.Context, interval time.Duration, run RunFunc) *Manager {
	return &Manager{root: root, interval: interval, run: run, jobs: make(map[string]*job)}
}

func (m *Manager) Start(id string) error {
	m.mu.Lock()
	if _, exists := m.jobs[id]; exists {
		m.mu.Unlock()
		return ErrAlreadyRunning
	}
	ctx, cancel := context.WithCancel(m.root)
	j := &job{cancel: cancel, done: make(chan struct{}), status: Status{Running: true}}
	m.jobs[id] = j
	m.mu.Unlock()
	go m.loop(ctx, id, j)
	return nil
}

func (m *Manager) loop(ctx context.Context, id string, j *job) {
	defer close(j.done)
	for {
		delay := m.execute(ctx, id, j)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			m.mu.Lock()
			j.status.Running = false
			j.status.NextRunAt = time.Time{}
			m.mu.Unlock()
			return
		case <-timer.C:
		}
	}
}

func (m *Manager) execute(ctx context.Context, id string, j *job) time.Duration {
	err := m.run(ctx, id)
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	j.status.LastRunAt = now
	delay := m.interval
	if err == nil {
		j.status.LastSuccess = now
		j.status.LastError = ""
		j.status.ConsecutiveFailures = 0
	} else if !errors.Is(err, context.Canceled) {
		j.status.LastError = "COLLECTION_FAILED"
		j.status.ConsecutiveFailures++
		delay = m.backoff(j.status.ConsecutiveFailures)
	}
	j.status.NextRunAt = now.Add(delay)
	return delay
}

func (m *Manager) backoff(failures int) time.Duration {
	if failures <= 0 {
		return m.interval
	}
	delay := m.interval
	for index := 0; index < failures && delay < 2*time.Minute; index++ {
		delay *= 2
	}
	if delay > 2*time.Minute {
		return 2 * time.Minute
	}
	return delay
}

func (m *Manager) Stop(id string) {
	m.mu.RLock()
	j := m.jobs[id]
	m.mu.RUnlock()
	if j == nil {
		return
	}
	j.cancel()
	<-j.done
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

func (m *Manager) Status(id string) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if j := m.jobs[id]; j != nil {
		return j.status
	}
	return Status{}
}

func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.jobs)
}
