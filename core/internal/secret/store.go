package secret

import (
	"context"
	"errors"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

const serviceName = "hgdb_monitor"

var ErrNotFound = errors.New("secret not found")

type Store interface {
	Set(context.Context, string, string) error
	Get(context.Context, string) (string, error)
	Delete(context.Context, string) error
}

// OSStore delegates encryption and persistence to the operating system's
// credential service. It intentionally has no plaintext file fallback.
type OSStore struct{}

func NewOSStore() *OSStore { return &OSStore{} }

func (*OSStore) Set(_ context.Context, id, value string) error {
	return keyring.Set(serviceName, id, value)
}

func (*OSStore) Get(_ context.Context, id string) (string, error) {
	value, err := keyring.Get(serviceName, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return value, err
}

func (*OSStore) Delete(_ context.Context, id string) error {
	err := keyring.Delete(serviceName, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// MemoryStore is used by tests; it never leaves credentials on disk.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{values: make(map[string]string)} }

func (s *MemoryStore) Set(_ context.Context, id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[id] = value
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[id]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[id]; !ok {
		return ErrNotFound
	}
	delete(s.values, id)
	return nil
}
