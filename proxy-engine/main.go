package main

import (
	"aegis/proxy/internal/infrastructure/repository"
	"aegis/proxy/internal/infrastructure/server"
	"aegis/proxy/internal/usecase"
	"log/slog"
	"os"
)

func main() {
	// Initialize structured JSON logging directed to stdout
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Starting Aegis DB Proxy Engine...")

	// Initialize clean architecture modules
	repo := repository.NewInMemorySessionRepository()
	useCase := usecase.NewSessionUseCase(repo)

	// Set listening addresses and configuration mode
	tcpAddr := ":5433"
	httpAddr := ":5434"
	mode := os.Getenv("AEGIS_MODE")
	if mode == "" {
		mode = "enforce" // Default to enforce mode
	}

	if port := os.Getenv("AEGIS_PROXY_TCP_PORT"); port != "" {
		tcpAddr = ":" + port
	}
	if port := os.Getenv("AEGIS_PROXY_HTTP_PORT"); port != "" {
		httpAddr = ":" + port
	}

	// 1. Start the TCP Layer 4 DB connection proxy listener
	tcpProxy := server.NewTCPProxy(useCase, mode)
	go func() {
		slog.Info("Starting DB connection interceptor", slog.String("address", tcpAddr), slog.String("mode", mode))
		if err := tcpProxy.Start(tcpAddr); err != nil {
			slog.Error("Fatal error in TCP Proxy", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// 2. Start the HTTP admin registry server (blocking on main thread)
	httpServer := server.NewHTTPServer(useCase)
	slog.Info("Starting administration API server", slog.String("address", httpAddr))
	if err := httpServer.Start(httpAddr); err != nil {
		slog.Error("Fatal error in HTTP Server", slog.Any("error", err))
		os.Exit(1)
	}
}
