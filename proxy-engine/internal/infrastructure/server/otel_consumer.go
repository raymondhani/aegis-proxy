package server

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// StartOTelConsumer starts the background goroutine to process QueryEvents from TelemetryBus
func StartOTelConsumer(ctx context.Context) {
	tracer := otel.Tracer("aegis-proxy/interceptor")
	meter := otel.Meter("aegis-proxy/metrics")

	queriesProcessed, err := meter.Int64Counter("aegis.proxy.queries.processed", metric.WithDescription("Number of processed queries"))
	if err != nil {
		slog.Error("Failed to create queries processed metric", slog.Any("error", err))
		return
	}

	queriesBlocked, err := meter.Int64Counter("aegis.proxy.queries.blocked", metric.WithDescription("Number of blocked queries"))
	if err != nil {
		slog.Error("Failed to create queries blocked metric", slog.Any("error", err))
		return
	}

	slog.Info("Started OpenTelemetry background consumer for TelemetryBus")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping OTel background consumer")
			return
		case evt := <-TelemetryBus:
			// Increment metrics
			queriesProcessed.Add(ctx, 1)
			if evt.Blocked {
				queriesBlocked.Add(ctx, 1)

				// Create trace span for blocked queries
				_, span := tracer.Start(ctx, "SQL Anomaly Blocked")
				span.SetAttributes(
					attribute.String("session_id", evt.SessionID),
					attribute.String("sql.query", evt.RawQuery),
					attribute.String("sql.fingerprint", evt.Fingerprint),
					attribute.Bool("aegis.action_blocked", true),
				)
				span.End()
			}
		}
	}
}
