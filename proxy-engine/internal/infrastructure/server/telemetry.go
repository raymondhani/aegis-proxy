package server

// QueryEvent represents a single SQL query observation emitted for anomaly detection.
type QueryEvent struct {
	SessionID   string `json:"session_id"`
	Fingerprint string `json:"fingerprint"`
	RawQuery    string `json:"raw_query"`
	Timestamp   int64  `json:"timestamp_unix"`
	Blocked     bool   `json:"blocked"`
}

// TelemetryBus is the shared channel for streaming query events to consumers (SSE, anomaly engine).
// Buffered at 4096 to absorb bursts without blocking the hot proxy path.
var TelemetryBus = make(chan QueryEvent, 4096)

// EmitQueryEvent sends a query event into the telemetry bus without blocking.
// If the buffer is full, the event is silently dropped to protect proxy latency.
func EmitQueryEvent(evt QueryEvent) {
	select {
	case TelemetryBus <- evt:
	default:
		// Buffer full — drop silently. Never block the hot path.
	}
}
