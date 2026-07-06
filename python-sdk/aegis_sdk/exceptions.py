class AegisJailError(Exception):
    """Raised when a session has been jailed by the anomaly detection engine."""
    def __init__(self, session_id: str, reason: str):
        self.session_id = session_id
        self.reason = reason
        super().__init__(f"Session {session_id} has been jailed: {reason}")
