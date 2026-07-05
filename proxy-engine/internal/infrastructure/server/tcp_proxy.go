package server

import (
	"aegis/proxy/internal/usecase"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

	sqlparser "vitess.io/vitess/go/vt/sqlparser"
)

// TCPProxy intercepts PostgreSQL connections and routes them dynamically.
type TCPProxy struct {
	useCase *usecase.SessionUseCase
}

// NewTCPProxy instantiates a TCPProxy.
func NewTCPProxy(useCase *usecase.SessionUseCase) *TCPProxy {
	return &TCPProxy{useCase: useCase}
}

// Start runs the Layer 4 TCP Listener.
func (p *TCPProxy) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	log.Printf("[TCPProxy] Listening on %s\n", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[TCPProxy] Error accepting connection: %v\n", err)
			continue
		}
		go p.handleConnection(conn)
	}
}

func (p *TCPProxy) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()
	log.Printf("[TCPProxy] New client connection from %s\n", clientConn.RemoteAddr())

	// Read initial 8 bytes of connection request.
	header := make([]byte, 8)
	_, err := io.ReadFull(clientConn, header)
	if err != nil {
		log.Printf("[TCPProxy] Error reading connection header: %v\n", err)
		return
	}

	length := binary.BigEndian.Uint32(header[0:4])
	code := binary.BigEndian.Uint32(header[4:8])

	var startupData []byte

	// SSLRequest code is 80877103. Length is 8.
	if length == 8 && code == 80877103 {
		log.Printf("[TCPProxy] Received SSLRequest. Refusing SSL to parse StartupMessage in plain text...\n")
		// Write 'N' to decline SSL
		_, err = clientConn.Write([]byte{'N'})
		if err != nil {
			log.Printf("[TCPProxy] Error writing SSL rejection: %v\n", err)
			return
		}

		// Client falls back to plain TCP and sends StartupMessage.
		startupHeader := make([]byte, 8)
		_, err = io.ReadFull(clientConn, startupHeader)
		if err != nil {
			log.Printf("[TCPProxy] Error reading StartupMessage header: %v\n", err)
			return
		}

		len2 := binary.BigEndian.Uint32(startupHeader[0:4])
		if len2 < 8 || len2 > 10000 {
			log.Printf("[TCPProxy] Invalid StartupMessage length after SSL refusal: %d\n", len2)
			return
		}

		rest := make([]byte, len2-8)
		_, err = io.ReadFull(clientConn, rest)
		if err != nil {
			log.Printf("[TCPProxy] Error reading StartupMessage body: %v\n", err)
			return
		}
		startupData = append(startupHeader, rest...)
	} else {
		// Connection didn't start with SSLRequest; it began directly with StartupMessage.
		if length < 8 || length > 10000 {
			log.Printf("[TCPProxy] Invalid StartupMessage length: %d\n", length)
			return
		}
		rest := make([]byte, length-8)
		_, err = io.ReadFull(clientConn, rest)
		if err != nil {
			log.Printf("[TCPProxy] Error reading StartupMessage body: %v\n", err)
			return
		}
		startupData = append(header, rest...)
	}

	// Parse the StartupMessage parameters
	sm, err := parseStartupMessage(startupData)
	if err != nil {
		log.Printf("[TCPProxy] Error parsing StartupMessage: %v\n", err)
		sendPgError(clientConn, fmt.Sprintf("failed to parse startup message: %v", err))
		return
	}

	// Extract and remove the session ID from parameters
	sessionID, ok := extractSessionId(sm)
	if !ok {
		log.Printf("[TCPProxy] Routing failed: No session_id found in connection parameters\n")
		sendPgError(clientConn, "aegis session ID not found in connection parameters. Use dbname?session_id=UUID")
		return
	}

	log.Printf("[TCPProxy] Extracted session ID: %s\n", sessionID)

	// Resolve dynamic backend target host
	sess, err := p.useCase.GetSession(sessionID)
	if err != nil {
		log.Printf("[TCPProxy] Routing failed: Session %s not found in registry\n", sessionID)
		sendPgError(clientConn, fmt.Sprintf("invalid or expired Aegis session ID: %s", sessionID))
		return
	}

	targetHost := sess.TargetHost
	log.Printf("[TCPProxy] Resolving target host for session %s -> %s\n", sessionID, targetHost)

	// Neon endpoints enforce SSL. We must establish a secure TLS connection.
	if !strings.Contains(targetHost, ":") {
		targetHost = targetHost + ":5432"
	}
	hostOnly, _, err := net.SplitHostPort(targetHost)
	if err != nil {
		hostOnly = targetHost
	}

	tlsConfig := &tls.Config{
		ServerName: hostOnly,
	}

	log.Printf("[TCPProxy] Connecting to Neon backend via TLS (%s, SNI: %s)...\n", targetHost, hostOnly)
	backendConn, err := tls.Dial("tcp", targetHost, tlsConfig)
	if err != nil {
		log.Printf("[TCPProxy] Connection to Neon backend failed: %v\n", err)
		sendPgError(clientConn, fmt.Sprintf("failed to establish connection to backend database: %v", err))
		return
	}
	defer backendConn.Close()

	// Serialize modified StartupMessage (without the session ID parameter) and write to backend
	modifiedStartup := sm.serialize()
	_, err = backendConn.Write(modifiedStartup)
	if err != nil {
		log.Printf("[TCPProxy] Error writing StartupMessage to backend: %v\n", err)
		return
	}

	// Inspect initial response packets from the backend to intercept and modify the AuthenticationSASL mechanism list.
	// This prevents SCRAM-SHA-256-PLUS channel binding downgrade crashes on the plain client connection.
	for {
		t, packetBytes, err := readPgPacket(backendConn)
		if err != nil {
			log.Printf("[TCPProxy] Error reading backend packet: %v\n", err)
			return
		}

		if t == 'R' && len(packetBytes) >= 9 {
			authType := binary.BigEndian.Uint32(packetBytes[5:9])
			if authType == 10 { // AuthenticationSASL
				log.Printf("[TCPProxy] Intercepted AuthenticationSASL. Filtering mechanisms...\n")
				modifiedPacket := rewriteSASLAuthPacket(packetBytes)
				_, err = clientConn.Write(modifiedPacket)
				if err != nil {
					log.Printf("[TCPProxy] Error sending modified SASL packet to client: %v\n", err)
					return
				}
				break
			}
		}

		// Forward unmodified packet to client
		_, err = clientConn.Write(packetBytes)
		if err != nil {
			log.Printf("[TCPProxy] Error forwarding packet to client: %v\n", err)
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

	// Bidirectional stream proxying with query inspection on client-to-backend
	errChan := make(chan error, 2)

	// Client to Backend (Interception and Filtering)
	go func() {
		for {
			t, packetBytes, err := readPgPacket(clientConn)
			if err != nil {
				errChan <- err
				return
			}

			// Check if packet type is 'Q' (Query) or 'P' (Parse)
			if t == 'Q' || t == 'P' {
				queryStr := ""
				if t == 'Q' {
					queryStr = string(packetBytes[5 : len(packetBytes)-1])
				} else if t == 'P' {
					idx := 5
					for idx < len(packetBytes) && packetBytes[idx] != 0 {
						idx++
					}
					idx++ // skip statement name null byte
					start := idx
					for idx < len(packetBytes) && packetBytes[idx] != 0 {
						idx++
					}
					if idx > start {
						queryStr = string(packetBytes[start:idx])
					}
				}

				if queryStr != "" {
					isDestructive, err := inspectQuery(queryStr)
					if err == nil {
						if isDestructive {
							log.Printf("[TCPProxy] Blocked destructive SQL query from client session %s: %s\n", sessionID, queryStr)
							sendPgInsufficientPrivilegeError(clientConn, "Aegis Proxy Error: Destructive SQL execution blocked at the network layer.")
							errChan <- errors.New("destructive query blocked")
							return
						}
					} else {
						// Suppress non-critical syntax error noise from proprietary PG syntax unsupported by Vitess
						if !strings.Contains(strings.ToLower(err.Error()), "syntax error") {
							log.Printf("[TCPProxy] Parser error on session %s: %v\n", sessionID, err)
						}
					}
				}
			}

			_, err = backendConn.Write(packetBytes)
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Backend to Client (Raw Copy for maximum performance)
	go func() {
		_, err := io.Copy(clientConn, backendConn)
		errChan <- err
	}()

	err = <-errChan
	if err != nil && err != io.EOF && !strings.Contains(err.Error(), "use of closed network connection") {
		log.Printf("[TCPProxy] Session %s connection closed: %v\n", sessionID, err)
	} else {
		log.Printf("[TCPProxy] Session %s connection closed cleanly\n", sessionID)
	}
}

// startupMessage represents PG 3.0 protocol StartupMessage details.
type startupMessage struct {
	version int32
	params  map[string]string
}

func parseStartupMessage(data []byte) (*startupMessage, error) {
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
	return &startupMessage{version: version, params: params}, nil
}

func extractSessionId(sm *startupMessage) (string, bool) {
	// 1. Direct param check
	if val, ok := sm.params["session_id"]; ok {
		delete(sm.params, "session_id")
		return val, true
	}
	if val, ok := sm.params["aegis_session_id"]; ok {
		delete(sm.params, "aegis_session_id")
		return val, true
	}
	// 2. Database suffix check (e.g. database=neondb?session_id=UUID)
	if dbName, ok := sm.params["database"]; ok {
		if idx := strings.Index(dbName, "?session_id="); idx != -1 {
			sessID := dbName[idx+len("?session_id="):]
			if endIdx := strings.Index(sessID, "&"); endIdx != -1 {
				sessID = sessID[:endIdx]
			}
			sm.params["database"] = dbName[:idx]
			return sessID, true
		}
	}
	return "", false
}

func (sm *startupMessage) serialize() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 0}) // placeholder for length

	var verBuf [4]byte
	binary.BigEndian.PutUint32(verBuf[:], uint32(sm.version))
	buf.Write(verBuf[:])

	for k, v := range sm.params {
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

	log.Printf("[TCPProxy] Filtered SASL mechanisms from %v to %v\n", mechanisms, filtered)
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

func inspectQuery(queryStr string) (bool, error) {
    // 1. Instantiate parser as per go doc (func New(opts Options))
    parser, err := sqlparser.New(sqlparser.Options{})
    if err != nil {
        return false, err
    }

    // 2. Parse using instance
    stmt, err := parser.Parse(queryStr)
    if err != nil {
        return false, err
    }

    // 3. Type switch using verified AST types
    switch s := stmt.(type) {
    case *sqlparser.DropTable:
        // verified DropTable type from go doc
        if !s.IfExists {
            return true, nil 
        }
    case *sqlparser.Delete:
        // verified Delete type from go doc
        return true, nil
    }

    return false, nil
}
