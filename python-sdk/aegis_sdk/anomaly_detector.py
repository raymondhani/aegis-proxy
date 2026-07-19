"""Aegis Behavioral Anomaly Detection Engine.

Runs as a daemon thread consuming telemetry from the Go proxy via SSE.
Uses a Count-Min Sketch with sliding window for frequency estimation
and a strike tracker for escalation to session jailing.
"""

import re
import os
import sys
import json
import math
import time
import threading
import urllib.request
from collections import deque

# ==========================================
# SQL Fingerprinting (stdlib only)
# ==========================================

_QUOTED_STR = re.compile(r"'[^']*'")
_NUMERIC = re.compile(r"\b\d+(?:\.\d+)?\b")
_WHITESPACE = re.compile(r"\s+")


def fingerprint_sql(query: str) -> str:
    """Normalize a SQL query into a structural fingerprint.

    Replaces string literals, numeric literals with '?',
    collapses whitespace, lowercases, and strips trailing semicolons.
    """
    q = _QUOTED_STR.sub("?", query)
    q = _NUMERIC.sub("?", q)
    q = _WHITESPACE.sub(" ", q).strip().lower().rstrip(";")
    return q


# ==========================================
# Count-Min Sketch with Sliding Window
# ==========================================

class SlidingWindowCMS:
    """A Count-Min Sketch with a time-based sliding window.

    Each bucket covers a 60-second interval. Old buckets are automatically
    evicted when the window size is exceeded.
    """

    def __init__(self, width: int = 1024, depth: int = 4, window_buckets: int = 60):
        self.width = width
        self.depth = depth
        self.window_buckets = window_buckets
        self._lock = threading.Lock()
        self._known_fingerprints: set = set()
        self.current_bucket_start: float = time.time()
        # Each bucket is a 2D list: depth rows × width columns
        self._buckets: deque = deque(maxlen=window_buckets)
        self._buckets.append(self._new_bucket())

    def _new_bucket(self) -> list:
        """Create a fresh zero-initialized bucket (depth × width)."""
        return [[0] * self.width for _ in range(self.depth)]

    def _hash(self, key: str, seed: int) -> int:
        """Deterministic hash for a key with a given seed."""
        return hash((key, seed)) % self.width

    def _rotate_if_needed(self, timestamp: float) -> None:
        """Append new zero buckets if the current time has advanced past the bucket boundary."""
        while timestamp >= self.current_bucket_start + 60.0:
            self._buckets.append(self._new_bucket())
            self.current_bucket_start += 60.0

    def record(self, fingerprint: str, timestamp: float) -> None:
        """Record a fingerprint observation at the given timestamp."""
        with self._lock:
            self._rotate_if_needed(timestamp)
            self._known_fingerprints.add(fingerprint)
            bucket = self._buckets[-1]  # current (latest) bucket
            for row in range(self.depth):
                col = self._hash(fingerprint, row)
                bucket[row][col] += 1

    def query_frequency(self, fingerprint: str) -> int:
        """Estimate the total frequency of a fingerprint across all window buckets.

        Returns the sum of per-bucket minimums across all hash rows.
        """
        with self._lock:
            total = 0
            for bucket in self._buckets:
                min_val = min(bucket[row][self._hash(fingerprint, row)] for row in range(self.depth))
                total += min_val
            return total

    def get_distribution_stats(self) -> tuple:
        """Compute mean and stddev of all non-zero fingerprint frequencies.

        Returns:
            (mean, stddev) tuple of floats.
        """
        with self._lock:
            fingerprints = list(self._known_fingerprints)

        if not fingerprints:
            return (0.0, 0.0)

        frequencies = []
        for fp in fingerprints:
            freq = self.query_frequency(fp)
            if freq > 0:
                frequencies.append(freq)

        if not frequencies:
            return (0.0, 0.0)

        n = len(frequencies)
        mean = sum(frequencies) / n
        if n < 2:
            return (mean, 0.0)
        variance = sum((f - mean) ** 2 for f in frequencies) / n
        stddev = math.sqrt(variance)
        return (mean, stddev)


# ==========================================
# Strike Tracker
# ==========================================

class StrikeTracker:
    """Tracks per-session anomaly strikes with time-based decay."""

    def __init__(self, max_strikes: int = 3, decay_seconds: float = 300):
        self.max_strikes = max_strikes
        self.decay_seconds = decay_seconds
        self._strikes: dict = {}  # session_id -> list of timestamps

    def record_strike(self, session_id: str, timestamp: float) -> int:
        """Record a strike for a session. Returns the current active strike count."""
        if session_id not in self._strikes:
            self._strikes[session_id] = []
        strikes = self._strikes[session_id]
        # Prune expired strikes
        cutoff = timestamp - self.decay_seconds
        self._strikes[session_id] = [t for t in strikes if t > cutoff]
        # Append new strike
        self._strikes[session_id].append(timestamp)
        return len(self._strikes[session_id])

    def should_jail(self, session_id: str) -> bool:
        """Check if a session has accumulated enough active strikes to be jailed."""
        if session_id not in self._strikes:
            return False
        now = time.time()
        cutoff = now - self.decay_seconds
        active = [t for t in self._strikes[session_id] if t > cutoff]
        return len(active) >= self.max_strikes


# ==========================================
# Anomaly Detector (Main Orchestrator)
# ==========================================

class AnomalyDetector:
    """Main anomaly detection orchestrator.

    Consumes telemetry from the Go proxy via SSE, scores query behavior
    using a Count-Min Sketch, and escalates anomalies through a strike
    system that can ultimately jail a session.
    """

    def __init__(self):
        self.anomaly_threshold = float(os.environ.get("AEGIS_ANOMALY_THRESHOLD", "3.0"))
        self.novelty_threshold = float(os.environ.get("AEGIS_NOVELTY_THRESHOLD", "2.0"))
        # Absolute burst threshold: fires when a single fingerprint dominates and
        # stddev is 0 (only one unique fingerprint seen). Env-configurable.
        self.burst_threshold = int(os.environ.get("AEGIS_BURST_THRESHOLD", "15"))
        # Simplified novelty baseline: a fingerprint with frequency==1 is flagged as
        # novel when the distribution mean exceeds this value. Env-configurable.
        self.novelty_baseline = float(os.environ.get("AEGIS_NOVELTY_BASELINE", "5.0"))
        strike_limit = int(os.environ.get("AEGIS_STRIKE_LIMIT", "3"))
        strike_decay = float(os.environ.get("AEGIS_STRIKE_DECAY", "300"))
        cms_width = int(os.environ.get("AEGIS_CMS_WIDTH", "1024"))
        cms_depth = int(os.environ.get("AEGIS_CMS_DEPTH", "4"))
        window_buckets = int(os.environ.get("AEGIS_WINDOW_BUCKETS", "60"))
        self.proxy_http_url = os.environ.get("AEGIS_ADMIN_URL") or os.environ.get("AEGIS_PROXY_HTTP_URL") or "http://localhost:5434"

        self.cms = SlidingWindowCMS(width=cms_width, depth=cms_depth, window_buckets=window_buckets)
        self.strikes = StrikeTracker(max_strikes=strike_limit, decay_seconds=strike_decay)
        self._started = False

    def start(self) -> None:
        """Launch the telemetry consumer as a daemon thread."""
        if self._started:
            return
        self._started = True
        t = threading.Thread(target=self._consume_telemetry, daemon=True)
        t.start()

    def _consume_telemetry(self) -> None:
        """Connect to the proxy SSE telemetry stream and process events.

        Reconnects automatically on connection errors with a 2-second backoff.
        """
        import requests
        url = f"{self.proxy_http_url}/telemetry/stream"
        while True:
            try:
                res = requests.get(url, headers={"Accept": "text/event-stream"}, stream=True)
                res.raise_for_status()
                for line_bytes in res.iter_lines():
                    if line_bytes:
                        line = line_bytes.decode("utf-8", errors="replace").rstrip("\n\r")
                        if line.startswith("data: "):
                            data_str = line[len("data: "):]
                            try:
                                event = json.loads(data_str)
                                self._process_event(event)
                            except json.JSONDecodeError:
                                continue
            except Exception as e:
                print(
                    json.dumps({
                        "level": "warn",
                        "msg": "telemetry stream connection error",
                        "error": str(e),
                    }),
                    file=sys.stderr,
                )
                time.sleep(2)

    def _process_event(self, event: dict) -> None:
        """Score a telemetry event and escalate anomalies.

        Scoring logic:
        1. Fingerprint the SQL query.
        2. Record in CMS and retrieve frequency.
        3. Compute z-score against current distribution.
        4. Flag frequency anomalies (z > threshold) or novelty anomalies.
        5. Record strikes and jail sessions that exceed the strike limit.
        """
        query = event.get("raw_query") or event.get("query", "")
        session_id = event.get("session_id", "")
        if not query or not session_id:
            return

        print(f"[DAEMON] Processed query: {query} for session: {session_id}")
        fp = fingerprint_sql(query)
        now = time.time()

        # Record and query
        self.cms.record(fp, now)
        frequency = self.cms.query_frequency(fp)
        mean, stddev = self.cms.get_distribution_stats()

        # Compute z-score
        if stddev > 0:
            z_score = (frequency - mean) / stddev
        else:
            z_score = 0.0

        # --- Frequency anomaly ---
        # Primary path: z-score fires when multiple fingerprints create a valid distribution.
        # Fallback path: when stddev==0 (single unique fingerprint dominates the window),
        # the z-score is always 0 and can never trigger. We catch this with an absolute
        # burst threshold so rapid-fire single-query attacks are still detected.
        if stddev > 0:
            frequency_anomaly = z_score > self.anomaly_threshold
        else:
            frequency_anomaly = frequency > self.burst_threshold

        # --- Novelty anomaly ---
        # Simplified: a fingerprint seen exactly once while the established mean is
        # already significant is inherently novel. The previous z-score approach failed
        # here because a dominant fingerprint skews the mean so high that the z-score
        # for a novel query only reaches ~-1.0, well below the -2.0 threshold.
        novelty_anomaly = frequency == 1 and mean > self.novelty_baseline

        if frequency_anomaly or novelty_anomaly:
            anomaly_type = "frequency" if frequency_anomaly else "novelty"
            print(
                json.dumps({
                    "level": "warn",
                    "msg": "anomaly detected",
                    "type": anomaly_type,
                    "session_id": session_id,
                    "fingerprint": fp,
                    "frequency": frequency,
                    "z_score": round(z_score, 4),
                    "mean": round(mean, 4),
                    "stddev": round(stddev, 4),
                }),
                file=sys.stderr,
            )

            strike_count = self.strikes.record_strike(session_id, now)

            if self.strikes.should_jail(session_id):
                print(
                    json.dumps({
                        "level": "error",
                        "msg": "session jailed",
                        "session_id": session_id,
                        "strikes": strike_count,
                    }),
                    file=sys.stderr,
                )
                print(f"[DAEMON] Anomaly threshold breached! Triggering jail for session...")
                self._jail_session(session_id)

    def _jail_session(self, session_id: str) -> None:
        """Send a jail request to the Go proxy for the given session."""
        import requests
        try:
            url = f"{self.proxy_http_url}/jail/{session_id}"
            response = requests.post(url, timeout=5)
            print(f"[DAEMON] /jail POST responded with Status: {response.status_code}, Body: {response.text}")
            response.raise_for_status()
            body = response.text
            print(
                json.dumps({
                    "level": "info",
                    "msg": "jail request sent",
                    "session_id": session_id,
                    "response": body,
                }),
                file=sys.stderr,
            )
        except Exception as e:
            print(f"[BACKGROUND DAEMON ERROR] Failed to execute /jail POST request. Reason: {e}")
            print(
                json.dumps({
                    "level": "error",
                    "msg": "failed to jail session",
                    "session_id": session_id,
                    "error": str(e),
                }),
                file=sys.stderr,
            )


# ==========================================
# Module-level convenience singleton
# ==========================================

_detector_instance = None
_detector_lock = threading.Lock()


def get_detector() -> AnomalyDetector:
    """Return the module-level AnomalyDetector singleton (lazy-initialized)."""
    global _detector_instance
    if _detector_instance is None:
        with _detector_lock:
            if _detector_instance is None:
                _detector_instance = AnomalyDetector()
    return _detector_instance


def start_detector():
    """Initialize and start the global anomaly detector daemon."""
    get_detector().start()
