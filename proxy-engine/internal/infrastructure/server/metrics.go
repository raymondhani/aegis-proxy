package server

import "sync/atomic"

// Metrics holds the atomic counters for proxy statistics.
type Metrics struct {
	QueriesProcessed  int64 `json:"queries_processed"`
	QueriesBlocked    int64 `json:"queries_blocked"`
	ActiveConnections int64 `json:"active_connections"`
}

// GlobalMetrics is the shared instance of proxy metrics.
var GlobalMetrics Metrics

// RecordQueryProcessed increments the processed query counter.
func RecordQueryProcessed() {
	atomic.AddInt64(&GlobalMetrics.QueriesProcessed, 1)
}

// RecordQueryBlocked increments the blocked query counter.
func RecordQueryBlocked() {
	atomic.AddInt64(&GlobalMetrics.QueriesBlocked, 1)
}

// IncrementConnections increments the active connection counter.
func IncrementConnections() {
	atomic.AddInt64(&GlobalMetrics.ActiveConnections, 1)
}

// DecrementConnections decrements the active connection counter.
func DecrementConnections() {
	atomic.AddInt64(&GlobalMetrics.ActiveConnections, -1)
}
