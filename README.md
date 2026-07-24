<div align="center">
  <h1>🛡️ Aegis Security Proxy</h1>
  <p><b>Enterprise-grade, deterministic database security proxy for modern serverless architectures.</b></p>
  <p><i>Shift-Left Security. Zero Hallucinations. Sub-millisecond Response.</i></p>
  <br>
  <a href="https://pypi.org/project/aegis-proxy-sdk/"><img src="https://img.shields.io/pypi/v/aegis-proxy-sdk.svg" alt="PyPI version"></a>
  <a href="https://hub.docker.com/r/raymondartin2/aegis-proxy"><img src="https://img.shields.io/docker/pulls/raymondartin2/aegis-proxy.svg" alt="Docker Pulls"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL_v3-blue.svg" alt="License"></a>
</div>

---

Aegis is a robust, AI-native database security proxy purpose-built for modern serverless databases like [Neon](https://neon.tech). Positioned as a true **"Shift-Left"** security tool, Aegis empowers developers to embed security directly into their data layer before production deployments. By intercepting database connections at the proxy layer, Aegis allows you to safely sandbox AI agents, execute adversarial testing, and deterministically block malicious queries—all without slowing down your application.

## 🌟 The Aegis Product Suite

The Aegis ecosystem is divided into three distinct products. Here is a detailed breakdown of what each product does, with examples:

### 1. Aegis Proxy (Tier 1 — Open Source Core)
**What it does:** 
This is the core engine (Layer 4 TCP Proxy) built in user-space Go. It intercepts connections and uses native PostgreSQL AST parsing (`go-pgquery`) to deterministically block standard malicious queries (`DROP TABLE`, `DELETE`). It exposes generic Go interfaces (`Authenticator`, `PolicyValidator`) to allow for enterprise extensibility without compromising the open-source license.

### 2. Aegis SaaS Control Plane (Tier 2 — Managed SaaS)
**What it does:**
This is the hosted control plane for startups. It leverages WebSockets/SSE to provide real-time threat telemetry across an entire fleet of proxies. It also features a dynamic, ML-driven heuristic analyzer that detects runaway AI loops and automatically pushes adaptive rate-limiting (`AEGIS_RATE_LIMIT`) down to the OSS proxies via their HTTP APIs to protect cloud infrastructure costs.

### 3. Aegis Enterprise Guardrail (Tier 3 — Enterprise)
**What it does:**
This is a proprietary, private repository that compiles into a high-performance binary wrapping the OSS core. It injects strict B2B features directly into the OSS interfaces:
- **eBPF Network Acceleration:** Drops malicious packets directly in the Linux kernel (XDP) for absolute zero-latency execution.
- **Cryptographic Agent Identity:** Verifies JWTs/mTLS against cached JWKS during the `StartupMessage` to prevent API key sharing ("Shadow AI").
- **Custom YAML Policies:** Injects RBAC granular policies into the AST parser.
- **SIEM Exporters:** Batches and streams anomaly events directly to enterprise SIEM platforms (Splunk, Datadog).

---

## 🏗️ Multi-Agent Architecture

Aegis is designed to support a **Multi-Agent Workflow** where several autonomous AI agents can operate simultaneously without cross-contamination. 
- **Sandboxed Execution:** Every agent interaction is securely sandboxed in a dedicated Copy-on-Write (CoW) ephemeral branch provisioned via the Neon API.
- **Z-Score Anomaly Detection:** Agents' queries are mathematically analyzed in real-time using Native PostgreSQL AST parsing via `wasilibs/go-pgquery`.
- **Cryptographic Agent Identity:** Agents are identified using signed JWTs for secure, tamper-proof authentication.
- **Advanced Threat Telemetry:** Zero-latency monitoring is powered by OpenTelemetry.

---

## 🌉 Go/Python Bridging Mechanism

Aegis seamlessly bridges high-performance systems-level intercept logic with accessible data-science workflows.

1. **The Go Proxy Engine (Layer 4):**
   Written in Go utilizing Clean Architecture. It binds to local ports (e.g., `5433` for TCP, `5434` for HTTP metrics) and intercepts PostgreSQL wire-protocol traffic.
   
2. **The Python SDK (Application Layer):**
   Using the `@safe_db_run` decorator, the SDK automatically provisions an ephemeral branch, configures the agent's connection string to point to the local Go Proxy instead of directly to the DB, and manages the lifecycle of the test.

---

## 🚀 CI/CD Deployment Pipeline

Aegis utilizes a hardened CI/CD infrastructure for enterprise-grade distribution:
- **Continuous Integration:** Every commit to the `main` branch or pull request triggers a suite of unit tests.
- **Docker Publishing:** The pipeline builds multi-architecture Docker images (ARM64 & AMD64) and pushes them to Docker Hub. 

---

## 📦 Installation

### Python SDK (PyPI)
```bash
pip install aegis-proxy-sdk
```

### Docker Engine
```bash
docker pull raymondartin2/aegis-proxy
```

---

## 🛠️ Quick Start (Docker Compose)

The easiest way to run the entire Aegis suite (Proxy, SaaS Dashboard, and Enterprise Guardrail) is using Docker Compose.

### 1. Run the Stack
Navigate to the root directory where `docker-compose.yml` is located and run:
```bash
docker-compose up -d
```

### 2. Access the Services
- **SaaS Control Plane:** Open your browser to `http://localhost:3000`
- **Enterprise API:** Accessible at `http://localhost:5435`
- **TCP Proxy:** Connect your database clients to `localhost:5433`

---

## 🚀 Quickstart: Python SDK Integration

To connect to the local Docker proxy from your Python application, you must install the SDK and configure your environment.

### 1. Installation
Install the SDK and the PostgreSQL driver:
```bash
pip install aegis-proxy-sdk psycopg2-binary
```

### 2. Environment Configuration
Create a `.env` file in the root of your project containing your Neon credentials:
```env
NEON_API_KEY=your_api_key_here
NEON_PROJECT_ID=your_project_id_here
```

### 3. The `@safe_db_run` Decorator
The SDK relies on the `@safe_db_run` decorator. When applied to a function, it automatically provisions a secure, ephemeral Neon branch. It then dynamically rewrites your connection string's host and port to point to the local Go proxy (defaulting to `localhost:5433`, but configurable via `@safe_db_run(agent_id="test", proxy_port=9000)`).

### 4. Testing the AST Guillotine
Here is a complete Python script demonstrating how the proxy deterministically catches and severs connections executing malicious queries like `DROP TABLE`:

```python
import psycopg2
from aegis_proxy_sdk import safe_db_run

@safe_db_run(agent_id="test_agent", proxy_port=5433)
def execute_test(dsn: str):
    print("Connecting to local Aegis Proxy...")
    
    try:
        conn = psycopg2.connect(dsn)
        conn.autocommit = True
        cursor = conn.cursor()
        
        # 1. Execute a benign query
        print("Executing benign query: SELECT 1;")
        cursor.execute("SELECT 1;")
        print(f"Result: {cursor.fetchone()}")
        
        # 2. Execute a malicious query (will trigger the Guillotine)
        print("Executing malicious query: DROP TABLE users;")
        cursor.execute("DROP TABLE users;")
        
    except psycopg2.OperationalError as e:
        print(f"\n[!] Connection Severed by Proxy: {e}")
    finally:
        if 'conn' in locals() and not conn.closed:
            conn.close()

if __name__ == "__main__":
    execute_test()
```

---

## 🏢 Aegis Enterprise Edition
The open-source proxy is designed for local development and single-node sandboxing. For production-scale teams, **Aegis Enterprise (Tier 3)** is commercially backed by **Languaza Software** and offers:

- **eBPF Kernel Acceleration:** Drops malicious packets at the Linux XDP layer for absolute zero-latency execution.
- **Cryptographic Identity Validation:** Verifies JWTs against cached JWKS to prevent API key sharing.
- **Distributed State:** Redis-backed rate limiting for High Availability (HA) clusters.
- **SIEM Exporters:** Batches and streams anomaly events directly to Datadog and Splunk.

*Need production-grade guardrails for your AI agents? Contact our enterprise team at **aegis@languaza.net** to schedule a technical architecture review.*

---

## 📚 Further Reading
- **`AGENTS.md`**: Machine-readable specifications and boundaries for AI coding assistants.
