"""Tests for backend.config.Settings."""

from pathlib import Path

import pytest
from pydantic import ValidationError

from backend.config import Settings


class TestSettingsDefaults:
    """Verify default values when only required fields are provided."""

    def test_defaults_with_required_keys(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        settings = Settings(
            anthropic_api_key="test-ant-key",
            openai_api_key="test-oai-key",
            _env_file=None,
        )

        # Required fields
        assert settings.anthropic_api_key == "test-ant-key"
        assert settings.openai_api_key == "test-oai-key"

        # Model defaults
        assert settings.interviewer_model == "claude-sonnet-4-20250514"
        assert settings.evaluator_model == "claude-sonnet-4-20250514"
        assert settings.coach_model == "claude-sonnet-4-20250514"

        # Speech defaults
        assert settings.tts_voice == "onyx"
        assert settings.tts_model == "tts-1"
        assert settings.whisper_model == "whisper-1"

        # Session defaults
        assert settings.default_timer_minutes == 45

        # Database default
        assert settings.database_url == "postgresql://localhost:5432/drill"

        # Storage default
        assert settings.data_dir == Path("data")

    def test_custom_values_override_defaults(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        settings = Settings(
            anthropic_api_key="test-ant-key",
            openai_api_key="test-oai-key",
            interviewer_model="claude-opus-4-20250514",
            default_timer_minutes=30,
            database_url="postgresql://localhost:5432/drill_custom",
            data_dir=Path("/tmp/drill_data"),
            _env_file=None,
        )

        assert settings.interviewer_model == "claude-opus-4-20250514"
        assert settings.default_timer_minutes == 30
        assert settings.database_url == "postgresql://localhost:5432/drill_custom"
        assert settings.data_dir == Path("/tmp/drill_data")


class TestSettingsValidation:
    """Verify that missing required fields raise validation errors."""

    def test_missing_anthropic_key_raises(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        with pytest.raises(ValidationError) as exc_info:
            Settings(
                openai_api_key="test-oai-key",
                _env_file=None,
            )

        errors = exc_info.value.errors()
        field_names = [e["loc"][0] for e in errors]
        assert "anthropic_api_key" in field_names

    def test_missing_openai_key_raises(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        with pytest.raises(ValidationError) as exc_info:
            Settings(
                anthropic_api_key="test-ant-key",
                _env_file=None,
            )

        errors = exc_info.value.errors()
        field_names = [e["loc"][0] for e in errors]
        assert "openai_api_key" in field_names

    def test_missing_both_keys_raises(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        with pytest.raises(ValidationError) as exc_info:
            Settings(_env_file=None)

        errors = exc_info.value.errors()
        field_names = [e["loc"][0] for e in errors]
        assert "anthropic_api_key" in field_names
        assert "openai_api_key" in field_names

    def test_timer_minutes_must_be_positive(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
        monkeypatch.delenv("OPENAI_API_KEY", raising=False)

        with pytest.raises(ValidationError):
            Settings(
                anthropic_api_key="test-ant-key",
                openai_api_key="test-oai-key",
                default_timer_minutes=0,
                _env_file=None,
            )


class TestSettingsFromEnv:
    """Verify that settings can be loaded from environment variables."""

    def test_loads_from_env_vars(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("ANTHROPIC_API_KEY", "env-ant-key")
        monkeypatch.setenv("OPENAI_API_KEY", "env-oai-key")
        monkeypatch.setenv("INTERVIEWER_MODEL", "claude-opus-4-20250514")
        monkeypatch.setenv("DEFAULT_TIMER_MINUTES", "60")

        settings = Settings(_env_file=None)

        assert settings.anthropic_api_key == "env-ant-key"
        assert settings.openai_api_key == "env-oai-key"
        assert settings.interviewer_model == "claude-opus-4-20250514"
        assert settings.default_timer_minutes == 60
