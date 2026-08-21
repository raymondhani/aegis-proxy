package server

import (
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/domain"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/infrastructure"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/usecase"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
	"os"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/semaphore"
)


// Authenticator defines the interface for verifying agent identities.
type Authenticator interface {
	Authenticate(sm *StartupMessage) (string, string, error)
}

// TCPProxy intercepts PostgreSQL connections and routes them dynamically.
type TCPProxy struct {
	useCase         *usecase.SessionUseCase
	mode            string
	idleTimeout     time.Duration
	jailRepo        domain.JailRepository
	queryInspector  usecase.QueryInspector
	authenticator   Authenticator
	policyValidator usecase.PolicyValidator
	policyManager   *infrastructure.PolicyManager
	rateLimiter     domain.RateLimiter

	// backendPlaintext selects a plaintext net.Dial for the backend connection instead of
	// tls.Dial. It is opt-in only, via AEGIS_BACKEND_TLS=disable, so a local Postgres
	// container that serves no TLS can be used as a backend for offline testing. TLS remains
	// the default for a missing or unrecognised value — see resolveBackendPlaintext.
	backendPlaintext bool

	// tenantLimiters holds one shared StaticRateLimiter per tenant, keyed by tenant ID (or
	// "default" when a session carries none). Only used when no custom rateLimiter has been
	// injected via WithRateLimiter. Sharing one instance per tenant across every connection
	// that tenant opens is what makes the configured rate_limit_rpm an actual ceiling on the
	// tenant's aggregate traffic — constructing a fresh limiter per accepted connection (the
	// prior behavior) reset every connection's own bucket to full, so the limit never
	// actually engaged. The tradeoff: a limiter's rate/capacity is fixed at first creation for
	// the life of the process; a later policy change for that tenant will not resize it.
	tenantLimiters   map[string]*StaticRateLimiter
	tenantLimitersMu sync.Mutex
}

// DefaultJWTAuthenticator implements basic JWT verification for OSS Tier 1.
type DefaultJWTAuthenticator struct{}

// resolveBackendPlaintext reads AEGIS_BACKEND_TLS and reports whether the backend dial should
// skip TLS. The accepted values mirror libpq's sslmode vocabulary: "disable" opts in to a
// plaintext dial, "require" is the explicit form of the default. TLS is the behaviour for every
// other input — unset, empty, or unrecognised — so no deployment can silently downgrade to
// plaintext against a real database by way of a typo. An unrecognised value is logged rather
// than ignored, so a misspelling is visible in the run's own output.
func resolveBackendPlaintext() bool {
	raw := strings.TrimSpace(os.Getenv("AEGIS_BACKEND_TLS"))
	switch strings.ToLower(raw) {
	case "":
		return false
	case "disable":
		return true
	case "require":
		return false
	default:
		slog.Warn("Unrecognised AEGIS_BACKEND_TLS value; dialing backend over TLS",
			slog.String("value", raw))
		return false
	}
}

// backendTLSMode returns the resolved mode as the value that would select it, for logging.
func (p *TCPProxy) backendTLSMode() string {
	if p.backendPlaintext {
		return "disable"
	}
	return "require"
}

// NewTCPProxy instantiates a TCPProxy.
func NewTCPProxy(useCase *usecase.SessionUseCase, mode string, idleTimeout time.Duration, jailRepo domain.JailRepository, queryInspector usecase.QueryInspector, policyManager *infrastructure.PolicyManager) *TCPProxy {
	return &TCPProxy{
		useCase:         useCase,
		mode:            mode,
		idleTimeout:     idleTimeout,
		jailRepo:        jailRepo,
		queryInspector:  queryInspector,
		authenticator:   &DefaultJWTAuthenticator{},
		policyValidator: nil, // Optional: injected via Enterprise Guardrail
		policyManager:   policyManager,
		tenantLimiters:  make(map[string]*StaticRateLimiter),

		backendPlaintext: resolveBackendPlaintext(),
	}
}

// WithAuthenticator injects a custom authenticator (Tier 3)
func (p *TCPProxy) WithAuthenticator(auth Authenticator) {
	p.authenticator = auth
}

// WithPolicyValidator injects a custom policy validator (Tier 3)
func (p *TCPProxy) WithPolicyValidator(validator usecase.PolicyValidator) {
	p.policyValidator = validator
}

// WithRateLimiter injects a custom RateLimiter (e.g. enterprise adaptive
// throttling). When none is injected, every connection for a given tenant
// shares one StaticRateLimiter sized from that tenant's resolved policy.
func (p *TCPProxy) WithRateLimiter(limiter domain.RateLimiter) {
	p.rateLimiter = limiter
}

// getOrCreateTenantRateLimiter returns the shared StaticRateLimiter for tenantKey, creating
// and caching it on first use so every connection from the same tenant draws down the same
// bucket instead of each getting its own full one.
func (p *TCPProxy) getOrCreateTenantRateLimiter(tenantKey string, rpm int) *StaticRateLimiter {
	p.tenantLimitersMu.Lock()
	defer p.tenantLimitersMu.Unlock()
	if limiter, ok := p.tenantLimiters[tenantKey]; ok {
		return limiter
	}
	limiter := NewStaticRateLimiter(rpm)
	p.tenantLimiters[tenantKey] = limiter
	return limiter
}



// Start runs the Layer 4 TCP Listener.
func (p *TCPProxy) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	slog.Info("TCPProxy listening", slog.String("address", addr),
		slog.String("backend_tls", p.backendTLSMode()))
	if p.backendPlaintext {
		slog.Warn("Backend TLS is DISABLED: backend connections will be dialed in plaintext. " +
			"This is intended for local testing only and must never be set against a real database.")
	}

	// Limit concurrent connections to prevent FD exhaustion and OOM
	connSemaphore := semaphore.NewWeighted(10000)

	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Error("Error accepting connection", slog.Any("error", err))
			continue
		}
		
		if !connSemaphore.TryAcquire(1) {
			slog.Warn("Connection limit reached, dropping new connection")
			conn.Close()
			continue
		}
		
		go func(c net.Conn) {
			defer connSemaphore.Release(1)
			p.handleConnection(c)
		}(conn)
	}
}

func (p *TCPProxy) handleConnection(clientConn net.Conn) {
	IncrementConnections()
	defer DecrementConnections()
	defer clientConn.Close()

	slog.Info("New client connection", slog.String("remote_address", clientConn.RemoteAddr().String()))

	// Enforce strict read deadline for the initial Postgres handshake to prevent Slowloris attacks
	_ = clientConn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Read initial 8 bytes of connection request.
	header := make([]byte, 8)
	_, err := io.ReadFull(clientConn, header)
	if err != nil {
		slog.Error("Error reading connection header", slog.Any("error", err))
		sendPgError(clientConn, fmt.Sprintf("failed to read connection header: %v", err))
		return
	}

	length := binary.BigEndian.Uint32(header[0:4])
	code := binary.BigEndian.Uint32(header[4:8])

	var startupData []byte

	// SSLRequest code is 80877103. Length is 8.
	if length == 8 && code == 80877103 {
		slog.Info("Received SSLRequest, refusing SSL to parse StartupMessage in plain text")
		// Write 'N' to decline SSL
		_, err = clientConn.Write([]byte{'N'})
		if err != nil {
			slog.Error("Error writing SSL rejection", slog.Any("error", err))
			sendPgError(clientConn, fmt.Sprintf("failed to write SSL rejection: %v", err))
			return
		}

		// Client falls back to plain TCP and sends StartupMessage.
		startupHeader := make([]byte, 8)
		_, err = io.ReadFull(clientConn, startupHeader)
		if err != nil {
			slog.Error("Error reading StartupMessage header", slog.Any("error", err))
			sendPgError(clientConn, fmt.Sprintf("failed to read StartupMessage header: %v", err))
			return
		}

		len2 := binary.BigEndian.Uint32(startupHeader[0:4])
		if len2 < 8 || len2 > 10000 {
			slog.Error("Invalid StartupMessage length after SSL refusal", slog.Uint64("length", uint64(len2)))
			sendPgError(clientConn, fmt.Sprintf("invalid StartupMessage length: %d", len2))
			return
		}

		rest := make([]byte, len2-8)
		_, err = io.ReadFull(clientConn, rest)
		if err != nil {
			slog.Error("Error reading StartupMessage body", slog.Any("error", err))
			sendPgError(clientConn, fmt.Sprintf("failed to read StartupMessage body: %v", err))
			return
		}
		startupData = append(startupHeader, rest...)
	} else {
		// Connection didn't start with SSLRequest; it began directly with StartupMessage.
		if length < 8 || length > 10000 {
			slog.Error("Invalid StartupMessage length", slog.Uint64("length", uint64(length)))
			sendPgError(clientConn, fmt.Sprintf("invalid StartupMessage length: %d", length))
			return
		}
		rest := make([]byte, length-8)
		_, err = io.ReadFull(clientConn, rest)
		if err != nil {
			slog.Error("Error reading StartupMessage body", slog.Any("error", err))
			sendPgError(clientConn, fmt.Sprintf("failed to read StartupMessage body: %v", err))
			return
		}
		startupData = append(header, rest...)
	}

	// Parse the StartupMessage parameters
	sm, err := parseStartupMessage(startupData)
	if err != nil {
		slog.Error("Error parsing StartupMessage", slog.Any("error", err))
		sendPgError(clientConn, fmt.Sprintf("failed to parse startup message: %v", err))
		return
	}

	// Extract and verify JWT to get session ID using injected Authenticator
	sessionID, dbUser, err := p.authenticator.Authenticate(sm)
	
	// Handshake complete, remove the read deadline for normal operations
	_ = clientConn.SetReadDeadline(time.Time{})
	
	if err != nil {
		slog.Warn("Routing failed: Authentication failed", slog.Any("error", err))
		sendPgError(clientConn, fmt.Sprintf("Aegis Proxy Error: Invalid Agent Identity: %v", err))
		return
	}

	slog.Info("Extracted session ID", slog.String("session_id", sessionID), slog.String("db_user", dbUser))

	// Resolve dynamic backend target host
	sess, err := p.useCase.GetSession(sessionID)
	if err != nil {
		slog.Warn("Routing failed: Session not found in registry", slog.String("session_id", sessionID))
		sendPgError(clientConn, fmt.Sprintf("invalid or expired Aegis session ID: %s", sessionID))
		return
	}

	targetHost := sess.TargetHost
	slog.Info("Resolving target host for session", slog.String("session_id", sessionID), slog.String("target_host", targetHost))

	// Hosted endpoints such as Neon enforce SSL, so a TLS dial is the default. A local
	// Postgres container serves no TLS at all, and dialing it with tls.Dial fails the
	// handshake and surfaces to the client as a bare EOF; AEGIS_BACKEND_TLS=disable selects
	// net.Dial for that case. See resolveBackendPlaintext for why the opt-in is narrow.
	if !strings.Contains(targetHost, ":") {
		targetHost = targetHost + ":5432"
	}
	hostOnly, _, err := net.SplitHostPort(targetHost)
	if err != nil {
		hostOnly = targetHost
	}

	var backendConn net.Conn
	if p.backendPlaintext {
		slog.Info("Connecting to backend in plaintext", slog.String("session_id", sessionID), slog.String("target_host", targetHost))
		backendConn, err = net.Dial("tcp", targetHost)
	} else {
		tlsConfig := &tls.Config{
			ServerName: hostOnly,
		}
		slog.Info("Connecting to backend via TLS", slog.String("session_id", sessionID), slog.String("target_host", targetHost), slog.String("sni", hostOnly))
		backendConn, err = tls.Dial("tcp", targetHost, tlsConfig)
	}
	if err != nil {
		slog.Error("Connection to backend failed", slog.String("session_id", sessionID), slog.String("backend_tls", p.backendTLSMode()), slog.Any("error", err))
		sendPgError(clientConn, fmt.Sprintf("failed to establish connection to backend database: %v", err))
		return
	}
	defer backendConn.Close()

	// Serialize modified StartupMessage (without the session ID parameter) and write to backend
	modifiedStartup := sm.serialize()
	_, err = backendConn.Write(modifiedStartup)
	if err != nil {
		slog.Error("Error writing StartupMessage to backend", slog.String("session_id", sessionID), slog.Any("error", err))
		sendPgError(clientConn, "failed to relay startup to backend database")
		return
	}

	// Inspect initial response packets from the backend to intercept and modify the AuthenticationSASL mechanism list.
	// This prevents SCRAM-SHA-256-PLUS channel binding downgrade crashes on the plain client connection.
	for {
		t, packetBytes, err := readPgPacket(backendConn)
		if err != nil {
			slog.Error("Error reading backend packet during auth phase", slog.String("session_id", sessionID), slog.Any("error", err))
			sendPgError(clientConn, "backend database connection failed during authentication")
			return
		}

		if t == 'R' && len(packetBytes) >= 9 {
			authType := binary.BigEndian.Uint32(packetBytes[5:9])
			if authType == 10 { // AuthenticationSASL
				slog.Info("Intercepted AuthenticationSASL. Filtering mechanisms...", slog.String("session_id", sessionID))
				modifiedPacket := rewriteSASLAuthPacket(packetBytes)
				_, err = clientConn.Write(modifiedPacket)
				if err != nil {
					slog.Error("Error sending modified SASL packet to client", slog.String("session_id", sessionID), slog.Any("error", err))
					sendPgError(clientConn, "failed to relay authentication challenge")
					return
				}
				break
			}
		}

		// Forward unmodified packet to client
		_, err = clientConn.Write(packetBytes)
		if err != nil {
			slog.Error("Error forwarding auth packet to client", slog.String("session_id", sessionID), slog.Any("error", err))
			sendPgError(clientConn, "failed to relay authentication response")
			return
		}

		// Terminate inspection if authentication is finished or fails
		if t == 'R' && len(packetBytes) >= 9 {
			authType := binary.BigEndian.Uint32(packetBytes[5:9])
			if authType == 0 { // AuthenticationOk
				break
			}
		}
		if t == 'E' { // ErrorResponse
			break
		}
	}

	// Fetch policy for the connecting session's actual tenant (empty for the direct-SDK flow,
	// which has no tenant of its own — GetPolicy treats that the same as an unknown tenant).
	tenantKey := sess.TenantID
	if tenantKey == "" {
		tenantKey = "default"
	}
	policy := p.policyManager.GetPolicy(sess.TenantID)

	// Bidirectional stream proxying with query inspection on client-to-backend
	errChan := make(chan error, 2)
	rateLimiter := p.rateLimiter
	if rateLimiter == nil {
		rateLimiter = p.getOrCreateTenantRateLimiter(tenantKey, policy.RateLimitRPM)
	}

	// Maps to track Extended Protocol state for this connection to prevent bypasses
	approvedStatements := make(map[string]bool) // stmtName -> bool
	portalToStatement := make(map[string]string) // portalName -> stmtName

	// Client to Backend (Interception, Filtering, Rate Limiting, and Timeout)
	go func() {
		for {
			// Apply read deadline to client read operations
			if p.idleTimeout > 0 {
				_ = clientConn.SetReadDeadline(time.Now().Add(p.idleTimeout))
			}
			t, packetBytes, err := readPgPacket(clientConn)
			if err != nil {
				errChan <- err
				return
			}

			// Handle Postgres Extended Protocol (P, B, E) and Simple Query (Q)
			queryStr := ""
			
			if t == 'Q' {
				queryStr = string(packetBytes[5 : len(packetBytes)-1])
			} else if t == 'P' {
				idx := 5
				startName := idx
				for idx < len(packetBytes) && packetBytes[idx] != 0 {
					idx++
				}
				stmtName := string(packetBytes[startName:idx])
				idx++ // skip statement name null byte
				
				startQuery := idx
				for idx < len(packetBytes) && packetBytes[idx] != 0 {
					idx++
				}
				if idx > startQuery {
					queryStr = string(packetBytes[startQuery:idx])
				}
				
				// Validate 'P' below, and if it passes, we add it to approvedStatements
				isDestructive, pErr := p.queryInspector.IsDestructive(queryStr)
				if pErr == nil && !isDestructive {
					approvedStatements[stmtName] = true
				}
			} else if t == 'B' {
				// Bind message: binds a portal to a statement
				idx := 5
				startPortal := idx
				for idx < len(packetBytes) && packetBytes[idx] != 0 {
					idx++
				}
				portalName := string(packetBytes[startPortal:idx])
				idx++
				
				startStmt := idx
				for idx < len(packetBytes) && packetBytes[idx] != 0 {
					idx++
				}
				stmtName := string(packetBytes[startStmt:idx])
				
				// Ensure the statement being bound was previously parsed and approved
				if !approvedStatements[stmtName] {
					RecordQueryBlocked()
					slog.Error("Blocked Bind to unapproved prepared statement", slog.String("stmt_name", stmtName))
					sendPgError(clientConn, "Aegis Proxy Error: Attempted to bind an unapproved prepared statement.")
					errChan <- errors.New("bind to unapproved statement")
					return
				}
				portalToStatement[portalName] = stmtName
			} else if t == 'E' {
				// Execute message: executes a portal
				idx := 5
				startPortal := idx
				for idx < len(packetBytes) && packetBytes[idx] != 0 {
					idx++
				}
				portalName := string(packetBytes[startPortal:idx])
				
				// Ensure the portal being executed was bound to an approved statement
				if stmtName, exists := portalToStatement[portalName]; !exists || !approvedStatements[stmtName] {
					RecordQueryBlocked()
					slog.Error("Blocked Execute of unapproved portal", slog.String("portal_name", portalName))
					sendPgError(clientConn, "Aegis Proxy Error: Attempted to execute an unapproved portal.")
					errChan <- errors.New("execute of unapproved portal")
					return
				}
			}

			if queryStr != "" {
					// 0. Check jail blocklist FIRST (O(1) sync.Map lookup)
					if p.jailRepo.IsJailed(sessionID) {
						RecordQueryBlocked()
						slog.Error("Query rejected: session is jailed",
							slog.String("session_id", sessionID),
							slog.String("query", queryStr))
						sendPgError(clientConn, "Aegis Proxy Error: Session has been jailed due to anomalous behavior.")
						errChan <- errors.New("session jailed")
						return
					}

					// 1. Enforce query rate limit
					if !rateLimiter.Allow(sessionID) {
						RecordQueryBlocked()
						slog.Error("Query rate limit exceeded",
							slog.String("session_id", sessionID),
							slog.String("query", queryStr),
							slog.Int("limit_per_min", policy.RateLimitRPM))
						sendPgError(clientConn, "Aegis Proxy Error: Query rate limit exceeded. Connection terminated.")
						errChan <- errors.New("rate limit exceeded")
						return
					}

					// 2. Perform AST inspection
					isDestructive, err := p.queryInspector.IsDestructive(queryStr)
					blocked := false
					if err == nil {
						if isDestructive {
							blocked = true
							RecordQueryBlocked()
							if p.mode == "monitor" {
								slog.Warn("SHADOW MODE: Would have blocked query",
									slog.String("session_id", sessionID),
									slog.String("query", queryStr))
								RecordQueryProcessed()
								blocked = false
							} else {
								slog.Error("Blocked destructive SQL query from client",
									slog.String("session_id", sessionID),
									slog.String("query", queryStr))
								sendPgError(clientConn, "Aegis Proxy Error: Destructive SQL execution blocked at the network layer.")
								errChan <- errors.New("destructive query blocked")
								
								EmitQueryEvent(QueryEvent{
									SessionID:   sessionID,
									Fingerprint: FingerprintSQL(queryStr),
									RawQuery:    queryStr,
									Timestamp:   time.Now().UnixMilli(),
									Blocked:     true,
								})
								return
							}
						}
					}

					// 3. Optional Enterprise Policy Validation (RBAC)
					if !blocked && p.policyValidator != nil {
						allowed, pErr := p.policyValidator.Validate(queryStr, sessionID)
						if pErr != nil || !allowed {
							blocked = true
							RecordQueryBlocked()
							slog.Error("Blocked query due to enterprise policy violation",
								slog.String("session_id", sessionID),
								slog.String("query", queryStr))
							sendPgError(clientConn, "Aegis Proxy Error: Query blocked by Enterprise RBAC Policy.")
							errChan <- errors.New("policy violation")
							
							EmitQueryEvent(QueryEvent{
								SessionID:   sessionID,
								Fingerprint: FingerprintSQL(queryStr),
								RawQuery:    queryStr,
								Timestamp:   time.Now().UnixMilli(),
								Blocked:     true,
							})
							return
						}
					}
					
					if !blocked {
						RecordQueryProcessed()
					}

					// Emit telemetry for processed queries (non-blocked)
					EmitQueryEvent(QueryEvent{
						SessionID:   sessionID,
						Fingerprint: FingerprintSQL(queryStr),
						RawQuery:    queryStr,
						Timestamp:   time.Now().UnixMilli(),
						Blocked:     blocked,
					})
				}

			// Apply write deadline to backend write operations
			if p.idleTimeout > 0 {
				_ = backendConn.SetWriteDeadline(time.Now().Add(p.idleTimeout))
			}
			_, err = backendConn.Write(packetBytes)
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Backend to Client (Piping with Idle Timeout)
	go func() {
		copyWithTimeout(clientConn, backendConn, p.idleTimeout, errChan)
	}()

	err = <-errChan
	if err != nil && err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
		slog.Error("Session connection closed with error", slog.String("session_id", sessionID), slog.Any("error", err))
	} else {
		slog.Info("Session connection closed cleanly", slog.String("session_id", sessionID))
	}
}

// StartupMessage represents PG 3.0 protocol StartupMessage details.
type StartupMessage struct {
	Version int32
	Params  map[string]string
}

func parseStartupMessage(data []byte) (*StartupMessage, error) {
	if len(data) < 8 {
		return nil, errors.New("data too short")
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if int(length) > len(data) {
		return nil, errors.New("incomplete message")
	}
	version := int32(binary.BigEndian.Uint32(data[4:8]))
	params := make(map[string]string)

	idx := 8
	for idx < int(length)-1 {
		// Read key
		keyStart := idx
		for idx < int(length) && data[idx] != 0 {
			idx++
		}
		if idx >= int(length) {
			break
		}
		key := string(data[keyStart:idx])
		idx++ // skip null byte

		if len(key) == 0 {
			break
		}

		// Read value
		valStart := idx
		for idx < int(length) && data[idx] != 0 {
			idx++
		}
		if idx >= int(length) {
			break
		}
		val := string(data[valStart:idx])
		idx++ // skip null byte

		params[key] = val
	}
	return &StartupMessage{Version: version, Params: params}, nil
}

func (a *DefaultJWTAuthenticator) Authenticate(sm *StartupMessage) (string, string, error) {
	var tokenString string

	dbUser := sm.Params["user"]

	// 1. Direct param check
	if val, ok := sm.Params["aegis_jwt"]; ok {
		tokenString = val
		delete(sm.Params, "aegis_jwt")
	} else if val, ok := sm.Params["jwt"]; ok {
		tokenString = val
		delete(sm.Params, "jwt")
	} else if dbName, ok := sm.Params["database"]; ok {
		// 2. Database suffix check (e.g. database=neondb?aegis_jwt=TOKEN)
		if idx := strings.Index(dbName, "?aegis_jwt="); idx != -1 {
			tokenString = dbName[idx+len("?aegis_jwt="):]
			if endIdx := strings.Index(tokenString, "&"); endIdx != -1 {
				tokenString = tokenString[:endIdx]
			}
			sm.Params["database"] = dbName[:idx]
		}
	}

	if tokenString == "" {
		return "", "", errors.New("aegis_jwt not found in connection parameters")
	}

	secret := os.Getenv("AEGIS_JWT_SECRET")
	if secret == "" {
		return "", "", errors.New("AEGIS_JWT_SECRET environment variable is not set")
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to parse JWT: %v", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		if sub, ok := claims["sub"].(string); ok {
			return sub, dbUser, nil
		}
		return "", "", errors.New("sub claim missing from JWT")
	}

	return "", "", errors.New("invalid JWT token")
}

func (sm *StartupMessage) serialize() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0}) // placeholder for length

	var verBuf [4]byte
	binary.BigEndian.PutUint32(verBuf[:], uint32(sm.Version))
	buf.Write(verBuf[:])

	for k, v := range sm.Params {
		buf.WriteString(k)
		buf.WriteByte(0)
		buf.WriteString(v)
		buf.WriteByte(0)
	}
	buf.WriteByte(0)

	data := buf.Bytes()
	binary.BigEndian.PutUint32(data[0:4], uint32(len(data)))
	return data
}

func sendPgError(conn net.Conn, message string) {
	var buf bytes.Buffer
	buf.WriteByte('E')            // Error response indicator
	buf.Write([]byte{0, 0, 0, 0}) // length placeholder

	buf.WriteByte('S')
	buf.WriteString("FATAL")
	buf.WriteByte(0)

	buf.WriteByte('C')
	buf.WriteString("08006") // Connection failure SQLSTATE
	buf.WriteByte(0)

	buf.WriteByte('M')
	buf.WriteString("Aegis DB Proxy error: " + message)
	buf.WriteByte(0)

	buf.WriteByte(0)

	data := buf.Bytes()
	binary.BigEndian.PutUint32(data[1:5], uint32(len(data)-1))
	_, _ = conn.Write(data)

	drainPendingInput(conn)
}

// drainPendingInput closes the write half of conn gracefully and discards whatever the peer
// has already sent but this proxy has not yet read, for a short bounded window. Every
// sendPgError call site returns immediately afterward, which triggers handleConnection's
// deferred conn.Close() moments later. On a real TCP socket, fully closing a connection
// while unread inbound bytes remain sends an abortive RST instead of a graceful FIN, and
// that RST can discard the error frame just written along with it -- even though the Write
// above already completed successfully. This is exactly the long-open FR-014 gap (client
// observes severity=None/sqlstate=None instead of FATAL/08006): a client that pipelines a
// further protocol message ahead of a response (e.g. Sync sent immediately after Execute in
// the extended query protocol, without waiting) leaves bytes sitting unread in the kernel
// receive buffer at refusal time.
//
// CloseWrite (shutdown, write-only) is unaffected by unread inbound data -- unlike a full
// Close, it always flushes the pending write and sends a graceful FIN -- so the frame is
// safely handed off to the network before the caller's later full Close can ever turn into a
// destructive RST. Draining any remaining backlog on top of that closes the residual race
// where the caller's full Close still finds unread bytes buffered on our side. Only real TCP
// connections exhibit this failure mode -- net.Pipe (used in tests) is synchronous and has no
// send/receive buffering to race, so it is left untouched.
func drainPendingInput(conn net.Conn) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.CloseWrite()
	_ = tc.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 4096)
	for {
		if _, err := tc.Read(buf); err != nil {
			break
		}
	}
}

func readPgPacket(r io.Reader) (byte, []byte, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return 0, nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length < 4 || length > 100000 {
		return 0, nil, fmt.Errorf("invalid packet length: %d", length)
	}
	body := make([]byte, length-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}

	var pkt bytes.Buffer
	pkt.WriteByte(t[0])
	pkt.Write(lenBuf[:])
	pkt.Write(body)
	return t[0], pkt.Bytes(), nil
}

func rewriteSASLAuthPacket(packet []byte) []byte {
	if len(packet) < 9 {
		return packet
	}

	// Parse mechanisms starting at offset 9
	idx := 9
	var mechanisms []string
	for idx < len(packet)-1 {
		start := idx
		for idx < len(packet) && packet[idx] != 0 {
			idx++
		}
		if idx >= len(packet) {
			break
		}
		mech := string(packet[start:idx])
		idx++ // skip null byte

		if len(mech) == 0 {
			break
		}
		mechanisms = append(mechanisms, mech)
	}

	// Filter out SCRAM-SHA-256-PLUS
	var filtered []string
	for _, m := range mechanisms {
		if m != "SCRAM-SHA-256-PLUS" {
			filtered = append(filtered, m)
		}
	}

	// Re-serialize the AuthenticationSASL packet
	var buf bytes.Buffer
	buf.WriteByte('R')
	buf.Write([]byte{0, 0, 0, 0}) // length placeholder

	var authTypeBuf [4]byte
	binary.BigEndian.PutUint32(authTypeBuf[:], 10)
	buf.Write(authTypeBuf[:])

	for _, m := range filtered {
		buf.WriteString(m)
		buf.WriteByte(0)
	}
	buf.WriteByte(0) // final null byte

	data := buf.Bytes()
	binary.BigEndian.PutUint32(data[1:5], uint32(len(data)-1))

	slog.Info("Filtered SASL mechanisms", slog.Any("original", mechanisms), slog.Any("filtered", filtered))
	return data
}

func sendPgInsufficientPrivilegeError(conn net.Conn, message string) {
	var buf bytes.Buffer
	buf.WriteByte('E')            // Error response indicator
	buf.Write([]byte{0, 0, 0, 0}) // length placeholder

	buf.WriteByte('S')
	buf.WriteString("FATAL")
	buf.WriteByte(0)

	buf.WriteByte('C')
	buf.WriteString("42501") // Insufficient Privilege SQLSTATE
	buf.WriteByte(0)

	buf.WriteByte('M')
	buf.WriteString(message)
	buf.WriteByte(0)

	buf.WriteByte(0)

	data := buf.Bytes()
	binary.BigEndian.PutUint32(data[1:5], uint32(len(data)-1))
	_, _ = conn.Write(data)
}


var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32768)
		return &b
	},
}

// copyWithTimeout copies all bytes from src to dst, enforcing a read/write timeout.
func copyWithTimeout(dst net.Conn, src net.Conn, timeout time.Duration, errChan chan error) {
	bufPtr := bufferPool.Get().(*[]byte)
	buf := *bufPtr
	defer bufferPool.Put(bufPtr)

	for {
		if timeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(timeout))
		}
		n, err := src.Read(buf)
		if n > 0 {
			if timeout > 0 {
				_ = dst.SetWriteDeadline(time.Now().Add(timeout))
			}
			_, wErr := dst.Write(buf[:n])
			if wErr != nil {
				errChan <- wErr
				return
			}
		}
		if err != nil {
			errChan <- err
			return
		}
	}
}


