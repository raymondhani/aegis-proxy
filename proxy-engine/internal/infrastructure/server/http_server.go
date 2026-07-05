package server

import (
	"aegis/proxy/internal/usecase"
	"encoding/json"
	"net/http"
	"strings"
)

// HTTPServer exposes the management endpoints for routing registrations.
type HTTPServer struct {
	useCase *usecase.SessionUseCase
}

// NewHTTPServer instantiates a new HTTPServer.
func NewHTTPServer(useCase *usecase.SessionUseCase) *HTTPServer {
	return &HTTPServer{useCase: useCase}
}

// RegisterRequest contains session mapping inputs.
type RegisterRequest struct {
	SessionID  string `json:"session_id"`
	TargetHost string `json:"target_host"`
}

// Start listens and serves HTTP requests.
func (s *HTTPServer) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", s.handleRegister)
	mux.HandleFunc("/unregister", s.handleUnregister)
	mux.HandleFunc("/session/", s.handleSessionDelete) // For DELETE /session/{session_id}

	return http.ListenAndServe(addr, mux)
}

func (s *HTTPServer) handleRegister(w http.ResponseWriter, r *http.Request) {
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

	if err := s.useCase.RegisterSession(req.SessionID, req.TargetHost); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

func (s *HTTPServer) handleUnregister(w http.ResponseWriter, r *http.Request) {
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
