package repository

import (
	"aegis/proxy/pkg/domain"
	"fmt"
	"sync"
)

// InMemorySessionRepository stores session routing mappings in memory.
type InMemorySessionRepository struct {
	mu       sync.RWMutex
	sessions map[string]*domain.Session
}

// NewInMemorySessionRepository instantiates an InMemorySessionRepository.
func NewInMemorySessionRepository() *InMemorySessionRepository {
	return &InMemorySessionRepository{
		sessions: make(map[string]*domain.Session),
	}
}

// Store saves a session mapping.
func (r *InMemorySessionRepository) Store(sess *domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sess.ID] = sess
	return nil
}

// Get retrieves a session mapping or returns an error if not found.
func (r *InMemorySessionRepository) Get(id string) (*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sess, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found in registry", id)
	}
	return sess, nil
}

// Delete removes a session mapping.
func (r *InMemorySessionRepository) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
	return nil
}
