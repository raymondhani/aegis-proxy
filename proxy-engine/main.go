package main

import (
	"aegis/proxy/pkg/domain"
	"aegis/proxy/pkg/infrastructure"
	"aegis/proxy/pkg/infrastructure/observability"
	"aegis/proxy/pkg/infrastructure/repository"
	"aegis/proxy/pkg/infrastructure/server"
	"aegis/proxy/pkg/usecase"
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	// Initialize structured JSON logging directed to stdout
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Starting Aegis DB Proxy Engine...")

	// Initialize OpenTelemetry
	ctx := context.Background()
	shutdownOTel, err := observability.InitOTel(ctx)
	if err != nil {
		slog.Error("Failed to initialize OpenTelemetry", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := shutdownOTel(ctx); err != nil {
			slog.Error("Error during OTel shutdown", slog.Any("error", err))
		}
	}()

	// Connect to Redis
	redisURL := os.Getenv("AEGIS_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		slog.Error("Failed to parse Redis URL", slog.Any("error", err))
		os.Exit(1)
	}
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		slog.Error("Failed to connect to Redis", slog.Any("error", err))
		os.Exit(1)
	}

	// Start OpenTelemetry consumer for TelemetryBus
	go server.StartOTelConsumer(ctx, redisClient)

	repo := repository.NewInMemorySessionRepository()
	useCase := usecase.NewSessionUseCase(repo)
	
	// Add mock session for testing
	repo.Store(&domain.Session{
		ID:         "financial_bot_1",
		TargetHost: "ep-odd-cake-afeipmzt-pooler.c-2.us-west-2.aws.neon.tech", // Target downstream PG
		CreatedAt:  time.Now().Unix(),
	})
	
	jailRepo := repository.NewInMemoryJailRepository()
	policyManager := infrastructure.NewPolicyManager(redisClient)

	// Set listening addresses and configuration mode (bind to 0.0.0.0 for Docker compatibility)
	tcpAddr := "0.0.0.0:5433"
	httpAddr := "0.0.0.0:5434"
	mode := os.Getenv("AEGIS_MODE")
	if mode == "" {
		mode = "enforce" // Default to enforce mode
	}

	// Read jail enabled state
	jailEnabled := os.Getenv("AEGIS_JAIL_ENABLED")
	if jailEnabled == "" {
		jailEnabled = "true"
	}

	// Read and parse connection idle timeout
	idleStr := os.Getenv("AEGIS_IDLE_TIMEOUT")
	if idleStr == "" {
		idleStr = "15s" // Default to 15 seconds
	}
	idleTimeout, err := time.ParseDuration(idleStr)
	if err != nil {
		slog.Error("Invalid AEGIS_IDLE_TIMEOUT, defaulting to 15s", slog.String("value", idleStr), slog.Any("error", err))
		idleTimeout = 15 * time.Second
	}

	if port := os.Getenv("AEGIS_PROXY_TCP_PORT"); port != "" {
		if !strings.Contains(port, ":") {
			tcpAddr = "0.0.0.0:" + port
		} else {
			tcpAddr = port
		}
	}
	if port := os.Getenv("AEGIS_PROXY_HTTP_PORT"); port != "" {
		if !strings.Contains(port, ":") {
			httpAddr = "0.0.0.0:" + port
		} else {
			httpAddr = port
		}
	}

	// 1. Start the TCP Layer 4 DB connection proxy listener
	queryInspector := usecase.NewPGQueryInspector()
	tcpProxy := server.NewTCPProxy(useCase, mode, idleTimeout, jailRepo, queryInspector, policyManager)
	go func() {
		slog.Info("Starting DB connection interceptor",
			slog.String("address", tcpAddr),
			slog.String("mode", mode),
			slog.Duration("idle_timeout", idleTimeout),
			slog.String("jail_enabled", jailEnabled),
		)
		if err := tcpProxy.Start(tcpAddr); err != nil {
			slog.Error("Fatal error in TCP Proxy", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// 2. Start the HTTP admin registry server (blocking on main thread)
	httpServer := server.NewHTTPServer(useCase, jailRepo)
	slog.Info("Starting administration API server", slog.String("address", httpAddr))
	if err := httpServer.Start(httpAddr); err != nil {
		slog.Error("Fatal error in HTTP Server", slog.Any("error", err))
		os.Exit(1)
	}
}

