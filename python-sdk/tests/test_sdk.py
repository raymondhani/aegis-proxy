import sys
import psycopg2
from aegis_sdk.neon_provisioner import safe_db_run

@safe_db_run
def test_successful_run(db_url: str):
    """
    Executes valid operations (table creation with primary key, insert, and select)
    to confirm the Go Proxy routes the connection properly and validation passes.
    """
    print(f"\n[Test] Running test_successful_run against proxy URL: {db_url}")
    
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            # 1. Clean up potential leftover from manual tests
            cur.execute("DROP TABLE IF EXISTS test_aegis_users;")
            
            # 2. Create table with primary key
            cur.execute("""
                CREATE TABLE test_aegis_users (
                    id SERIAL PRIMARY KEY,
                    name VARCHAR(100) NOT NULL
                );
            """)
            print("[Test] Created table 'test_aegis_users' (with Primary Key)")
            
            # 3. Insert and Select
            cur.execute("INSERT INTO test_aegis_users (name) VALUES (%s) RETURNING id;", ("Aegis Agent",))
            row_id = cur.fetchone()[0]
            print(f"[Test] Inserted record with ID: {row_id}")
            
            cur.execute("SELECT name FROM test_aegis_users WHERE id = %s;", (row_id,))
            name = cur.fetchone()[0]
            print(f"[Test] Retrieved record name: {name}")
            
            assert name == "Aegis Agent", f"Expected 'Aegis Agent' but got '{name}'"
            conn.commit()
    finally:
        conn.close()

@safe_db_run
def test_missing_pk_run(db_url: str):
    """
    Attempts to create a table without a primary key.
    This should be caught by the validate_has_primary_keys validation rule.
    """
    print(f"\n[Test] Running test_missing_pk_run against proxy URL: {db_url}")
    
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            cur.execute("DROP TABLE IF EXISTS test_aegis_nopk;")
            # Create a table without primary key
            cur.execute("""
                CREATE TABLE test_aegis_nopk (
                    name VARCHAR(100)
                );
            """)
            print("[Test] Created table 'test_aegis_nopk' (without Primary Key)")
            conn.commit()
    finally:
        conn.close()

if __name__ == "__main__":
    print("====================================================")
    print("Aegis DB Proxy SDK Integration Test Suite Starting...")
    print("====================================================")
    
    # 1. Run successful path
    try:
        test_successful_run()
        print("\n=> SUCCESS: test_successful_run passed as expected.")
    except Exception as e:
        print(f"\n=> FAIL: test_successful_run failed unexpectedly: {e}")
        sys.exit(1)

    # 2. Run schema violation path
    try:
        test_missing_pk_run()
        print("\n=> FAIL: test_missing_pk_run did not trigger validation error!")
        sys.exit(1)
    except ValueError as ve:
        print(f"\n=> SUCCESS: test_missing_pk_run was blocked by validation as expected: {ve}")
    except Exception as e:
        print(f"\n=> FAIL: test_missing_pk_run threw wrong exception: {e}")
        sys.exit(1)

    print("\n====================================================")
    print("All integration tests executed successfully!")
    print("====================================================")
