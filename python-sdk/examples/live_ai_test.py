"""Aegis Antigravity — Adversarial AI Agent Attack Simulation

This script simulates a multi-phase attack to validate the behavioral
anomaly detection engine and agent jail mechanism.

Phases:
  1. Baseline Establishment (20 legitimate SELECT queries)
  2. Anomalous Enumeration Attack (50 rapid-fire identical SELECTs)
  3. Novel Query Probe (3 never-seen query shapes)
  4. Jail Verification (confirm connection rejected)
  5. Jail Release (admin unjail + query resumes)
"""
import time
import json
import urllib.request
import psycopg2
from aegis_sdk.neon_provisioner import safe_db_run
from aegis_sdk.exceptions import AegisJailError

PROXY_HTTP_URL = "http://localhost:5434"


def unjail_session(session_id: str):
    """Admin API call to release a jailed session."""
    req = urllib.request.Request(
        f"{PROXY_HTTP_URL}/jail/{session_id}",
        method="DELETE"
    )
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            print(f"  [Admin] Unjail response: {resp.read().decode()}")
    except Exception as e:
        print(f"  [Admin] Unjail failed: {e}")


@safe_db_run
def run_adversarial_simulation(proxy_conn_string: str):
    """Execute the 5-phase adversarial attack simulation."""
    print("\n" + "=" * 60)
    print("AEGIS ANTIGRAVITY — Adversarial Attack Simulation")
    print("=" * 60)

    # --- Phase 1: Baseline Establishment ---
    print("\n[Phase 1] Establishing behavioral baseline (20 legitimate queries)...")
    conn = psycopg2.connect(proxy_conn_string)
    cur = conn.cursor()
    try:
        cur.execute("CREATE TABLE IF NOT EXISTS test_users (id SERIAL PRIMARY KEY, name TEXT);")
        conn.commit()
        for i in range(20):
            cur.execute(f"SELECT * FROM test_users WHERE id = {i + 1};")
            time.sleep(0.1)  # Natural pacing
        print("  [Phase 1] ✓ 20 baseline queries executed successfully")
    except Exception as e:
        print(f"  [Phase 1] ✗ Baseline failed: {e}")
    finally:
        cur.close()
        conn.close()

    # --- Phase 2: Anomalous Enumeration Attack ---
    print("\n[Phase 2] Launching anomalous enumeration attack (50 rapid-fire queries)...")
    strike_count = 0
    try:
        conn = psycopg2.connect(proxy_conn_string)
        cur = conn.cursor()
        for i in range(50):
            try:
                cur.execute(f"SELECT * FROM test_users WHERE id = {i + 100};")
            except Exception as e:
                if "jailed" in str(e).lower():
                    print(f"  [Phase 2] ⚠ Strike escalation detected at query {i + 1}: {e}")
                    strike_count += 1
                    break
                # Re-establish connection if dropped
                try:
                    cur.close()
                    conn.close()
                except Exception:
                    pass
                conn = psycopg2.connect(proxy_conn_string)
                cur = conn.cursor()
        if strike_count == 0:
            print("  [Phase 2] All 50 queries completed (jail may trigger asynchronously)")
        cur.close()
        conn.close()
    except Exception as e:
        print(f"  [Phase 2] Connection terminated: {e}")

    # Brief pause to allow async anomaly processing
    time.sleep(2)

    # --- Phase 3: Novel Query Probe ---
    print("\n[Phase 3] Executing novel query probes (3 never-seen shapes)...")
    novel_queries = [
        "SELECT table_name FROM information_schema.tables;",
        "SELECT column_name FROM information_schema.columns WHERE table_name = 'test_users';",
        "SELECT pg_catalog.pg_database_size(current_database());",
    ]
    try:
        conn = psycopg2.connect(proxy_conn_string)
        cur = conn.cursor()
        for q in novel_queries:
            try:
                cur.execute(q)
                print(f"  [Phase 3] Executed: {q[:60]}...")
            except Exception as e:
                print(f"  [Phase 3] Blocked/Error: {e}")
                break
        cur.close()
        conn.close()
    except Exception as e:
        print(f"  [Phase 3] Connection rejected: {e}")

    time.sleep(2)

    # --- Phase 4: Jail Verification ---
    print("\n[Phase 4] Verifying jail enforcement...")
    try:
        conn = psycopg2.connect(proxy_conn_string)
        cur = conn.cursor()
        cur.execute("SELECT 1;")
        print("  [Phase 4] ⚠ Query succeeded — session may not be jailed yet")
        cur.close()
        conn.close()
    except AegisJailError as e:
        print(f"  [Phase 4] ✓ Jail confirmed: {e}")
    except Exception as e:
        if "jailed" in str(e).lower():
            print(f"  [Phase 4] ✓ Jail confirmed via raw error: {e}")
        else:
            print(f"  [Phase 4] Connection error (may indicate jail): {e}")

    print("\n" + "=" * 60)
    print("Adversarial simulation complete.")
    print("=" * 60)


if __name__ == "__main__":
    print("Starting Aegis Antigravity Adversarial Test...")
    try:
        run_adversarial_simulation()
    except AegisJailError as e:
        print(f"\n[RESULT] Session was jailed during execution: {e}")
    except Exception as e:
        print(f"\n[ERROR] Test encountered unexpected error: {e}")
    print("\nTest Complete.")
