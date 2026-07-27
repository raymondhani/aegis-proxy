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
