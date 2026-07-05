# Stage 1: Build the Go Proxy Engine
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy the workspace config and proxy source code
COPY go.work ./
COPY proxy-engine/ ./proxy-engine/

# Set working directory to proxy module and download dependencies
WORKDIR /app/proxy-engine
RUN go mod download

# Build a statically linked Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o aegis-proxy main.go

# Stage 2: Minimalist production runtime image
FROM alpine:latest

# Install certificates for secure TLS connections to Neon
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the compiled executable from the builder stage
COPY --from=builder /app/proxy-engine/aegis-proxy .

# Expose DB proxy port (5433) and administration HTTP API port (5434)
EXPOSE 5433 5434

# Run the proxy engine
CMD ["./aegis-proxy"]
