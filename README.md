# Aegis: AI-Native Database Proxy

Aegis is an AI-Native Database Proxy designed to secure, sandbox, and validate database interactions performed by LLM agents. Aegis dynamically intercepts database connection attempts from AI agents, provisions isolated Copy-on-Write database branches (via the Neon API) for the duration of the agent's work, routes the connection to the sandbox branch, and executes strict validation rules before letting changes merge.

---

## Why Aegis?

Giving autonomous AI agents direct write access to production databases introduces major security risks:
* **Prompt Injection Attacks**: An agent can be manipulated by malicious user inputs into executing destructive SQL commands like `DROP TABLE` or `DELETE FROM`.
* **Data Deletion & Corruption**: AI logical errors or hallucinations can lead to accidental data loss.
* **Lack of Isolation**: Without sandboxing, multiple agents working on the same database can cause race conditions or corrupt each other's state.

**Aegis solves these challenges at the network layer**:
1. **Network Interception & AST Parsing**: Aegis intercepts agent connections at the TCP layer, parses SQL packets using the Vitess SQL AST parser, and blocks unauthorized destructive commands before they reach the database engine.
2. **Dynamic Copy-on-Write Sandboxes**: It provisions dynamic, isolated database branches via the Neon API, routing the agent's queries safely into a sandbox.
3. **Strict Schema Merge Rules**: It executes schema snapshot validations (e.g., verifying that no tables or columns are dropped and that all new tables contain primary keys) before allowing database changes to merge.
4. **Production Readiness**: Supports Query Rate Limiting, Connection Idle Timeouts, Structured JSON logging, and "/metrics" monitoring endpoints.

---

## Production Deployment

### 1. Environment Configuration

Define the following environment variables. Ensure these are set in your production container runner or read from a secure secrets provider:

```env
# Neon API Settings
NEON_API_KEY=your_neon_api_key_here
NEON_PROJECT_ID=your_neon_project_id_here
NEON_BASE_URL=https://console.neon.tech/api/v2

# Aegis Proxy Configuration
AEGIS_MODE=enforce                 # Mode of operation: "enforce" (active blocking) or "monitor" (shadow mode)
AEGIS_PROXY_TCP_PORT=5433         # Port for database connection interception
AEGIS_PROXY_HTTP_PORT=5434        # Port for HTTP admin API and /metrics endpoint
AEGIS_IDLE_TIMEOUT=15s            # Idle timeout duration for connection reaping (e.g. 15s, 5m)
AEGIS_RATE_LIMIT=100              # Maximum query rate allowed per connection (queries per minute)
```

### 2. Run using Docker

Aegis is packaged as a multi-stage Docker image that compiles the Go binary using Alpine Go and runs it in a minimal, secure runtime environment.

Build the image:
```bash
docker build -t aegis-proxy .
```

Run in Enforce Mode (production default):
```bash
docker run -d \
  -p 5433:5433 \
  -p 5434:5434 \
  -e NEON_API_KEY="your_api_key" \
  -e NEON_PROJECT_ID="your_project_id" \
  aegis-proxy
```

Run in Monitor Mode (shadow logging):
```bash
docker run -d \
  -p 5433:5433 \
  -p 5434:5434 \
  -e AEGIS_MODE="monitor" \
  -e NEON_API_KEY="your_api_key" \
  -e NEON_PROJECT_ID="your_project_id" \
  aegis-proxy
```

### 3. Monitoring & Metrics

The Admin API exposes a `/metrics` HTTP endpoint on port `5434` for monitoring integration:
```bash
curl http://localhost:5434/metrics
```
Response format:
```json
{
  "queries_processed": 1050,
  "queries_blocked": 4,
  "active_connections": 2
}
```

---

## Secrets Management

> [!WARNING]
> **Never commit your `.env` file or hardcode credentials into the repository.**
> Always configure secret management practices in production. Use secure stores like Google Secret Manager, AWS Secrets Manager, or HashiCorp Vault to inject `NEON_API_KEY` dynamically.
> Verify that the `.env` file is excluded from your git index by ensuring it is present in the `.gitignore` file.

---

## Development & Local Testing

For detailed instructions on building, running, and modifying the proxy, see [AGENTS.md](file:///e:/aegis-project/AGENTS.md).
