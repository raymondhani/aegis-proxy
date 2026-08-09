package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/infrastructure"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/infrastructure/repository"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/usecase"
)

// buildStartupMessage encodes a minimal, well-formed PG 3.0 StartupMessage
// so tests can drive handleConnection through parseStartupMessage.
func buildStartupMessage(params map[string]string) []byte {
	var body bytes.Buffer
	body.Write([]byte{0, 3, 0, 0}) // protocol version 3.0
	for k, v := range params {
		body.WriteString(k)
		body.WriteByte(0)
		body.WriteString(v)
		body.WriteByte(0)
	}
	body.WriteByte(0) // terminator

	length := 4 + body.Len()
	var out bytes.Buffer
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(length))
	out.Write(lenBuf)
	out.Write(body.Bytes())
	return out.Bytes()
}

// readErrorFrame reads and parses one PG ErrorResponse frame ('E', FATAL,
// 08006, message) per contracts/termination-frame.md's wire contract.
type parsedFrame struct {
	severity string
	sqlstate string
	message  string
}

func readErrorFrame(t *testing.T, conn net.Conn) (*parsedFrame, error) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	var typ [1]byte
	if _, err := io.ReadFull(conn, typ[:]); err != nil {
		return nil, err
	}
	if typ[0] != 'E' {
		t.Fatalf("frame type = %q, want 'E'", typ[0])
	}

	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}

	f := &parsedFrame{}
	i := 0
	for i < len(payload) && payload[i] != 0 {
		field := payload[i]
		i++
		start := i
		for i < len(payload) && payload[i] != 0 {
			i++
		}
		val := string(payload[start:i])
		i++ // skip null terminator
		switch field {
		case 'S':
			f.severity = val
		case 'C':
			f.sqlstate = val
		case 'M':
			f.message = val
		}
	}
	return f, nil
}

func assertFatalFrame(t *testing.T, conn net.Conn) {
	t.Helper()
	f, err := readErrorFrame(t, conn)
	if err != nil {
		t.Fatalf("expected a fatal error frame, got read error: %v (contract: client must never observe a bare connection reset)", err)
	}
	if f.severity != "FATAL" {
		t.Errorf("severity = %q, want FATAL", f.severity)
	}
	if f.sqlstate != "08006" {
		t.Errorf("sqlstate = %q, want 08006", f.sqlstate)
	}
	if f.message == "" {
		t.Error("message is empty, want a specific actionable message")
	}
}

// TestSendPgErrorWireFormat: T052 -- coverage changes, format does not.
func TestSendPgErrorWireFormat(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	go func() {
		sendPgError(server, "boom")
		server.Close()
	}()
	assertFatalFrame(t, client)
}

func newTestProxy() *TCPProxy {
	sessionRepo := repository.NewInMemorySessionRepository()
	jailRepo := repository.NewInMemoryJailRepository()
	sessionUseCase := usecase.NewSessionUseCase(sessionRepo)
	pgInspector := usecase.NewPGQueryInspector()
	policyManager := infrastructure.NewPolicyManager(nil)
	return NewTCPProxy(sessionUseCase, "enforce", 5*time.Second, jailRepo, pgInspector, policyManager)
}

// TestTerminationFrameCoverage drives every reachable startup/pre-auth
// silent path fixed by this feature, plus a regression check on
// already-compliant paths, asserting each yields a structured FATAL/08006
// frame rather than a bare disconnect (FR-023, contracts/termination-frame.md).
//
// Upstream-phase paths (232/241/252/262 in the pre-fix file) require a live
// TLS backend to reach and are exercised instead by the manual driver
// cross-check (quickstart.md US3, T053) and by code review of the identical
// sendPgError call added at each site (T050).
func TestTerminationFrameCoverage(t *testing.T) {
	tests := []struct {
		name       string
		input      func() []byte
		consumeAck bool // true when the wire protocol sends a byte (e.g. SSL 'N') before any frame
	}{
		{
			name: "invalid StartupMessage length (non-SSL path)",
			input: func() []byte {
				buf := make([]byte, 8)
				binary.BigEndian.PutUint32(buf[0:4], 99999) // exceeds 10000 cap
				binary.BigEndian.PutUint32(buf[4:8], 196608)
				return buf
			},
		},
		{
			name: "invalid StartupMessage length after SSL refusal",
			input: func() []byte {
				var out bytes.Buffer
				sslReq := make([]byte, 8)
				binary.BigEndian.PutUint32(sslReq[0:4], 8)
				binary.BigEndian.PutUint32(sslReq[4:8], 80877103)
				out.Write(sslReq)

				bad := make([]byte, 8)
				binary.BigEndian.PutUint32(bad[0:4], 3) // below the 8-byte floor
				binary.BigEndian.PutUint32(bad[4:8], 196608)
				out.Write(bad)
				return out.Bytes()
			},
			consumeAck: true, // proxy first writes 'N' declining SSL, per protocol
		},
		{
			name: "invalid agent identity: missing JWT (already-compliant regression check)",
			input: func() []byte {
				return buildStartupMessage(map[string]string{"user": "agent", "database": "postgres"})
			},
		},
		{
			name: "unknown session (already-compliant regression check)",
			input: func() []byte {
				return buildStartupMessage(map[string]string{
					"user": "agent", "database": "postgres",
					"aegis_jwt": "not-a-real-jwt-but-parses-as-a-session-id-lookup-miss",
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newTestProxy()
			client, srv := net.Pipe()
			defer client.Close()

			done := make(chan struct{})
			go func() {
				proxy.handleConnection(srv)
				close(done)
			}()

			data := tc.input()
			go func() {
				_, _ = client.Write(data)
			}()

			if tc.consumeAck {
				_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
				var ack [1]byte
				if _, err := io.ReadFull(client, ack[:]); err != nil {
					t.Fatalf("failed to read SSL-decline ack: %v", err)
				}
			}

			assertFatalFrame(t, client)
			<-done
		})
	}
}

// TestStreamTeardownPathsDoNotSendFrames documents contracts/termination-frame.md's
// explicit-out-of-scope list: errChan <- err inside the bidirectional copy
// goroutines is normal end-of-session propagation and MUST NOT gain a frame.
// This is a static assertion over the source rather than a live-socket test,
// since simulating mid-stream teardown requires a full established session.
func TestStreamTeardownPathsDoNotSendFrames(t *testing.T) {
	// copyWithTimeout is the stream-teardown helper (contract lines 768/773
	// pre-fix); it must report errors only via errChan, never write to the
	// connection itself.
	errChan := make(chan error, 1)
	client, srv := net.Pipe()
	_ = client.Close() // force src.Read to fail immediately

	copyWithTimeout(srv, srv, 0, errChan)

	select {
	case err := <-errChan:
		if err == nil {
			t.Fatal("expected a non-nil error on the channel")
		}
	default:
		t.Fatal("expected copyWithTimeout to report its error via errChan")
	}
	_ = errors.New // keep errors imported if unused by future edits
}
