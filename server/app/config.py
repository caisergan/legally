"""Central configuration. All env vars live here."""

from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict

BASE_DIR = Path(__file__).resolve().parent.parent


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=BASE_DIR / ".env", extra="ignore")

    # --- core ---
    session_secret: str = "dev-secret-change-me"
    database_path: Path = BASE_DIR / "data" / "yargi_asistan.db"
    frontend_dist: Path = BASE_DIR.parent / "web" / "dist"

    # --- auth ---
    registration_mode: str = "open"  # open | closed
    session_ttl_days: int = 30

    # --- LLM ---
    anthropic_api_key: str = ""
    chat_model: str = "claude-sonnet-5"
    title_model: str = "claude-haiku-4-5-20251001"
    max_tool_calls_per_turn: int = 15
    max_output_tokens: int = 4096

    # --- MCP bridge ---
    # embedded: import yargi-mcp and run it in-process (default)
    # http: connect to a running MCP server at yargi_mcp_url
    mcp_mode: str = "embedded"
    yargi_mcp_url: str = "http://127.0.0.1:8555/mcp/"
    tool_timeout_seconds: float = 45.0

    # --- quotas (per user, per day) ---
    daily_llm_token_quota: int = 300_000
    daily_tool_call_quota: int = 300

    # --- document cache ---
    doc_cache_ttl_days: int = 30


settings = Settings()
settings.database_path.parent.mkdir(parents=True, exist_ok=True)
