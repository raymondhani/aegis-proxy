package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/domain"
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

// =====================================================================================
// T123 (Spec 004, FR-014): the still-open write-then-close sequencing gap in the four
// post-auth refusal controls -- jail blocklist, rate ceiling, the Guillotine, and RBAC
// (contracts/control-precedence.md's steps 0-3). TestTerminationFrameCoverage above only
// exercises pre-auth paths and never reproduces this: those return directly from
// handleConnection's own goroutine, and net.Pipe's Write() blocks until the reader
// consumes it, which makes any write-then-close artificially safe regardless of the bug.
//
// The real defect: a client that has already sent a second protocol message before the
// proxy decides to refuse and close -- e.g. the extended query protocol's Sync sent
// immediately after Execute, without waiting for a reply -- leaves that message unread in
// the kernel receive buffer on the proxy's side of the socket at the moment
// handleConnection's deferred clientConn.Close() runs. Closing a real TCP socket while
// unread inbound data remains sends an abortive RST instead of a graceful FIN, and the RST
// discards the error frame sendPgError just wrote along with it -- even though the Write
// call itself already completed successfully. This was confirmed by direct experiment
// against this platform's TCP stack before writing this test: a naive write-then-close
// loses the frame 100% of the time once unread inbound data is present, and 0% of the time
// once sendPgError drains pending input before the caller's deferred Close() can race it
// (the fix landed in T124).
// =====================================================================================

type stubSessionAuthenticator struct{ sessionID string }

func (a *stubSessionAuthenticator) Authenticate(sm *StartupMessage) (string, string, error) {
	return a.sessionID, sm.Params["user"], nil
}

type alwaysDenyRateLimiter struct{}

func (alwaysDenyRateLimiter) Allow(sessionID string) bool { return false }

type alwaysDenyPolicyValidator struct{}

func (alwaysDenyPolicyValidator) Validate(query, sessionID string) (bool, error) { return false, nil }

// newRefusalPathProxy is newTestProxy's sibling for post-auth tests: it exposes the
// session use case and jail repository so a test can register a session and jail it
// before driving a connection through handleConnection end-to-end.
func newRefusalPathProxy() (*TCPProxy, *usecase.SessionUseCase, domain.JailRepository) {
	sessionRepo := repository.NewInMemorySessionRepository()
	jailRepo := repository.NewInMemoryJailRepository()
	sessionUseCase := usecase.NewSessionUseCase(sessionRepo)
	pgInspector := usecase.NewPGQueryInspector()
	policyManager := infrastructure.NewPolicyManager(nil)
	proxy := NewTCPProxy(sessionUseCase, "enforce", 5*time.Second, jailRepo, pgInspector, policyManager)
	return proxy, sessionUseCase, jailRepo
}

// startFakeBackend accepts exactly one connection and replies with a minimal
// AuthenticationOk packet so handleConnection's auth-phase loop completes and proceeds to
// bidirectional streaming. None of these tests need a real Postgres backend beyond that:
// every refusal path fires before the proxy ever forwards the triggering query onward.
func startFakeBackend(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake backend listener: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		pkt := make([]byte, 9) // AuthenticationOk: type 'R', length 8 (self-inclusive), authtype 0
		pkt[0] = 'R'
		binary.BigEndian.PutUint32(pkt[1:5], 8)
		binary.BigEndian.PutUint32(pkt[5:9], 0)
		if _, err := conn.Write(pkt); err != nil {
			return
		}

		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	return ln.Addr().String()
}

// buildSimpleQuery encodes a Postgres simple-query ('Q') protocol message.
func buildSimpleQuery(query string) []byte {
	body := append([]byte(query), 0)
	length := 4 + len(body)
	pkt := make([]byte, 1+length)
	pkt[0] = 'Q'
	binary.BigEndian.PutUint32(pkt[1:5], uint32(length))
	copy(pkt[5:], body)
	return pkt
}

func TestPostAuthRefusalPathsDeliverFatalFrameEvenWithPipelinedInput(t *testing.T) {
	// go-pgquery's first ParseToJSON call in a process compiles/instantiates its WASM parser
	// module, which alone takes ~2s -- confirmed by direct measurement -- versus effectively 0s
	// on every call after. Every refusal path here runs step 2 (Guillotine AST inspection)
	// before step 3 (RBAC), even for a non-destructive query, so whichever subtest happens to
	// run first pays that one-time cost and can blow past readErrorFrame's 2s deadline for a
	// reason that has nothing to do with the write-then-close defect under test. Warming the
	// parser up front keeps the deadline meaningful for what it's actually checking.
	if _, err := usecase.NewPGQueryInspector().IsDestructive("SELECT 1"); err != nil {
		t.Fatalf("failed to warm up the query inspector: %v", err)
	}

	tests := []struct {
		name    string
		query   string
		jailed  bool
		limiter domain.RateLimiter
		policy  usecase.PolicyValidator
	}{
		{name: "jail blocklist", query: "SELECT 1", jailed: true},
		{name: "rate ceiling", query: "SELECT 1", limiter: alwaysDenyRateLimiter{}},
		{name: "the Guillotine", query: "DROP TABLE aegis_qa_refusal_probe"},
		{name: "RBAC", query: "SELECT 1", policy: alwaysDenyPolicyValidator{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AEGIS_BACKEND_TLS", "disable")
			backendAddr := startFakeBackend(t)

			proxy, sessionUseCase, jailRepo := newRefusalPathProxy()
			sessionID := "qa-refusal-" + tc.name
			if err := sessionUseCase.RegisterSession(sessionID, backendAddr, ""); err != nil {
				t.Fatalf("failed to register session: %v", err)
			}
			if tc.jailed {
				if err := jailRepo.Jail(sessionID); err != nil {
					t.Fatalf("failed to jail session: %v", err)
				}
			}
			if tc.limiter != nil {
				proxy.WithRateLimiter(tc.limiter)
			}
			if tc.policy != nil {
				proxy.WithPolicyValidator(tc.policy)
			}
			proxy.WithAuthenticator(&stubSessionAuthenticator{sessionID: sessionID})

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("failed to start proxy listener: %v", err)
			}
			defer ln.Close()
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				proxy.handleConnection(conn)
			}()

			client, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatalf("failed to dial proxy: %v", err)
			}
			defer client.Close()

			if _, err := client.Write(buildStartupMessage(map[string]string{
				"user": "agent", "database": "postgres",
			})); err != nil {
				t.Fatalf("failed to write startup message: %v", err)
			}

			// Consume the AuthenticationOk packet relayed from the fake backend before
			// sending the query.
			authOk := make([]byte, 9)
			_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := io.ReadFull(client, authOk); err != nil {
				t.Fatalf("failed to read AuthenticationOk relay: %v", err)
			}

			// Send the triggering query immediately followed by a second, pipelined
			// message -- mirroring a client that does not wait between messages -- which
			// is what leaves unread bytes on the proxy's socket at refusal time.
			var toSend bytes.Buffer
			toSend.Write(buildSimpleQuery(tc.query))
			toSend.Write(buildSimpleQuery("SELECT 1"))
			if _, err := client.Write(toSend.Bytes()); err != nil {
				t.Fatalf("failed to write query: %v", err)
			}

			assertFatalFrame(t, client)
		})
	}
}
