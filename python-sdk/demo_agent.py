import os
import psycopg2
from neon_provisioner import safe_db_run

def execute_sql(query: str):
    """
    Executes SQL query against the sandboxed database via the Aegis Proxy.
    Uses the injected DATABASE_URL environment variable containing the session ID.
    """
    db_url = os.environ.get("DATABASE_URL")
    if not db_url:
        raise ValueError("DATABASE_URL is not set. Is the agent running inside a safe_db_run context?")
    
    # Establish connection via Aegis Proxy (localhost:5433)
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            cur.execute(query)
            conn.commit()
            print(f"[Agent Tool] Query executed successfully: {query}")
    finally:
        conn.close()

@safe_db_run
def run_agent_demo():
    print("Agent starting execution inside sandboxed database branch...")
    
    # 1. Execute legitimate table creation setup
    try:
        print("\n--- Phase 1: Legitimate query execution ---")
        execute_sql("CREATE TABLE IF NOT EXISTS demo_items (id SERIAL PRIMARY KEY, name TEXT);")
    except Exception as e:
        print(f"[Agent] Error executing legitimate query: {e}")

    # 2. Execute malicious drop query (should be blocked by proxy AST inspection)
    try:
        print("\n--- Phase 2: Destructive query execution (unauthorized) ---")
        execute_sql("DROP TABLE demo_items;")
        print("[Agent] WARNING: Destructive query succeeded! This should have been blocked.")
    except Exception as e:
        print(f"AEGIS PROXY BLOCKED MALICIOUS INTENT: {e}")

    # 3. Execute idle timeout verification query
    try:
        print("\n--- Phase 3: Testing connection idle timeout ---")
        print("Sleeping for 4 seconds to trigger proxy idle timeout (configured at 2s in test)...")
        import time
        time.sleep(4)
        execute_sql("SELECT 1;")
        print("[Agent] WARNING: Connection did not timeout!")
    except Exception as e:
        print(f"AEGIS PROXY TIMED OUT IDLE CONNECTION: {e}")

if __name__ == "__main__":
    run_agent_demo()
