package server

import (
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/domain"
	"github.com/raymondhani/aegis-proxy/proxy-engine/pkg/usecase"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPServer exposes the management endpoints for routing registrations.
type HTTPServer struct {
	useCase  *usecase.SessionUseCase
	jailRepo domain.JailRepository
}

// NewHTTPServer instantiates a new HTTPServer.
func NewHTTPServer(useCase *usecase.SessionUseCase, jailRepo domain.JailRepository) *HTTPServer {
	return &HTTPServer{
		useCase:  useCase,
		jailRepo: jailRepo,
	}
}

// RegisterRequest contains session mapping inputs.
type RegisterRequest struct {
	SessionID  string `json:"session_id"`
	TargetHost string `json:"target_host"`
	TenantID   string `json:"tenant_id"`
}

// Start listens and serves HTTP requests.
func (s *HTTPServer) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", s.handleRegister)
	mux.HandleFunc("/unregister", s.handleUnregister)
	mux.HandleFunc("/session/", s.handleSessionDelete) // For DELETE /session/{session_id}
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/jail/", s.handleJail)             // Handles POST and DELETE for /jail/{session_id}
	mux.HandleFunc("/jail", s.handleJailList)          // Handles GET list of jailed session IDs
	mux.HandleFunc("/telemetry/stream", s.handleTelemetryStream)

	return http.ListenAndServe(addr, mux)
}

func (s *HTTPServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	expectedToken := os.Getenv("AEGIS_ADMIN_TOKEN")
	if expectedToken == "" || r.Header.Get("Authorization") != "Bearer "+expectedToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" || req.TargetHost == "" {
		http.Error(w, "session_id and target_host are required", http.StatusBadRequest)
		return
	}

	if err := s.useCase.RegisterSession(req.SessionID, req.TargetHost, req.TenantID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

func (s *HTTPServer) handleUnregister(w http.ResponseWriter, r *http.Request) {
	expectedToken := os.Getenv("AEGIS_ADMIN_TOKEN")
	if expectedToken == "" || r.Header.Get("Authorization") != "Bearer "+expectedToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	if err := s.useCase.UnregisterSession(req.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "unregistered"})
}

func (s *HTTPServer) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path: /session/{session_id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 || parts[2] == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	sessionID := parts[2]

	if err := s.useCase.UnregisterSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "unregistered"})
}

func (s *HTTPServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Load metrics atomically to prevent data races
	metricsCopy := Metrics{
		QueriesProcessed:  atomic.LoadInt64(&GlobalMetrics.QueriesProcessed),
		QueriesBlocked:    atomic.LoadInt64(&GlobalMetrics.QueriesBlocked),
		ActiveConnections: atomic.LoadInt64(&GlobalMetrics.ActiveConnections),
		SessionsJailed:    atomic.LoadInt64(&GlobalMetrics.SessionsJailed),
		AnomaliesDetected: atomic.LoadInt64(&GlobalMetrics.AnomaliesDetected),
	}

	_ = json.NewEncoder(w).Encode(metricsCopy)
}

func (s *HTTPServer) handleJail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 || parts[2] == "" {
		log.Printf("DEBUG: Received jail request but session ID is missing")
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	sessionID := parts[2]
	log.Printf("DEBUG: Received jail request for session ID: %s", sessionID)

	if r.Method == http.MethodPost {
		if err := s.jailRepo.Jail(sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		RecordSessionJailed()
		RecordAnomalyDetected() // Since jail is triggered by anomaly limit breach

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "jailed", "session_id": sessionID})
		return
	} else if r.Method == http.MethodDelete {
		if err := s.jailRepo.Unjail(sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "unjailed", "session_id": sessionID})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *HTTPServer) handleJailList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(s.jailRepo.ListJailed())
}

func (s *HTTPServer) handleTelemetryStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher.Flush()

	ctx := r.Context()
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			_, _ = w.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		case evt := <-TelemetryBus:
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
			flusher.Flush()
		}
	}
}
