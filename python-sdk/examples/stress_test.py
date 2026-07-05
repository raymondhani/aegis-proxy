import concurrent.futures
import urllib.request
import json
import time
import sys
# Added for packaging consistency
from aegis_sdk.neon_provisioner import safe_db_run

def fetch_metrics():
    try:
        # Request metrics endpoint
        with urllib.request.urlopen("http://localhost:5434/metrics", timeout=3) as response:
            data = response.read()
            return json.loads(data.decode('utf-8'))
    except Exception as e:
        return str(e)

def run_stress_test():
    print("Starting concurrency stress test against http://localhost:5434/metrics...")
    start_time = time.time()
    
    success_count = 0
    fail_count = 0
    
    # Send 500 concurrent requests across 50 worker threads
    with concurrent.futures.ThreadPoolExecutor(max_workers=50) as executor:
        futures = [executor.submit(fetch_metrics) for _ in range(500)]
        for future in concurrent.futures.as_completed(futures):
            res = future.result()
            if isinstance(res, dict) and "queries_processed" in res:
                success_count += 1
            else:
                fail_count += 1
                print(f"[ERROR] Request failed: {res}", file=sys.stderr)
                
    elapsed_duration = time.time() - start_time
    print("\n==============================================")
    print(f"Concurreny Stress Test Completed in {elapsed_duration:.3f}s")
    print(f"Total Requests: 500")
    print(f"Successful Requests: {success_count}")
    print(f"Failed Requests: {fail_count}")
    print("==============================================")
    
    if fail_count > 0:
        print("RESULT: FAIL - Concurrency stress test encountered errors.")
        sys.exit(1)
    else:
        print("RESULT: SUCCESS - Aegis Proxy Admin API remained stable and responsive.")
        sys.exit(0)

if __name__ == "__main__":
    run_stress_test()
