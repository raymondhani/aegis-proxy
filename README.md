<div align="center">
  <h1>🛡️ Aegis Security Proxy</h1>
  <br>
  <a href="https://pypi.org/project/aegis-proxy-sdk/"><img src="https://img.shields.io/pypi/v/aegis-proxy-sdk.svg" alt="PyPI version"></a>
  <a href="https://hub.docker.com/r/raymondartin2/aegis-proxy"><img src="https://img.shields.io/docker/pulls/raymondartin2/aegis-proxy.svg" alt="Docker Pulls"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL_v3-blue.svg" alt="License"></a>
</div>

---

Aegis sits between your application and your Postgres database and blocks destructive queries before they run, without you changing a line of your database code.

## How it fits together

```
      Your Application                          Aegis Proxy                            Your Database
   (psycopg2, or any client)                                                          (e.g. Neon Postgres)

   +--------------------+   Postgres wire    +-----------------------------+   TLS    +--------------------+
   |                    | ------------------> |  :5433  TCP intercept       | -------> |                    |
   |  SELECT ...        |                     |    - parses every query     |          |   real database    |
   |  DROP TABLE ...    | <------------------ |    - enforces rate limits   | <------- |                    |
   |                    |   rows, or a        |    - blocks destructive     |          |                    |
   +--------------------+   fatal error       |      statements (the        |          +--------------------+
                            if blocked        |      "Guillotine")          |
                                              |  :5434  admin + metrics API |
                                              +-----------------------------+
```

Your application never talks to the database directly — every query passes through the proxy first. A benign query is forwarded and the rows come straight back. A destructive one (`DROP TABLE`, an unfiltered `DELETE`, etc.) is intercepted and the connection is terminated before it reaches your database.

## Quickstart

Three steps, one command to start the stack, and a running proxy.

### Step 1 — Get the code and your credentials

```bash
git clone https://github.com/raymondhani/aegis-proxy.git
cd aegis-proxy
cp .env.sample .env
```

Now open `.env` and replace the four placeholders with your own values:

| Placeholder in `.env.sample` | What to put there |
| :--- | :--- |
| `NEON_API_KEY=your_api_key_here` | An API key from your [Neon](https://neon.tech) account |
| `NEON_PROJECT_ID=your_project_id_here` | The Neon project Aegis should provision ephemeral branches in |
| `AEGIS_ADMIN_TOKEN=your_custom_admin_token_12345` | Any string you choose — Aegis's own admin credential, not from Neon |
| `AEGIS_JWT_SECRET=your_super_secret_jwt_key_that_is_at_least_32_bytes_long` | Any string **at least 32 characters long** — used to sign session tokens |

None of these are optional: the proxy exits immediately at startup if `AEGIS_JWT_SECRET` is missing, and every step below assumes all four are set.

### Step 2 — Start the stack

```bash
docker compose up -d
```

**Expected output** (container names may vary slightly depending on your folder name):

```text
 Container aegis-project-redis-1  Started
 Container aegis-proxy            Started
```

This single command starts both containers Aegis needs: Redis (used for policy state) and the proxy itself, listening on `5433` (the database connection) and `5434` (an admin/metrics API).

### Step 3 — Confirm it's running

```bash
curl -s http://localhost:5434/metrics
```

**Expected output:**

```json
{"queries_processed":0,"queries_blocked":0,"active_connections":0,"sessions_jailed":0,"anomalies_detected":0}
```

If you see that, the proxy is up and ready to accept connections.

## Try it: block a destructive query

Install the SDK and the Postgres driver, then run this against the stack you just started — nothing here needs modification beyond the credentials you already set in `.env`.

```bash
pip install aegis-proxy-sdk psycopg2-binary
```

```python
import psycopg2
from aegis_sdk import safe_db_run

@safe_db_run()
def run_demo(db_url: str):
    conn = psycopg2.connect(db_url)
    conn.autocommit = True
    cursor = conn.cursor()

    print("Running an ordinary query...")
    cursor.execute("SELECT 1;")
    print(f"Result: {cursor.fetchone()}")

    print("Running a destructive query: DROP TABLE users;")
    cursor.execute("DROP TABLE users;")

if __name__ == "__main__":
    try:
        run_demo()
    except psycopg2.OperationalError as e:
        print(f"\n[Aegis] Connection severed by the proxy: {e}")
```

The `@safe_db_run()` decorator does the setup for you: it provisions a throwaway Neon database branch, mints a session token, and points `db_url` at the local proxy instead of the real database — then deletes the branch again when the function returns. Your function just receives a working `db_url` and uses it like any other Postgres connection string.

**Expected output:**

```text
Running an ordinary query...
Result: (1,)
Running a destructive query: DROP TABLE users;

[Aegis] Connection severed by the proxy: server closed the connection unexpectedly
	This probably means the server terminated abnormally
	before or while processing the request.
```

The `SELECT 1` succeeds normally. The `DROP TABLE` never reaches the database — the proxy detects it, terminates the connection, and `psycopg2` surfaces that as `OperationalError`. Catching that exception around the specific call you expect to be blocked is the intended pattern, not a workaround.

`@safe_db_run` also accepts a few optional keyword arguments for less common setups:

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `agent_id` | `str` | `None` | A label for this run, included in telemetry — useful when multiple agents share one proxy. |
| `proxy_port` | `int` | `5433` | The TCP port the proxy is listening on, if you changed it from the default. |
| `validation_rules` | `list` | built-in schema checks | Custom functions to run against the database schema before/after execution, replacing the defaults (no dropped tables, no dropped columns, every table keeps a primary key). |

## What each tier gives you

Aegis ships as three products built on the same core. Every capability below is enforced by the proxy itself, not just documented — the automated benchmark suite in this workspace asserts each one directly against a running instance of every tier.

| Capability | OSS Proxy<br>(self-hosted) | Hosted SaaS<br>(managed) | Enterprise Guardrail<br>(self-hosted) |
| :--- | :---: | :---: | :---: |
| **Static rate limiting** — stops a single client from firing more queries per minute than the account allows | ✅ | ✅ | ✅ |
| **Wire transparency** — works with your existing Postgres client, unmodified, by speaking the same wire protocol | ✅ | ✅ | ✅ |
| **The Guillotine** — instantly kills any connection that tries to run a destructive statement, before it reaches your database | ✅ | ✅ | ✅ |
| **ML adaptive throttling** — learns each agent's normal query pattern and slows down abnormal spikes automatically, instead of one fixed limit for everyone | — | ✅ *(paid plans)* | ✅ |
| **Central fleet telemetry** — live query activity across your whole fleet of proxies in one feed, not one proxy at a time | — | ✅ *(paid plans)* | ✅ |
| **eBPF kernel-bypass** — drops malicious traffic inside the Linux kernel itself, before user-space code ever sees it, for the lowest possible latency | — | — | ✅ |
| **Custom AST policies** — write your own rules for which query patterns are allowed, beyond the built-in defaults | — | — | ✅ |

## Troubleshooting

| Symptom | Cause | Fix |
| :--- | :--- | :--- |
| **Wrong port** — your app hangs or times out connecting on `5432` | Aegis is not a Postgres server on the standard port. It listens on `5433` specifically, so it can run alongside a real Postgres instance on the same host without colliding. | Point your connection string at `5433` — the `docker-compose.yml` in this repo already maps it. |
| **Missing credentials** — the container exits right after starting (`docker compose ps` shows `Exited`), or your script raises `ValueError: AEGIS_JWT_SECRET environment variable is missing.` | `.env` was never created, or one of the four values is still the placeholder from `.env.sample`. | `cp .env.sample .env`, fill in real values (`AEGIS_JWT_SECRET` needs to be 32+ characters), then `docker compose up -d` again. |
| **Container not started** — `docker ps` shows nothing for `aegis-proxy` | `docker compose up -d` was never run in this directory, or it failed earlier without you noticing. | Run `docker compose up -d` from the repo root and read its output — each container should say `Started`. |
| **Connection refused** — `docker compose ps` shows `aegis-proxy` as `Up`, but your client still can't connect | Almost always a host mismatch. A client running inside its own Docker container can't reach the proxy via `localhost` — it needs the Docker-internal address. | From another container, connect to `host.docker.internal:5433` (Mac/Windows) or join the `aegis-net` network and use the service name `aegis-proxy`. Running your script directly on the host, as in the quickstart above, needs no special host. |
| **Unexpectedly severed query** — `psycopg2.OperationalError: server closed the connection unexpectedly` right after running a query | This is almost always the Guillotine working as intended — the proxy detected a destructive statement and terminated the connection to block it. It is not a network problem. | Catch `psycopg2.OperationalError` around the specific call, exactly as the example above does. If a query you didn't expect to be destructive gets blocked, that's a policy question, not a connectivity bug. |

---

## 🏗️ Multi-Agent Architecture

Aegis is designed to support a **Multi-Agent Workflow** where several autonomous AI agents can operate simultaneously without cross-contamination.
- **Sandboxed Execution:** Every agent interaction is securely sandboxed in a dedicated Copy-on-Write (CoW) ephemeral branch provisioned via the Neon API.
- **Z-Score Anomaly Detection:** Agents' queries are mathematically analyzed in real-time using native PostgreSQL AST parsing via `wasilibs/go-pgquery`.
- **Cryptographic Agent Identity:** Agents are identified using signed JWTs for secure, tamper-proof authentication.
- **Advanced Threat Telemetry:** Zero-latency monitoring is powered by OpenTelemetry.

## 🌉 Go/Python Bridging Mechanism

Aegis seamlessly bridges high-performance systems-level intercept logic with accessible data-science workflows.

1. **The Go Proxy Engine (Layer 4):**
   Written in Go using Clean Architecture. It binds to local ports (`5433` for TCP, `5434` for the HTTP admin/metrics API) and intercepts PostgreSQL wire-protocol traffic.

2. **The Python SDK (Application Layer):**
   Using the `@safe_db_run` decorator, the SDK automatically provisions an ephemeral branch, configures the agent's connection string to point to the local Go proxy instead of directly to the database, and manages the lifecycle of the run.

## 🚀 CI/CD Deployment Pipeline

Aegis uses a hardened CI/CD pipeline for distribution:
- **Continuous Integration:** Every commit to `main` and every pull request triggers a suite of unit tests.
- **Docker Publishing:** The pipeline builds multi-architecture Docker images (ARM64 & AMD64) and pushes them to Docker Hub.

## 🧠 Advanced SDK Usage

While `@safe_db_run` provides a seamless, zero-friction wrapper, `aegis_sdk` also exposes its underlying classes for developers who need custom integrations or manual state management:

- **`NeonProvisioner`**: Programmatically create, monitor, and tear down Neon ephemeral branches outside of the standard decorator flow.
- **`AnomalyDetector`**: Interface directly with the Z-Score engine to evaluate query payloads or agent behaviors before they hit the execution pipeline.

## 🏢 Aegis Enterprise Edition

The open-source proxy is built for local development and single-node sandboxing. For production-scale teams, **Aegis Enterprise** is commercially backed by **Languaza Software** and adds, on top of everything above:

- **eBPF Kernel Acceleration:** Drops malicious packets at the Linux XDP layer for the lowest possible latency.
- **Cryptographic Identity Validation:** Verifies JWTs against cached JWKS to prevent API key sharing.
- **Distributed State:** Redis-backed rate limiting for High Availability (HA) clusters.
- **SIEM Exporters:** Batches and streams anomaly events directly to Datadog and Splunk.
- **SaaS Control Plane:** The managed control plane is available at port `3000`.

*Need production-grade guardrails for your AI agents? Contact our enterprise team at **aegis@languaza.net** to schedule a technical architecture review.*

---

## 📚 Further Reading
- **`AGENTS.md`**: Machine-readable specifications and boundaries for AI coding assistants.
