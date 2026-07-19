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
    anthropic_base_url: str = ""
    # Some Anthropic-compatible proxies (e.g. cliproxyapi behind a WAF) block the SDK's
    # default "AsyncAnthropic/Python …" User-Agent. Sending a benign UA avoids that.
    anthropic_user_agent: str = "yargi-asistan"
    chat_model: str = "claude-sonnet-5"
    title_model: str = "claude-haiku-4-5-20251001"
    model_sonnet5: str = "claude-sonnet-5"
    model_opus: str = "claude-opus-4-8"
    model_haiku: str = "claude-haiku-4-5-20251001"
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

    # --- signing (e-imza) — every flag disabled by default ---
    signing_enabled: bool = False
    signing_kill_switch: bool = False
    signing_mode: str = "softhsm"
    signing_test_only: bool = True
    # Private artifact + runtime state root (never inside `data/` / SQLite).
    signing_state_dir: Path = BASE_DIR / "var" / "signing"
    signing_max_upload_bytes: int = 26_214_400  # 25 MiB
    signing_max_output_bytes: int = 52_428_800  # 50 MiB
    signing_input_retention_hours: int = 720  # 30 days
    signing_pin_window_seconds: int = 90
    signing_approval_ttl_seconds: int = 300
    signing_capability_ttl_seconds: int = 30
    signing_download_ttl_seconds: int = 120
    # Signer / broker private transport.
    signing_signer_socket: Path = Path("/run/yargi/signerd.sock")
    signing_broker_socket: Path = Path("/run/yargi/signing-broker.sock")
    signing_transport_group: str = "yargi-signing"
    signing_service_uid: int | None = None  # FastAPI service account UID
    signing_signer_uid: int | None = None  # signerd service account UID (broker allowlist)
    signing_clock_skew_seconds: int = 30
    signing_request_timeout_seconds: float = 10.0
    # Command-authentication keys (Ed25519). FastAPI signs signer commands; the
    # broker verifies pinned signerd command keys. Values are configuration
    # paths/pins, never secrets embedded here.
    signing_command_key_path: Path | None = None
    signing_command_key_id: str = "fastapi-cmd-1"  # kid FastAPI signs signer commands with
    signing_signer_command_pubkeys: str = ""  # "kid:hex,kid:hex" pinned signerd keys
    signing_challenge_pubkeys: str = ""  # pinned signerd challenge-verification keyset
    signing_capability_secret: str = ""  # broker capability HMAC secret (required when enabled)
    signing_capability_key_id: str = "broker-cap-1"


settings = Settings()
settings.database_path.parent.mkdir(parents=True, exist_ok=True)
