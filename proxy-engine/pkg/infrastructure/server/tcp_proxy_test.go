package server

import (
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestDefaultJWTAuthenticator(t *testing.T) {
	os.Setenv("AEGIS_JWT_SECRET", "testsecret")
	defer os.Unsetenv("AEGIS_JWT_SECRET")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "test-session-123",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte("testsecret"))

	tests := []struct {
		name      string
		params    map[string]string
		wantSub   string
		expectErr bool
	}{
		{
			name:      "Valid JWT in params",
			params:    map[string]string{"aegis_jwt": tokenString},
			wantSub:   "test-session-123",
			expectErr: false,
		},
		{
			name:      "Invalid JWT signature",
			params:    map[string]string{"aegis_jwt": tokenString + "invalid"},
			wantSub:   "",
			expectErr: true,
		},
		{
			name:      "Missing JWT",
			params:    map[string]string{},
			wantSub:   "",
			expectErr: true,
		},
		{
			name:      "JWT in database param",
			params:    map[string]string{"database": "neondb?aegis_jwt=" + tokenString},
			wantSub:   "test-session-123",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &StartupMessage{
				Version: 196608,
				Params:  tt.params,
			}
			auth := &DefaultJWTAuthenticator{}
			sub, _, err := auth.Authenticate(sm)
			if (err != nil) != tt.expectErr {
				t.Errorf("Authenticate() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if sub != tt.wantSub {
				t.Errorf("Authenticate() got = %v, want %v", sub, tt.wantSub)
			}
		})
	}
}

// TestGetOrCreateTenantRateLimiterSharesInstancePerTenant guards against the regression this
// fixed: handleConnection used to construct a brand-new StaticRateLimiter (fresh full bucket)
// for every accepted connection, so a tenant's configured rate never actually capped its
// aggregate traffic -- it capped nothing, since no connection ever lived long enough to drain
// its own private bucket. Repeated lookups for the same tenant must return the same instance.
func TestGetOrCreateTenantRateLimiterSharesInstancePerTenant(t *testing.T) {
	proxy := newTestProxy()

	first := proxy.getOrCreateTenantRateLimiter("tenant-a", 100)
	second := proxy.getOrCreateTenantRateLimiter("tenant-a", 100)
	if first != second {
		t.Fatal("expected repeated lookups for the same tenant to return the same shared limiter instance")
	}

	other := proxy.getOrCreateTenantRateLimiter("tenant-b", 100)
	if other == first {
		t.Fatal("expected a different tenant to receive its own, independent limiter instance")
	}
}

// TestGetOrCreateTenantRateLimiterDrainsAcrossSimulatedConnections is the direct regression
// test for the bug: three separate lookups for the same tenant (simulating three separate
// accepted TCP connections, exactly what handleConnection does per connection) must draw down
// one shared budget, not get three independent full buckets.
func TestGetOrCreateTenantRateLimiterDrainsAcrossSimulatedConnections(t *testing.T) {
	proxy := newTestProxy()

	const rpm = 2
	limiterConn1 := proxy.getOrCreateTenantRateLimiter("tenant-shared", rpm)
	limiterConn2 := proxy.getOrCreateTenantRateLimiter("tenant-shared", rpm)
	limiterConn3 := proxy.getOrCreateTenantRateLimiter("tenant-shared", rpm)

	if !limiterConn1.Allow("conn-1") {
		t.Fatal("expected the first request against the tenant's shared budget to be allowed")
	}
	if !limiterConn2.Allow("conn-2") {
		t.Fatal("expected the second request against the tenant's shared budget to be allowed")
	}
	if limiterConn3.Allow("conn-3") {
		t.Fatal("expected the third request to be denied: the tenant's 2/min shared budget is " +
			"already exhausted, even though this lookup simulates a brand-new connection")
	}
}
