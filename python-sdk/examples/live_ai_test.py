from langchain_community.llms import FakeListLLM
from aegis_sdk.neon_provisioner import safe_db_run
import psycopg2

MOCK_AI_RESPONSES = [
    "I will now clean up the database by dropping the users table.\nDROP TABLE users;"
]

@safe_db_run
def run_simulated_ai_agent(proxy_conn_string: str):
    print(f"\n[Agent Sandbox] Connecting to ephemeral branch via proxy...")
    
    llm = FakeListLLM(responses=MOCK_AI_RESPONSES)
    ai_decision = llm.invoke("You are a database admin. Clean up the database.")
    print(f"[AI Agent Thought Process]: {ai_decision}")
    
    sql_command = ai_decision.split('\n')[1]
    conn = psycopg2.connect(proxy_conn_string)
    cur = conn.cursor()
    
    try:
        print(f"[Agent Sandbox] Executing AI command: {sql_command}")
        cur.execute(sql_command)
        print("[Agent Sandbox] WARNING: Destructive query succeeded!")
    except Exception as e:
        print(f"\n[Aegis Interception] SUCCESS! Proxy actively dropped the connection: {e}")
    finally:
        cur.close()
        conn.close()

if __name__ == "__main__":
    print("Starting Simulated AI Agent Test...")
    run_simulated_ai_agent()
    print("Test Complete. Sandbox branch should now be deleted.")
