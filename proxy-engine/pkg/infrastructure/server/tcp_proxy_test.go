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
// one shared budget, not get three independent full buckets. rpm is 120, not 2, so that
// burstCapacity(rpm) (Spec 004 T131, contracts/rate-ceiling.md decision 1 -- capacity is about a
// second's worth of the budget, not the whole minute) still yields a capacity of exactly 2.
func TestGetOrCreateTenantRateLimiterDrainsAcrossSimulatedConnections(t *testing.T) {
	proxy := newTestProxy()

	const rpm = 120
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
		t.Fatal("expected the third request to be denied: the tenant's shared burst capacity of 2 is " +
			"already exhausted, even though this lookup simulates a brand-new connection")
	}
}


// TestResolveBackendPlaintextDefaultsToTLS pins the security-relevant half of the
// AEGIS_BACKEND_TLS contract: plaintext is reachable only through the exact opt-in value, and
// every other input — unset, empty, a typo, or a value borrowed from some other tool's
// vocabulary — must keep dialing TLS. A regression here would silently downgrade a real
// database connection to plaintext, which is why the unrecognised cases are enumerated rather
// than left to a single "not disable" assertion.
func TestResolveBackendPlaintextDefaultsToTLS(t *testing.T) {
	cases := []struct {
		name          string
		set           bool
		value         string
		wantPlaintext bool
	}{
		{name: "unset", set: false, wantPlaintext: false},
		{name: "empty", set: true, value: "", wantPlaintext: false},
		{name: "disable", set: true, value: "disable", wantPlaintext: true},
		{name: "disable mixed case", set: true, value: "DiSaBlE", wantPlaintext: true},
		{name: "disable padded", set: true, value: "  disable  ", wantPlaintext: true},
		{name: "require", set: true, value: "require", wantPlaintext: false},
		{name: "typo disabled", set: true, value: "disabled", wantPlaintext: false},
		{name: "off", set: true, value: "off", wantPlaintext: false},
		{name: "false", set: true, value: "false", wantPlaintext: false},
		{name: "zero", set: true, value: "0", wantPlaintext: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Unsetenv("AEGIS_BACKEND_TLS")
			if tc.set {
				os.Setenv("AEGIS_BACKEND_TLS", tc.value)
				defer os.Unsetenv("AEGIS_BACKEND_TLS")
			}

			if got := resolveBackendPlaintext(); got != tc.wantPlaintext {
				t.Fatalf("AEGIS_BACKEND_TLS=%q: got plaintext=%v, want %v",
					tc.value, got, tc.wantPlaintext)
			}
		})
	}
}

// TestNewTCPProxyCapturesBackendModeAtConstruction confirms the resolved mode is carried on the
// proxy itself, so both tiers get it from the one shared constructor rather than each reading
// the environment at its own dial site.
func TestNewTCPProxyCapturesBackendModeAtConstruction(t *testing.T) {
	os.Setenv("AEGIS_BACKEND_TLS", "disable")
	defer os.Unsetenv("AEGIS_BACKEND_TLS")

	p := NewTCPProxy(nil, "oss", 0, nil, nil, nil)
	if !p.backendPlaintext {
		t.Fatal("expected AEGIS_BACKEND_TLS=disable to select a plaintext backend dial")
	}
	if p.backendTLSMode() != "disable" {
		t.Fatalf("expected reported mode \"disable\", got %q", p.backendTLSMode())
	}

	os.Setenv("AEGIS_BACKEND_TLS", "require")
	q := NewTCPProxy(nil, "oss", 0, nil, nil, nil)
	if q.backendPlaintext {
		t.Fatal("expected AEGIS_BACKEND_TLS=require to keep the TLS backend dial")
	}
	if q.backendTLSMode() != "require" {
		t.Fatalf("expected reported mode \"require\", got %q", q.backendTLSMode())
	}
}
