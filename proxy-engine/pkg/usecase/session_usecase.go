package usecase

import (
	"aegis/proxy/pkg/domain"
	"errors"
)

// SessionUseCase coordinates session registration and database target resolution.
type SessionUseCase struct {
	repo domain.SessionRepository
}

// NewSessionUseCase instantiates a new SessionUseCase.
func NewSessionUseCase(repo domain.SessionRepository) *SessionUseCase {
	return &SessionUseCase{repo: repo}
}

// RegisterSession saves a session ID and its target database endpoint mapping.
func (u *SessionUseCase) RegisterSession(id, targetHost string) error {
	if id == "" || targetHost == "" {
		return errors.New("invalid arguments: session_id and target_host are required")
	}
	sess := &domain.Session{
		ID:         id,
		TargetHost: targetHost,
	}
	return u.repo.Store(sess)
}

// UnregisterSession deletes a session ID mapping.
func (u *SessionUseCase) UnregisterSession(id string) error {
	if id == "" {
		return errors.New("invalid arguments: session_id is required")
	}
	return u.repo.Delete(id)
}

// GetSession retrieves the session info for a given ID.
func (u *SessionUseCase) GetSession(id string) (*domain.Session, error) {
	if id == "" {
		return nil, errors.New("invalid arguments: session_id is required")
	}
	return u.repo.Get(id)
}
