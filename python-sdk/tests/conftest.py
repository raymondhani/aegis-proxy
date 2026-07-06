import pytest
from unittest.mock import patch, MagicMock

@pytest.fixture(autouse=True)
def mock_neon_api_requests():
    """Globally mocks Neon API interactions to allow offline SDK validation tests."""
    with patch("requests.post") as mock_post, \
         patch("requests.get") as mock_get, \
         patch("requests.delete") as mock_delete:
        
        # Mock branch creation payload
        mock_post_res = MagicMock()
        mock_post_res.status_code = 201
        mock_post_res.json.return_value = {
            "connection_string": "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db",
            "branch": {"id": "aegis-branch-dummy-id"},
            "endpoints": [{"id": "ep-dummy-id", "host": "localhost"}]
        }
        mock_post.return_value = mock_post_res

        # Mock GET requests for status and connection URI
        def mock_get_impl(url, *args, **kwargs):
            res = MagicMock()
            res.status_code = 200
            if "endpoints" in url:
                res.json.return_value = {"endpoint": {"current_state": "active"}}
            elif "connection_uri" in url:
                res.json.value = {"uri": "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db"}
                res.json.return_value = {"uri": "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db"}
            else:
                res.json.return_value = {}
            return res
            
        mock_get.side_effect = mock_get_impl

        # Mock branch teardown payload
        mock_del_res = MagicMock()
        mock_del_res.status_code = 200
        mock_delete.return_value = mock_del_res

        yield

@pytest.fixture(autouse=True)
def mock_psycopg2():
    """Globally mocks psycopg2 interactions to allow offline database tests without a running Postgres."""
    # We keep track of tables created in the test to mock the schema snapshot
    created_tables = set()

    with patch("psycopg2.connect") as mock_connect:
        mock_conn = MagicMock()
        mock_cur = MagicMock()

        # Handle queries dynamically
        def execute_impl(query, params=None):
            q = query.strip().lower()
            if "create table" in q:
                parts = query.split()
                parts_lower = [p.lower() for p in parts]
                try:
                    tbl_idx = parts_lower.index("table") + 1
                    tbl_name = parts[tbl_idx].strip("() \n\t,;")
                    created_tables.add(tbl_name)
                except Exception:
                    pass
            elif "drop table" in q:
                parts = query.split()
                parts_lower = [p.lower() for p in parts]
                try:
                    tbl_idx = parts_lower.index("table") + 1
                    if tbl_idx < len(parts_lower) and parts_lower[tbl_idx] == "if" and (tbl_idx+1) < len(parts_lower) and parts_lower[tbl_idx+1] == "exists":
                        tbl_name = parts[tbl_idx+2].strip("() \n\t,;")
                    else:
                        tbl_name = parts[tbl_idx].strip("() \n\t,;")
                    created_tables.discard(tbl_name)
                except Exception:
                    pass

        mock_cur.execute.side_effect = execute_impl

        def fetchone_impl():
            # Return dummy but valid results for fetchone
            return ("Aegis Agent",)

        mock_cur.fetchone.side_effect = fetchone_impl

        def fetchall_impl():
            calls = [c[0][0] for c in mock_cur.execute.call_args_list]
            if not calls:
                return []
            last_query = calls[-1].strip().lower()
            
            if "information_schema.columns" in last_query:
                rows = []
                for tbl in created_tables:
                    rows.append((tbl, "id"))
                    rows.append((tbl, "name"))
                return rows
            elif "information_schema.tables" in last_query:
                # If "test_aegis_nopk" in created_tables, return it to trigger validation error
                if "test_aegis_nopk" in created_tables:
                    return [("test_aegis_nopk",)]
                return []
            return []

        mock_cur.fetchall.side_effect = fetchall_impl

        mock_conn.cursor.return_value = mock_cur
        mock_conn.__enter__.return_value = mock_conn
        mock_cur.__enter__.return_value = mock_cur
        
        mock_connect.return_value = mock_conn
        yield

@pytest.fixture
def db_url():
    """Provides a fallback database connection string for tests."""
    return "postgresql://dummy_user:dummy_pass@localhost:5432/dummy_db"
