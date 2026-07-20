# 🔒 Aegis Internal Notes & Developer Runbook

Welcome to the internal engineering runbook for Aegis Antigravity. This document is strictly for the core maintainers and junior developers joining the team. It details our internal processes, CI/CD pipeline management, and exactly how our Go/Python bridging operates in production.

---

## 1. 🌉 The Go/Python Bridge: Local Testing Cycle
Our architecture bridges high-performance Go proxy logic with Python data-science workflows. To test the bridging mechanism locally, you need to spin up the Go proxy and execute the Python SDK against it. 

Run this exact sequence to build a local dev image, clear old containers, and run a clean test with real database credentials:

```bash
# 1. Build the Go Proxy Engine
docker build -t raymondartin2/aegis-proxy:dev .

# 2. Purge stale containers
docker stop aegis-live 2>nul & docker rm aegis-live 2>nul

# 3. Start the Local Proxy Bridge
docker run -d --name aegis-live -p 5433:5433 -p 5434:5434 ^
  -e AEGIS_MODE="monitor" ^
  -e AEGIS_RATE_LIMIT="100" ^
  -e DB_URL="postgresql://neondb_owner:npg_xQqflBJc27Li@ep-damp-paper-adv2rbtz.c-2.us-east-1.aws.neon.tech/neondb?sslmode=require" ^
  -e AEGIS_JWT_SECRET="dev_secret_key" ^
  raymondartin2/aegis-proxy:dev

# 4. Execute the Python SDK Agents
cd python-sdk && .\venv\Scripts\python examples/live_ai_test.py

# 5. Check Proxy Telemetry
docker logs aegis-live
```

---

## 2. 🚀 CI/CD Pipeline & Professional Docker Release Runbook
We rely on GitHub Actions for Continuous Integration (CI). Every commit is subjected to AST parser verification and multi-architecture builds via `.github/workflows/docker-publish.yml`. 

To manually push a new production version (e.g., `v1.0.12`) to Docker Hub, adhering to our strict semantic versioning, use this exact sequence:

```bash
# 1. Build and Tag Multi-Arch
docker build -t raymondartin2/aegis-proxy:v1.0.12 .
docker tag raymondartin2/aegis-proxy:v1.0.12 raymondartin2/aegis-proxy:latest

# 2. Push to Registry
docker push raymondartin2/aegis-proxy:v1.0.12
docker push raymondartin2/aegis-proxy:latest
```

## 3. 🤖 Multi-Agent AI Handover Prompt
When spawning a new AI coding assistant to work on Aegis, *paste this into the new AI session to instantly restore context for future work:*

> **Project Context: Aegis AI-Native Database Proxy**
> I am Raymond, the lead developer of Aegis, an open-source Layer-4 TCP database proxy written in Go (Clean Architecture, Domain-Driven Design). It intercepts Postgres connections from AI agents, provisions Copy-on-Write sandbox branches via the Neon API, and parses SQL natively using `wasilibs/go-pgquery` to block destructive queries (e.g., DROP TABLE).
> 
> **Architecture Rules:**
> - **Multi-Agent Isolation:** Every agent gets a dedicated sandbox.
> - **Cryptographic Agent Identity:** Uses signed JWTs for authentication (no raw session IDs).
> - **Go/Python Bridging:** Python SDK communicates to the Go proxy over TCP. The Proxy terminates SSL and renegotiates it with the DB.
> - **CI/CD:** Multi-arch docker builds and strict Semantic Versioning.
> 
> **Current State:**
> - The Go proxy and Python SDK are fully functional.
> - We have implemented Rate Limiting (Token Bucket), Connection Idle Timeouts, and a structured JSON logger (`log/slog`).
> - The proxy features Advanced Threat Telemetry via OpenTelemetry (zero latency) and exposes a `/metrics` HTTP endpoint tracking processed/blocked queries.
> - It is containerized and deployed as `raymondartin2/aegis-proxy:latest` on Docker Hub.
> 
> **Goal for today:** I want to begin the next phase of the project. Please ask me what I want to focus on first.

---

## 4. 📦 PyPI Global Release Process
To package and upload the `aegis-sdk` to PyPI, follow these steps:

1. **Install Packaging Tools**:
   ```bash
   pip install --upgrade build twine
   ```

2. **Build the Source and Wheel Distributions**:
   Run this command from within the `python-sdk` directory:
   ```bash
   python -m build
   ```

3. **Upload to PyPI via Twine**:
   ```bash
   twine upload dist/*
   ```

## 5. 🔍 Environment Variables Dictionary
**Mandatory:**
* `DB_URL`: The direct connection string to the Postgres/Neon database.
* `AEGIS_MODE`: `"enforce"` (actively blocks bad queries) or `"monitor"` (logs warnings but lets them pass).
* `AEGIS_JWT_SECRET`: The secret key used to sign and verify Agent Identity JWTs.

**Optional:**
* `AEGIS_RATE_LIMIT`: Max queries allowed per minute per session. (Default: 100)
* `AEGIS_IDLE_TIMEOUT`: How long a connection can sit idle before the proxy aggressively reaps it. (Default: 15s)
* `AEGIS_PROXY_TCP_PORT`: Port for SQL TCP interception. (Default: 5433)
* `AEGIS_PROXY_HTTP_PORT`: Port for `/metrics` and admin API. (Default: 5434)
