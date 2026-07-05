#!/bin/bash
# Stress test launcher script to bombard Aegis Proxy and check stability.
# Ensures the Go Proxy is active on localhost before executing the load simulation.

echo "Verifying Aegis Proxy Admin API is available on http://localhost:5434/metrics..."
if ! curl -s -f http://localhost:5434/metrics > /dev/null; then
  echo "Error: Aegis Proxy is not running or the metrics endpoint is unreachable on port 5434."
  echo "Please start the Go Proxy engine first using: ./aegis-proxy"
  exit 1
fi

echo "Running Python Concurrency Stress Test..."
python python-sdk/stress_test.py
