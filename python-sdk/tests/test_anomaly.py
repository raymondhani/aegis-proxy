import unittest
import time
import sys
import os

# Ensure aegis_sdk is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from aegis_sdk.anomaly_detector import (
    fingerprint_sql,
    SlidingWindowCMS,
    StrikeTracker,
)

class TestFingerprintSQL(unittest.TestCase):
    def test_strips_string_literals(self):
        self.assertEqual(
            fingerprint_sql("SELECT * FROM users WHERE name = 'Alice'"),
            "select * from users where name = ?"
        )
    
    def test_strips_numeric_literals(self):
        self.assertEqual(
            fingerprint_sql("SELECT * FROM users WHERE id = 42"),
            "select * from users where id = ?"
        )
    
    def test_collapses_whitespace(self):
        self.assertEqual(
            fingerprint_sql("SELECT  *  FROM   users   WHERE  id = 1"),
            "select * from users where id = ?"
        )
    
    def test_strips_semicolons(self):
        self.assertEqual(
            fingerprint_sql("SELECT 1;"),
            "select ?"
        )
    
    def test_identical_fingerprints(self):
        q1 = "INSERT INTO orders (name, qty) VALUES ('Bob', 5)"
        q2 = "INSERT INTO orders (name, qty) VALUES ('Alice', 100)"
        self.assertEqual(fingerprint_sql(q1), fingerprint_sql(q2))
    
    def test_different_structures(self):
        q1 = "SELECT * FROM users"
        q2 = "DELETE FROM users WHERE id = 1"
        self.assertNotEqual(fingerprint_sql(q1), fingerprint_sql(q2))


class TestSlidingWindowCMS(unittest.TestCase):
    def test_record_and_query(self):
        cms = SlidingWindowCMS(width=64, depth=4, window_buckets=5)
        now = time.time()
        for _ in range(10):
            cms.record("select * from users where id = ?", now)
        freq = cms.query_frequency("select * from users where id = ?")
        self.assertEqual(freq, 10)
    
    def test_unknown_fingerprint_returns_zero(self):
        cms = SlidingWindowCMS(width=64, depth=4, window_buckets=5)
        freq = cms.query_frequency("never seen before")
        self.assertEqual(freq, 0)
    
    def test_distribution_stats(self):
        cms = SlidingWindowCMS(width=256, depth=4, window_buckets=5)
        now = time.time()
        for _ in range(10):
            cms.record("query_a", now)
        for _ in range(20):
            cms.record("query_b", now)
        mean, stddev = cms.get_distribution_stats()
        self.assertGreater(mean, 0)
        self.assertGreater(stddev, 0)


class TestStrikeTracker(unittest.TestCase):
    def test_strike_accumulation(self):
        tracker = StrikeTracker(max_strikes=3, decay_seconds=300)
        now = time.time()
        self.assertEqual(tracker.record_strike("sess1", now), 1)
        self.assertEqual(tracker.record_strike("sess1", now + 1), 2)
        self.assertEqual(tracker.record_strike("sess1", now + 2), 3)
        self.assertTrue(tracker.should_jail("sess1"))
    
    def test_strike_decay(self):
        tracker = StrikeTracker(max_strikes=3, decay_seconds=10)
        now = time.time()
        tracker.record_strike("sess1", now)
        tracker.record_strike("sess1", now + 1)
        # Simulate time passing beyond decay
        tracker.record_strike("sess1", now + 15)
        # Only 1 strike should remain (the one at now+15, the first two decayed)
        self.assertFalse(tracker.should_jail("sess1"))
    
    def test_different_sessions_independent(self):
        tracker = StrikeTracker(max_strikes=2, decay_seconds=300)
        now = time.time()
        tracker.record_strike("sess1", now)
        tracker.record_strike("sess2", now)
        self.assertFalse(tracker.should_jail("sess1"))
        self.assertFalse(tracker.should_jail("sess2"))


if __name__ == "__main__":
    unittest.main()
