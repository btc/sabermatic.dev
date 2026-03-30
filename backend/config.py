from pathlib import Path

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    # Required API keys
    anthropic_api_key: str
    openai_api_key: str

    # Model configuration
    interviewer_model: str = "claude-sonnet-4-20250514"
    evaluator_model: str = "claude-sonnet-4-20250514"
    coach_model: str = "claude-sonnet-4-20250514"
    educator_model: str = "claude-opus-4-6"

    # Speech configuration
    tts_voice: str = "onyx"
    tts_model: str = "tts-1"
    whisper_model: str = "whisper-1"

    # Session defaults
    default_timer_minutes: int = Field(default=45, ge=1)

    # Database
    database_url: str = "postgresql://localhost:5432/drill"

    # Storage
    data_dir: Path = Path("data")
