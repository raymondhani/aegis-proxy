import pytest

@pytest.fixture
def db_url():
    """Provides a dummy database connection string for offline SDK tests."""
    return "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db"
