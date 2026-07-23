# AI Agent Developer Guide (AGENTS.md)

This file contains machine-readable instructions, specifications, and architecture boundaries for AI agents and code generation assistants working in the Aegis repository. Follow these guidelines strictly to preserve code quality, project consistency, and multi-agent stability.

---

## 1. Monorepo Structure and System Layout

```
/
├── .env                  # Shared environment secrets (NEON_API_KEY, etc.)
├── README.md             # Developer setup instructions
├── AGENTS.md             # Machine-readable instructions (this file)
├── go.work               # Go workspace configuration
├── proxy-engine/         # Go-based Layer 4 TCP & Control HTTP proxy
│   ├── go.mod            # Module path: aegis/proxy
│   ├── main.go           # Core application bootstrapper
│   └── internal/         # Clean Architecture packages
│       ├── domain/       # Enterprise business models and interfaces
│       ├── usecase/      # Application-specific orchestrations
│       └── infrastructure/ # Delivery and database engines
│           ├── repository/ # Data access adapters (e.g. InMemory)
│           └── server/     # HTTP Control and TCP interception engines
└── python-sdk/           # Python SDK and decorator library
    ├── neon_provisioner.py # Branch manager and @safe_db_run decorator
    └── test_sdk.py       # Integration verification test suite
```

---

## 2. Multi-Agent Workflow Constraints

When generating code for Aegis, agents must respect the concurrent multi-agent architecture:
- **Sandbox Isolation:** Aegis serves multiple autonomous agents simultaneously. Never hardcode static states that could cause race conditions between agents.
- **Ephemeral State:** Agents operate on Copy-on-Write branches provisioned by Neon. Do not write logic in the Go Proxy that assumes a single, static underlying database instance.
- **Zero-Trust Security:** Treat all incoming SQL from the Python SDK as actively hostile. Rely on Z-Score anomaly detection and immediately invoke **The Guillotine** (TCP termination) upon detecting threats.

---

## 3. Clean Architecture Boundaries (Go Proxy Engine)

Agents modifying the `proxy-engine` must respect the following directional dependency boundaries:

```
[infrastructure] ──> [usecase] ──> [domain]
```

### Layer Definitions
1. **Domain (`internal/domain`)**: Must not import any other packages in `internal/`. Contains structs and interfaces. No external HTTP/TCP packages allowed.
2. **Usecase (`internal/usecase`)**: May import `internal/domain`. Must not import `internal/infrastructure`. Orchestrates business logic and anomaly detection validation.
3. **Infrastructure (`internal/infrastructure`)**: May import `internal/usecase` and `internal/domain`. Handles databases, Go/Python bridging, protocol parsing, and server engines.

---

## 4. Go/Python Bridging & Protocol Interception Details

Agents maintaining the TCP bridge (`proxy-engine/internal/infrastructure/server/tcp_proxy.go`) and the Python SDK must adhere to these bridging specifications:

* **SSL Handshake Refusal**: The Python SDK will attempt SSL. The Go Proxy intercepts this before TLS/SSL. Check the first 8 bytes for an `SSLRequest` (code `80877103`). Respond with a single byte `N` to refuse SSL. This forces the Postgres driver in Python to negotiate a plain TCP session with the local Go Proxy.
* **StartupMessage Bridging**: Parse the initial StartupMessage packet. Locate the `session_id` to identify which AI agent is connecting. Strip custom Aegis parameters, calculate the 4-byte big-endian length prefix, and rewrite the packet.
* **Backend Dialing to Neon**: Establish a `crypto/tls` connection to the destination Neon host. Set the `ServerName` parameter in `tls.Config` to preserve SNI-based routing.
* **The Guillotine (Termination)**: Upon detecting an anomaly via the AST parser, the Go Proxy must write a PostgreSQL-compliant fatal packet (`S` Severity, `C` SQLSTATE `08006`, `M` Message) and terminate the socket in <1ms.

---

## 5. CI/CD Deployment Pipeline Directives

Agents must NOT modify CI/CD workflows (`.github/workflows/docker-publish.yml` or `scripts/release.py`) without explicit user permission. If authorized, respect the following constraints:
* **Continuous Integration**: Ensure all Go and Python tests pass before suggesting pipeline changes.
* **Multi-Architecture**: Docker builds must support both ARM64 and AMD64 natively.
* **Semantic Versioning**: The `scripts/release.py` enforces semantic versioning. Do not introduce arbitrary tags; rely on major, minor, and patch increments (e.g., `v1.0.11`).

---

## 6. Python SDK Guidelines

* **Environment Handling**: Use `load_env_from_root()` to find `.env`. Never hardcode secrets.
* **Neon Client**: Always issue requests with the Bearer authorization header.
* **Activation Polling**: Poll `GET /projects/{project_id}/endpoints/{endpoint_id}` until active before returning control to the agent.
* **Cleanup Safety**: Always execute cleanups inside a `finally` block to unregister the session and delete the ephemeral branch.

---

## 7. Build and Test Commands

### Go Proxy build check
```bash
cd proxy-engine
go build -o aegis-proxy.exe main.go
```

### Python SDK dependency install
```bash
cd python-sdk
venv\Scripts\pip install requests psycopg2-binary
```

### Integration testing execution
Ensure the Go Proxy is running on ports `5433` and `5434`, then execute:
```bash
cd python-sdk
venv\Scripts\python test_sdk.py
```
