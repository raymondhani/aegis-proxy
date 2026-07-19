import requests
import time
import threading

def listen_telemetry():
    url = "http://localhost:5434/telemetry/stream"
    print(f"Connecting to {url}...")
    try:
        res = requests.get(url, headers={"Accept": "text/event-stream"}, stream=True)
        print(f"Response status: {res.status_code}")
        for line in res.iter_lines(decode_unicode=True):
            if line:
                print(f"[STREAM] {line}")
    except Exception as e:
        print(f"Error: {e}")

t = threading.Thread(target=listen_telemetry, daemon=True)
t.start()

time.sleep(2)
print("Listening... Press Ctrl+C to stop.")
while True:
    time.sleep(1)
