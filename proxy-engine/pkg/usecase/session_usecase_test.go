package usecase_test

import (
	"fmt"
	"testing"

	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/domain"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/usecase"
)

// fakeSessionRepository is a minimal in-package domain.SessionRepository double, kept local to
// this test file so it never has to import pkg/infrastructure (Constitution Principle III,
// enforced even for tests by architecture_test.go's TestUsecaseNeverImportsInfrastructure).
type fakeSessionRepository struct {
	sessions map[string]*domain.Session
}

func newFakeSessionRepository() *fakeSessionRepository {
	return &fakeSessionRepository{sessions: make(map[string]*domain.Session)}
}

func (r *fakeSessionRepository) Store(sess *domain.Session) error {
	r.sessions[sess.ID] = sess
	return nil
}

func (r *fakeSessionRepository) Get(id string) (*domain.Session, error) {
	sess, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return sess, nil
}

func (r *fakeSessionRepository) Delete(id string) error {
	delete(r.sessions, id)
	return nil
}

// TestRegisterSessionStoresTenantID guards against the regression this fixed: the proxy used
// to discard the registering caller's tenant_id entirely, which is what forced tcp_proxy.go to
// fall back to a hardcoded "default" policy lookup instead of the connecting tenant's own.
func TestRegisterSessionStoresTenantID(t *testing.T) {
	uc := usecase.NewSessionUseCase(newFakeSessionRepository())

	if err := uc.RegisterSession("sess-1", "target.example.com", "tenant-abc"); err != nil {
		t.Fatalf("RegisterSession returned an error: %v", err)
	}

	sess, err := uc.GetSession("sess-1")
	if err != nil {
		t.Fatalf("GetSession returned an error: %v", err)
	}
	if sess.TenantID != "tenant-abc" {
		t.Fatalf("TenantID = %q, want %q", sess.TenantID, "tenant-abc")
	}
}

// TestRegisterSessionAllowsEmptyTenantID covers the direct-SDK flow, which has no SaaS control
// plane in the loop and therefore no tenant_id to supply -- this must keep working exactly as
// before, falling back to PolicyManager's own empty-tenant default rather than erroring.
func TestRegisterSessionAllowsEmptyTenantID(t *testing.T) {
	uc := usecase.NewSessionUseCase(newFakeSessionRepository())

	if err := uc.RegisterSession("sess-2", "target.example.com", ""); err != nil {
		t.Fatalf("RegisterSession returned an error for an empty tenant_id: %v", err)
	}

	sess, err := uc.GetSession("sess-2")
	if err != nil {
		t.Fatalf("GetSession returned an error: %v", err)
	}
	if sess.TenantID != "" {
		t.Fatalf("TenantID = %q, want empty", sess.TenantID)
	}
}
