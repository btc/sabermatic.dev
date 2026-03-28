"""Tests for backend.session_manager — pure state machine, no I/O."""

import time

import pytest

from backend.session_manager import (
    InvalidTransition,
    SessionState,
    SessionStateMachine,
)


class TestSessionStateEnum:
    def test_all_states_exist(self) -> None:
        expected = {
            "IDLE",
            "STARTING",
            "INTERVIEWER_SPEAKING",
            "WAITING_FOR_CANDIDATE",
            "CANDIDATE_SPEAKING",
            "PROCESSING",
            "ENDING",
            "ENDED",
            "EVALUATING",
            "REVIEWED",
        }
        assert {s.name for s in SessionState} == expected

    def test_states_are_str_subclass(self) -> None:
        for state in SessionState:
            assert isinstance(state, str)


class TestInitialState:
    def test_initial_state_is_idle(self) -> None:
        sm = SessionStateMachine()
        assert sm.state == SessionState.IDLE

    def test_started_at_is_none(self) -> None:
        sm = SessionStateMachine()
        assert sm.started_at is None

    def test_turn_count_is_zero(self) -> None:
        sm = SessionStateMachine()
        assert sm.turn_count == 0


class TestValidTransitions:
    def test_idle_to_starting(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        assert sm.state == SessionState.STARTING

    def test_starting_to_interviewer_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        assert sm.state == SessionState.INTERVIEWER_SPEAKING

    def test_interviewer_speaking_to_waiting(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        assert sm.state == SessionState.WAITING_FOR_CANDIDATE

    def test_waiting_to_candidate_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.state == SessionState.CANDIDATE_SPEAKING

    def test_candidate_speaking_to_processing(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.PROCESSING)
        assert sm.state == SessionState.PROCESSING

    def test_processing_to_interviewer_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.PROCESSING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        assert sm.state == SessionState.INTERVIEWER_SPEAKING

    def test_full_normal_cycle(self) -> None:
        """IDLE→STARTING→INTERVIEWER_SPEAKING→WAITING→CANDIDATE_SPEAKING→PROCESSING→INTERVIEWER_SPEAKING"""
        sm = SessionStateMachine()
        for state in [
            SessionState.STARTING,
            SessionState.INTERVIEWER_SPEAKING,
            SessionState.WAITING_FOR_CANDIDATE,
            SessionState.CANDIDATE_SPEAKING,
            SessionState.PROCESSING,
            SessionState.INTERVIEWER_SPEAKING,
        ]:
            sm.transition(state)
        assert sm.state == SessionState.INTERVIEWER_SPEAKING

    def test_interrupt_interviewer_speaking_to_candidate_speaking(self) -> None:
        """Interrupt: INTERVIEWER_SPEAKING → CANDIDATE_SPEAKING (direct)."""
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.state == SessionState.CANDIDATE_SPEAKING

    def test_end_from_interviewer_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.ENDING)
        assert sm.state == SessionState.ENDING

    def test_end_from_waiting(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        sm.transition(SessionState.ENDING)
        assert sm.state == SessionState.ENDING

    def test_end_from_candidate_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.ENDING)
        assert sm.state == SessionState.ENDING

    def test_end_from_processing(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.PROCESSING)
        sm.transition(SessionState.ENDING)
        assert sm.state == SessionState.ENDING

    def test_ending_to_ended(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.ENDING)
        sm.transition(SessionState.ENDED)
        assert sm.state == SessionState.ENDED

    def test_ended_to_evaluating(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.ENDING)
        sm.transition(SessionState.ENDED)
        sm.transition(SessionState.EVALUATING)
        assert sm.state == SessionState.EVALUATING

    def test_evaluating_to_reviewed(self) -> None:
        sm = SessionStateMachine()
        for state in [
            SessionState.STARTING,
            SessionState.INTERVIEWER_SPEAKING,
            SessionState.ENDING,
            SessionState.ENDED,
            SessionState.EVALUATING,
            SessionState.REVIEWED,
        ]:
            sm.transition(state)
        assert sm.state == SessionState.REVIEWED


class TestInvalidTransitions:
    def test_idle_to_interviewer_speaking_raises(self) -> None:
        sm = SessionStateMachine()
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.INTERVIEWER_SPEAKING)

    def test_idle_to_candidate_speaking_raises(self) -> None:
        sm = SessionStateMachine()
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.CANDIDATE_SPEAKING)

    def test_starting_to_candidate_speaking_raises(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.CANDIDATE_SPEAKING)

    def test_processing_to_waiting_raises(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.PROCESSING)
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.WAITING_FOR_CANDIDATE)

    def test_reviewed_has_no_valid_transitions(self) -> None:
        sm = SessionStateMachine()
        for state in [
            SessionState.STARTING,
            SessionState.INTERVIEWER_SPEAKING,
            SessionState.ENDING,
            SessionState.ENDED,
            SessionState.EVALUATING,
            SessionState.REVIEWED,
        ]:
            sm.transition(state)
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.IDLE)

    def test_invalid_transition_message_contains_states(self) -> None:
        sm = SessionStateMachine()
        with pytest.raises(InvalidTransition, match="IDLE"):
            sm.transition(SessionState.PROCESSING)

    def test_same_state_transition_raises(self) -> None:
        """Transitioning to the current state is invalid unless listed."""
        sm = SessionStateMachine()
        with pytest.raises(InvalidTransition):
            sm.transition(SessionState.IDLE)


class TestStartedAt:
    def test_started_at_set_on_starting_transition(self) -> None:
        sm = SessionStateMachine()
        before = time.time()
        sm.transition(SessionState.STARTING)
        after = time.time()
        assert sm.started_at is not None
        assert before <= sm.started_at <= after

    def test_started_at_not_reset_on_subsequent_transitions(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        t = sm.started_at
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        assert sm.started_at == t


class TestElapsedSeconds:
    def test_elapsed_seconds_none_before_start(self) -> None:
        sm = SessionStateMachine()
        assert sm.elapsed_seconds is None

    def test_elapsed_seconds_non_negative_after_start(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        assert sm.elapsed_seconds >= 0

    def test_elapsed_seconds_is_int(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        assert isinstance(sm.elapsed_seconds, int)

    def test_elapsed_seconds_increases_over_time(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        # Backdate started_at to simulate time passing
        sm.started_at = time.time() - 5
        assert sm.elapsed_seconds >= 5


class TestTurnCount:
    def test_turn_count_does_not_increment_on_starting(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        assert sm.turn_count == 0

    def test_turn_count_increments_on_candidate_speaking(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.WAITING_FOR_CANDIDATE)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.turn_count == 1

    def test_turn_count_increments_on_interrupt(self) -> None:
        """Interrupt path also increments turn_count."""
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.turn_count == 1

    def test_turn_count_increments_each_cycle(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        sm.transition(SessionState.PROCESSING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.turn_count == 2


class TestIsActive:
    def test_idle_is_not_active(self) -> None:
        sm = SessionStateMachine()
        assert sm.is_active is False

    def test_starting_is_active(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        assert sm.is_active is True

    def test_interviewer_speaking_is_active(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        assert sm.is_active is True

    def test_candidate_speaking_is_active(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.CANDIDATE_SPEAKING)
        assert sm.is_active is True

    def test_ended_is_not_active(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.ENDING)
        sm.transition(SessionState.ENDED)
        assert sm.is_active is False

    def test_evaluating_is_not_active(self) -> None:
        sm = SessionStateMachine()
        sm.transition(SessionState.STARTING)
        sm.transition(SessionState.INTERVIEWER_SPEAKING)
        sm.transition(SessionState.ENDING)
        sm.transition(SessionState.ENDED)
        sm.transition(SessionState.EVALUATING)
        assert sm.is_active is False

    def test_reviewed_is_not_active(self) -> None:
        sm = SessionStateMachine()
        for state in [
            SessionState.STARTING,
            SessionState.INTERVIEWER_SPEAKING,
            SessionState.ENDING,
            SessionState.ENDED,
            SessionState.EVALUATING,
            SessionState.REVIEWED,
        ]:
            sm.transition(state)
        assert sm.is_active is False
