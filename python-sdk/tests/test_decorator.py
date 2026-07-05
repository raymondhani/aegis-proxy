import os
import psycopg2
from aegis_sdk.neon_provisioner import safe_db_run

@safe_db_run
def run_dummy_task(db_url: str):
    """
    Dummy AI Agent tool wrapped in safe_db_run.
    The db_url is automatically injected into this function by the decorator.
    """
    print(f"[Test Agent] Injected Connection URI: {db_url}")
    
    # 1. Execute a safe SELECT 1 query
    print("\n--- Step 1: Executing Safe Query ---")
    try:
        conn = psycopg2.connect(db_url)
        with conn.cursor() as cur:
            cur.execute("SELECT 1;")
            result = cur.fetchone()
            print(f"[Test Agent] Safe query succeeded. Result: {result}")
        conn.close()
    except Exception as e:
        print(f"[Test Agent] Error running safe query: {e}")

    # 2. Attempt a DROP TABLE IF EXISTS statement (should bypass proxy filtering)
    print("\n--- Step 2: Executing Safe Drop Table (with IF EXISTS) ---")
    try:
        conn = psycopg2.connect(db_url)
        with conn.cursor() as cur:
            # This is safe and allowed by the proxy
            cur.execute("DROP TABLE IF EXISTS critical_data;")
            conn.commit()
            print("[Test Agent] Safe DROP TABLE IF EXISTS passed proxy successfully.")
        conn.close()
    except Exception as e:
        print(f"[Test Agent] Error running safe drop: {e}")

    # 3. Attempt a destructive DROP TABLE statement (should be blocked by proxy AST)
    print("\n--- Step 3: Executing Destructive Drop Table (without IF EXISTS) ---")
    try:
        conn = psycopg2.connect(db_url)
        with conn.cursor() as cur:
            # Lacks IF EXISTS, should trigger AST query interception blocking
            cur.execute("DROP TABLE critical_data;")
            conn.commit()
            print("[Test Agent] WARNING: Destructive query succeeded! This should have been blocked.")
        conn.close()
    except Exception as e:
        print(f"AEGIS PROXY BLOCKED DESTRUCTIVE QUERY: {e}")

if __name__ == "__main__":
    print("Starting decorator integration test...")
    run_dummy_task()
    print("Decorator integration test finished.")
