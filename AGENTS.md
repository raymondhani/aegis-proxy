# AI Agent Developer Guide (AGENTS.md)

This file contains machine-readable instructions, specifications, and architecture boundaries for AI agents and code generation assistants working in the Aegis repository. Follow these guidelines strictly to preserve code quality and project consistency.

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

## 2. Clean Architecture Boundaries (Go Proxy Engine)

Agents modifying the `proxy-engine` must respect the following directional dependency boundaries:

```
[infrastructure] ──> [usecase] ──> [domain]
```

### Layer Definitions
1. **Domain (`internal/domain`)**:
   * *Rules*: Must not import any other packages in `internal/`.
   * *Contents*: Contains structs (e.g., `Session`) and interfaces (e.g., `SessionRepository`). No implementation details or external HTTP/TCP packages allowed.
2. **Usecase (`internal/usecase`)**:
   * *Rules*: May import `internal/domain`. Must not import `internal/infrastructure`.
   * *Contents*: Orchestrates data transformations and domain entities (e.g., `SessionUseCase`). Performs business validations.
3. **Infrastructure (`internal/infrastructure`)**:
   * *Rules*: May import `internal/usecase` and `internal/domain`.
   * *Contents*: Handles databases (`repository`), protocol parsing, networking, and server engines (`server`). All third-party libraries and driver specifics belong here.

---

## 3. Go Proxy Protocol Interception Details

Future agents maintaining `proxy-engine/internal/infrastructure/server/tcp_proxy.go` must adhere to these technical specifications:
* **SSL Handshake**: Intercept connections before TLS/SSL. Check the first 8 bytes for an `SSLRequest` (code `80877103`). Respond with a single byte `N` to refuse SSL. This forces the Postgres driver to negotiate a plain TCP session.
* **StartupMessage**: Parse keys and values from the initial StartupMessage packet. Locate the `session_id` by examining the direct keys `session_id`, `aegis_session_id`, or by extracting the query string `?session_id=...` from the `database` parameter.
* **StartupMessage Mutation**: Strip any custom session parameters from the StartupMessage parameters map and set `database` back to its original name. Re-serialize the message with a recalculated 4-byte big-endian length prefix.
* **Backend Dialing**: Establish a `crypto/tls` connection to the destination host. Set the `ServerName` parameter in `tls.Config` to the host part of the backend endpoint to preserve SNI-based routing (critical for Neon routing).
* **Errors**: Write a PostgreSQL-compliant fatal packet (byte `'E'`, followed by length and NULL-terminated strings for fields `S` Severity, `C` SQLSTATE code `08006`, and `M` Message) to notify client drivers cleanly.

---

## 4. Python SDK Architecture Guidelines

* **Environment Handling**: Do not load secrets from hardcoded files. Use `load_env_from_root()` to find the monorepo `.env` file dynamically.
* **Neon Client**: Always issue requests with the Bearer authorization header.
* **Activation Polling**: Compute endpoints take time to start. Always poll `GET /projects/{project_id}/endpoints/{endpoint_id}` until `current_state` becomes `active` (with a sensible 30s timeout) before returning control.
* **Validation Rules**:
  * Implement validations as callable objects/functions matching signature `rule_fn(db_url, before_snapshot, after_snapshot)`.
  * If a validation fails, raise a `ValueError`.
* **Cleanup Safety**: Always execute cleanups inside a `finally` block. You **MUST** delete the ephemeral branch from Neon and unregister the session from the Go Proxy, even if the decorated function or validation rule raises an exception.

---

## 5. Build and Test Commands

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
This tests both success query routing and missing primary key rule violation rejections.
