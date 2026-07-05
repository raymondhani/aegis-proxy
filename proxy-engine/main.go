package main

import (
	"aegis/proxy/internal/infrastructure/repository"
	"aegis/proxy/internal/infrastructure/server"
	"aegis/proxy/internal/usecase"
	"log"
	"os"
)

func main() {
	log.Println("Starting Aegis DB Proxy Engine...")

	// Initialize clean architecture modules
	repo := repository.NewInMemorySessionRepository()
	useCase := usecase.NewSessionUseCase(repo)

	// Set listening addresses (defaults can be overridden via environment variables)
	tcpAddr := ":5433"
	httpAddr := ":5434"

	if port := os.Getenv("AEGIS_PROXY_TCP_PORT"); port != "" {
		tcpAddr = ":" + port
	}
	if port := os.Getenv("AEGIS_PROXY_HTTP_PORT"); port != "" {
		httpAddr = ":" + port
	}

	// 1. Start the TCP Layer 4 DB connection proxy listener
	tcpProxy := server.NewTCPProxy(useCase)
	go func() {
		log.Printf("Starting DB connection interceptor on %s\n", tcpAddr)
		if err := tcpProxy.Start(tcpAddr); err != nil {
			log.Fatalf("Fatal error in TCP Proxy: %v", err)
		}
	}()

	// 2. Start the HTTP admin registry server (blocking on main thread)
	httpServer := server.NewHTTPServer(useCase)
	log.Printf("Starting administration API server on %s\n", httpAddr)
	if err := httpServer.Start(httpAddr); err != nil {
		log.Fatalf("Fatal error in HTTP Server: %v", err)
	}
}
