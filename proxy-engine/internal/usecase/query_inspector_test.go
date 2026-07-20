package usecase_test

import (
	"aegis/proxy/internal/usecase"
	"testing"
)

func TestPGQueryInspector_IsDestructive(t *testing.T) {
	inspector := usecase.NewPGQueryInspector()

	tests := []struct {
		name          string
		query         string
		isDestructive bool
		expectError   bool
	}{
		{
			name:          "Safe SELECT",
			query:         "SELECT * FROM users;",
			isDestructive: false,
			expectError:   false,
		},
		{
			name:          "JSONB Operator",
			query:         "SELECT data ->> 'name' FROM json_table WHERE tags ? 'urgent';",
			isDestructive: false,
			expectError:   false,
		},
		{
			name:          "DROP TABLE",
			query:         "DROP TABLE users;",
			isDestructive: true,
			expectError:   false,
		},
		{
			name:          "DELETE",
			query:         "DELETE FROM sessions WHERE id = 1;",
			isDestructive: true,
			expectError:   false,
		},
		{
			name:          "TRUNCATE",
			query:         "TRUNCATE TABLE logs;",
			isDestructive: true,
			expectError:   false,
		},
		{
			name:          "Syntax Error",
			query:         "SELEC * FROM;",
			isDestructive: false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destructive, err := inspector.IsDestructive(tt.query)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error: %v, got: %v", tt.expectError, err)
			}
			if destructive != tt.isDestructive {
				t.Errorf("expected isDestructive: %v, got: %v", tt.isDestructive, destructive)
			}
		})
	}
}
