package repository

import (
	"sync"
	"time"

	"aegis/proxy/pkg/domain"
)

// InMemoryJailRepository is a sync.Map-backed implementation of domain.JailRepository.
// Uses sync.Map because reads (IsJailed checks on every query) vastly outnumber writes (jail/unjail operations).
type InMemoryJailRepository struct {
	// store maps sessionID (string) → jailed_at_unix_timestamp (int64)
	store sync.Map
}

// NewInMemoryJailRepository creates a new InMemoryJailRepository.
func NewInMemoryJailRepository() *InMemoryJailRepository {
	return &InMemoryJailRepository{}
}

// Jail marks a session as jailed, recording the current Unix timestamp.
func (r *InMemoryJailRepository) Jail(sessionID string) error {
	r.store.Store(sessionID, time.Now().Unix())
	return nil
}

// IsJailed returns true if the given session is currently jailed.
func (r *InMemoryJailRepository) IsJailed(sessionID string) bool {
	_, ok := r.store.Load(sessionID)
	return ok
}

// Unjail removes a session from the jail registry.
func (r *InMemoryJailRepository) Unjail(sessionID string) error {
	r.store.Delete(sessionID)
	return nil
}

// ListJailed returns a slice of all currently jailed session IDs.
func (r *InMemoryJailRepository) ListJailed() []string {
	var jailed []string
	r.store.Range(func(key, value any) bool {
		if id, ok := key.(string); ok {
			jailed = append(jailed, id)
		}
		return true
	})
	return jailed
}

// Compile-time interface compliance check.
var _ domain.JailRepository = (*InMemoryJailRepository)(nil)
