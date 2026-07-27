import os
import urllib.parse
import requests

class AegisClient:
    def __init__(self, api_key: str = None, api_url: str = None):
        self.api_key = api_key or os.getenv("AEGIS_API_KEY")
        self.api_url = api_url or os.getenv("AEGIS_API_URL", "http://localhost:3000")
        
        if not self.api_key:
            raise ValueError("AEGIS_API_KEY is required")

    def get_connection_string(self, original_db_url: str) -> str:
        """
        Registers the session with the Aegis Control Plane and returns the injected proxy connection string.
        """
        parsed = urllib.parse.urlparse(original_db_url)
        target_host = parsed.hostname
        if parsed.port:
            target_host = f"{target_host}:{parsed.port}"
        else:
            target_host = f"{target_host}:5432"
            
        jwt_token = self.api_key
        proxy_host = os.getenv("AEGIS_PROXY_HOST", "localhost:5433")
        
        query = urllib.parse.parse_qs(parsed.query)
        query['aegis_jwt'] = [jwt_token]
        query['sslmode'] = ['disable'] # Proxy connection must be plain text so it can intercept StartupMessage
        new_query = urllib.parse.urlencode(query, doseq=True)
        
        new_netloc = proxy_host
        if parsed.username:
            auth = parsed.username
            if parsed.password:
                auth += f":{parsed.password}"
            new_netloc = f"{auth}@{proxy_host}"
            
        new_url = urllib.parse.urlunparse((
            parsed.scheme,
            new_netloc,
            parsed.path,
            parsed.params,
            new_query,
            parsed.fragment
        ))
        
        return new_url
