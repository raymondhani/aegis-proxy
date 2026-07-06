import os
import pytest

@pytest.fixture(autouse=True)
def mock_neon_env():
    """Sets dummy environment variables for Neon API to prevent missing variable errors on CI."""
    os.environ.setdefault("NEON_API_KEY", "dummy_neon_api_key_12345")
    os.environ.setdefault("NEON_PROJECT_ID", "dummy-project-id-67890")
    yield

@pytest.fixture
def db_url():
    """Provides a dummy database connection string for offline SDK tests."""
    return "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db"
