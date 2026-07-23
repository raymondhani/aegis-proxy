package domain

// Session represents an active proxy routing mapping.
type Session struct {
	ID         string `json:"session_id"`
	TargetHost string `json:"target_host"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
}

// SessionRepository defines the interface for managing proxy session persistence.
type SessionRepository interface {
	Store(sess *Session) error
	Get(id string) (*Session, error)
	Delete(id string) error
}

// JailRepository defines the interface for managing jailed sessions due to anomalous behavior.
type JailRepository interface {
	Jail(sessionID string) error
	IsJailed(sessionID string) bool
	Unjail(sessionID string) error
	ListJailed() []string
}
