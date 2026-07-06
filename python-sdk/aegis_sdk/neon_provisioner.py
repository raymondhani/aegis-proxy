import os
import uuid
import time
import urllib.parse
from functools import wraps
import requests
import psycopg2

def load_env_from_root():
    """
    Recursively searches parent directories for a .env file and loads its variables into os.environ.
    This ensures environment variables are loaded regardless of execution context.
    """
    current_dir = os.path.dirname(os.path.abspath(__file__))
    while True:
        env_path = os.path.join(current_dir, ".env")
        if os.path.exists(env_path):
            with open(env_path, "r") as f:
                for line in f:
                    line = line.strip()
                    if not line or line.startswith("#"):
                        continue
                    if "=" in line:
                        key, val = line.split("=", 1)
                        os.environ.setdefault(key.strip(), val.strip())
            break
        parent_dir = os.path.dirname(current_dir)
        if parent_dir == current_dir:
            # Reached root of the filesystem without finding .env
            break
        current_dir = parent_dir

import threading

# Initialize env variables
load_env_from_root()

# Retrieve values
NEON_API_KEY = os.environ.get("NEON_API_KEY")
NEON_PROJECT_ID = os.environ.get("NEON_PROJECT_ID")
NEON_BASE_URL = os.environ.get("NEON_BASE_URL", "https://console.neon.tech/api/v2")
PROXY_TCP_HOST = os.environ.get("AEGIS_PROXY_TCP_HOST", "localhost")
PROXY_TCP_PORT = int(os.environ.get("AEGIS_PROXY_TCP_PORT", "5433"))
PROXY_HTTP_URL = os.environ.get("AEGIS_PROXY_HTTP_URL", "http://localhost:5434")

# Anomaly detector auto-start flag
_detector_started = False
_detector_lock = threading.Lock()

class NeonProvisioner:
    """Manages ephemeral database branch provisioning and registration with Aegis Go Proxy."""

    def __init__(self):
        if not NEON_API_KEY or not NEON_PROJECT_ID:
            raise ValueError(
                "Missing required environment configuration: NEON_API_KEY and NEON_PROJECT_ID are mandatory."
            )
        self.headers = {
            "Authorization": f"Bearer {NEON_API_KEY}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        }

    def provision_branch(self) -> tuple:
        """
        Creates an isolated database branch and waits for its compute endpoint to become active.
        Returns:
            (branch_id, endpoint_host)
        """
        branch_name = f"aegis-branch-{uuid.uuid4().hex[:10]}"
        print(f"[Neon SDK] Creating database branch: {branch_name}...")

        # 1. POST /projects/{project_id}/branches
        url = f"{NEON_BASE_URL}/projects/{NEON_PROJECT_ID}/branches"
        payload = {
            "branch": {
                "name": branch_name
            },
            "endpoints": [
                {
                    "type": "read_write"
                }
            ]
        }
        res = requests.post(url, json=payload, headers=self.headers)
        res.raise_for_status()
        data = res.json()

        branch_id = data["branch"]["id"]
        endpoint = data["endpoints"][0]
        endpoint_id = endpoint["id"]
        endpoint_host = endpoint["host"]

        print(f"[Neon SDK] Ephemeral branch {branch_id} created. Waiting for compute {endpoint_id} to activate...")

        # 2. Poll the endpoint status until it is 'active' (max 30 seconds)
        ep_url = f"{NEON_BASE_URL}/projects/{NEON_PROJECT_ID}/endpoints/{endpoint_id}"
        start_time = time.time()
        while time.time() - start_time < 30:
            ep_res = requests.get(ep_url, headers=self.headers)
            ep_res.raise_for_status()
            ep_data = ep_res.json()
            state = ep_data.get("endpoint", {}).get("current_state")
            if state == "active":
                print(f"[Neon SDK] Compute endpoint is active: {endpoint_host}")
                break
            time.sleep(1)
        else:
            print("[Neon SDK] Warning: Compute endpoint activation timeout. Proceeding anyway.")

        return branch_id, endpoint_host

    def get_connection_uri(self, branch_id: str) -> str:
        """
        Retrieves the connection URI containing credentials for the database and role in the branch.
        """
        # Request the full connection string
        conn_url = f"{NEON_BASE_URL}/projects/{NEON_PROJECT_ID}/connection_uri"
        params = {
            "branch_id": branch_id,
            "database_name": os.getenv("NEON_DEFAULT_DB", "neondb"),
            "role_name": os.getenv("NEON_DEFAULT_ROLE", "neondb_owner")
        }
        conn_res = requests.get(conn_url, headers=self.headers, params=params)
        conn_res.raise_for_status()
        
        data = conn_res.json()
        if "uri" not in data:
            raise RuntimeError(f"Failed to retrieve connection URI from Neon API. Response: {data}")
            
        return data["uri"]

    def register_session(self, session_id: str, target_host: str):
        """Registers the session routing mapping with the Go TCP Proxy."""
        url = f"{PROXY_HTTP_URL}/register"
        payload = {
            "session_id": session_id,
            "target_host": target_host
        }
        res = requests.post(url, json=payload, timeout=5)
        res.raise_for_status()

    def unregister_session(self, session_id: str):
        """Unregisters the session routing mapping from the Go TCP Proxy."""
        url = f"{PROXY_HTTP_URL}/unregister"
        payload = {
            "session_id": session_id
        }
        try:
            res = requests.post(url, json=payload, timeout=5)
            res.raise_for_status()
        except Exception as e:
            print(f"[Neon SDK] Warning: Failed to unregister session {session_id} from Go Proxy: {e}")

    def delete_branch(self, branch_id: str):
        """Deletes the branch from Neon."""
        url = f"{NEON_BASE_URL}/projects/{NEON_PROJECT_ID}/branches/{branch_id}"
        print(f"[Neon SDK] Deleting ephemeral branch {branch_id}...")
        try:
            res = requests.delete(url, headers=self.headers, timeout=10)
            res.raise_for_status()
            print("[Neon SDK] Ephemeral branch deleted successfully.")
        except Exception as e:
            print(f"[Neon SDK] Error deleting branch {branch_id}: {e}")

# ==========================================
# Schema Snapshot and Validation Rules
# ==========================================

def get_schema_snapshot(db_url: str) -> dict:
    """Returns a dictionary mapping table names to their set of column names in the public schema."""
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            cur.execute("""
                SELECT table_name, column_name 
                FROM information_schema.columns 
                WHERE table_schema = 'public';
            """)
            rows = cur.fetchall()
            snapshot = {}
            for table, col in rows:
                snapshot.setdefault(table, set()).add(col)
            return snapshot
    finally:
        conn.close()

def validate_no_dropped_tables(db_url: str, before: dict, after: dict):
    """Verifies that no tables in the public schema were dropped."""
    for table in before:
        if table not in after:
            raise ValueError(f"Validation Violation: Protected table '{table}' was dropped.")

def validate_no_dropped_columns(db_url: str, before: dict, after: dict):
    """Verifies that no existing columns in the public schema were dropped."""
    for table, cols in before.items():
        if table in after:
            dropped = cols - after[table]
            if dropped:
                raise ValueError(f"Validation Violation: Protected columns {dropped} in table '{table}' were dropped.")

def validate_has_primary_keys(db_url: str, before: dict, after: dict):
    """Verifies that all newly created tables have primary keys."""
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            cur.execute("""
                SELECT table_name 
                FROM information_schema.tables 
                WHERE table_schema = 'public'
                  AND table_name NOT IN (
                      SELECT kcu.table_name
                      FROM information_schema.table_constraints tc
                      JOIN information_schema.key_column_usage kcu
                        ON tc.constraint_name = kcu.constraint_name
                       AND tc.table_schema = kcu.table_schema
                      WHERE tc.constraint_type = 'PRIMARY KEY'
                  );
            """)
            rows = cur.fetchall()
            if rows:
                invalid_tables = [r[0] for r in rows]
                raise ValueError(
                    f"Validation Violation: Newly created tables lack primary key constraints: {invalid_tables}"
                )
    finally:
        conn.close()

# ==========================================
# Decorator
# ==========================================

def safe_db_run(func=None, *, validation_rules=None):
    """
    Decorator that provisions a Neon database branch, routes execution through the Aegis Go Proxy,
    takes schema snapshots, runs validation rules, and guarantees resource cleanup.
    """
    if validation_rules is None:
        validation_rules = [
            validate_no_dropped_tables,
            validate_no_dropped_columns,
            validate_has_primary_keys,
        ]

    def decorator(f):
        @wraps(f)
        def wrapper(*args, **kwargs):
            # Auto-start anomaly detector daemon (double-checked locking)
            global _detector_started
            if not _detector_started:
                with _detector_lock:
                    if not _detector_started:
                        try:
                            from aegis_sdk.anomaly_detector import start_detector
                            start_detector()
                            _detector_started = True
                        except Exception as e:
                            print(f"[Aegis SDK] Warning: Failed to start anomaly detector: {e}")

            prov = NeonProvisioner()
            branch_id = None
            session_id = str(uuid.uuid4())
            original_db_url = os.environ.get("DATABASE_URL")

            try:
                # 1. Provision branch
                branch_id, endpoint_host = prov.provision_branch()

                # 2. Get connection URI from Neon
                real_conn_uri = prov.get_connection_uri(branch_id)

                # 3. Register the host mapping in Go Proxy
                prov.register_session(session_id, endpoint_host)

                # 4. Build Aegis Proxy connection URL
                # We inject the session_id as a parameter suffix inside the database name path
                # e.g., postgresql://role:password@localhost:5433/neondb?session_id=UUID&sslmode=disable
                parsed = urllib.parse.urlparse(real_conn_uri)
                netloc = f"{PROXY_TCP_HOST}:{PROXY_TCP_PORT}"
                if parsed.password:
                    netloc = f"{parsed.username}:{parsed.password}@{netloc}"
                elif parsed.username:
                    netloc = f"{parsed.username}@{netloc}"

                # Append session_id query parameters inside the db name (unquoted path)
                db_name = parsed.path.lstrip("/")
                new_path = f"/{db_name}%3Fsession_id%3D{session_id}"

                proxy_db_url = urllib.parse.urlunparse((
                    parsed.scheme,
                    netloc,
                    new_path,
                    "",
                    "sslmode=disable", # Disable client SSL since Go proxy rejects SSLRequest
                    ""
                ))

                print(f"[Neon SDK] Registered Session {session_id} with Aegis Proxy.")
                print(f"[Neon SDK] Injecting proxy connection URL: postgresql://***:***@{PROXY_TCP_HOST}:{PROXY_TCP_PORT}/{db_name}?session_id={session_id}")

                # 5. Take schema snapshot before execution
                # Connect directly to the branch to capture snapshot (avoiding proxy overhead)
                before_snapshot = get_schema_snapshot(real_conn_uri)

                # 6. Inject into environment
                os.environ["DATABASE_URL"] = proxy_db_url

                # 7. Inject connection string into function call context if expected
                import inspect
                sig = inspect.signature(f)
                if "db_url" in sig.parameters:
                    kwargs["db_url"] = proxy_db_url
                elif "db_uri" in sig.parameters:
                    kwargs["db_uri"] = proxy_db_url
                elif "proxy_conn_string" in sig.parameters:
                    kwargs["proxy_conn_string"] = proxy_db_url

                # 8. Run execution
                try:
                    result = f(*args, **kwargs)
                except Exception as exec_err:
                    # Check if this is a jail error from the proxy
                    import psycopg2
                    if isinstance(exec_err, psycopg2.OperationalError) and "jailed" in str(exec_err).lower():
                        from aegis_sdk.exceptions import AegisJailError
                        raise AegisJailError(session_id, str(exec_err)) from exec_err
                    raise

                # 9. Take schema snapshot after execution
                after_snapshot = get_schema_snapshot(real_conn_uri)

                # 10. Run validation rules
                print("[Neon SDK] Running schema merge validation rules...")
                for rule in validation_rules:
                    rule(real_conn_uri, before_snapshot, after_snapshot)
                print("[Neon SDK] Validation passed successfully.")

                return result

            finally:
                # 11. Guaranteed Cleanup
                # Restore original env
                if original_db_url is not None:
                    os.environ["DATABASE_URL"] = original_db_url
                else:
                    os.environ.pop("DATABASE_URL", None)

                # Delete branch and unregister
                prov.unregister_session(session_id)
                if branch_id:
                    prov.delete_branch(branch_id)

        return wrapper

    if func is not None:
        return decorator(func)
    return decorator
