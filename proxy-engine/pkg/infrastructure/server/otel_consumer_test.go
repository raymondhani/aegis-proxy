package server

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOTelConsumer(t *testing.T) {
	// Setup test OTel providers
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(tracerProvider)

	meterProvider := metric.NewMeterProvider()
	otel.SetMeterProvider(meterProvider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consumer
	go StartOTelConsumer(ctx)

	// Wait for consumer to spin up
	time.Sleep(50 * time.Millisecond)

	// Emit non-blocked event
	EmitQueryEvent(QueryEvent{
		SessionID:   "session-1",
		Fingerprint: "SELECT * FROM users",
		RawQuery:    "SELECT * FROM users",
		Timestamp:   time.Now().UnixMilli(),
		Blocked:     false,
	})

	// Emit blocked event
	EmitQueryEvent(QueryEvent{
		SessionID:   "session-2",
		Fingerprint: "DROP TABLE users",
		RawQuery:    "DROP TABLE users",
		Timestamp:   time.Now().UnixMilli(),
		Blocked:     true,
	})

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Assert span is created for the blocked query
	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]
	if span.Name() != "SQL Anomaly Blocked" {
		t.Errorf("expected span name 'SQL Anomaly Blocked', got %s", span.Name())
	}

	foundActionBlocked := false
	for _, attr := range span.Attributes() {
		if string(attr.Key) == "aegis.action_blocked" && attr.Value.AsBool() == true {
			foundActionBlocked = true
		}
	}
	if !foundActionBlocked {
		t.Errorf("expected span attribute aegis.action_blocked to be true")
	}
}
