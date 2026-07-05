package domain

// Session represents an active proxy routing mapping.
type Session struct {
	ID         string `json:"session_id"`
	TargetHost string `json:"target_host"`
}

// SessionRepository defines the interface for managing proxy session persistence.
type SessionRepository interface {
	Store(sess *Session) error
	Get(id string) (*Session, error)
	Delete(id string) error
}
