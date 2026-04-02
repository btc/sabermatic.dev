"""Session state machine — pure logic, no I/O."""

import enum
import time


class SessionState(str, enum.Enum):
    IDLE = "idle"
    STARTING = "starting"
    INTERVIEWER_SPEAKING = "interviewer_speaking"
    WAITING_FOR_CANDIDATE = "waiting_for_candidate"
    CANDIDATE_SPEAKING = "candidate_speaking"
    PROCESSING = "processing"
    ENDING = "ending"
    ENDED = "ended"
    EVALUATING = "evaluating"
    REVIEWED = "reviewed"


class InvalidTransition(Exception):
    """Raised when a state transition is not permitted."""


# Valid transitions: mapping from current state → allowed next states.
TRANSITIONS: dict[SessionState, set[SessionState]] = {
    SessionState.IDLE: {SessionState.STARTING},
    SessionState.STARTING: {SessionState.INTERVIEWER_SPEAKING},
    SessionState.INTERVIEWER_SPEAKING: {
        SessionState.WAITING_FOR_CANDIDATE,
        SessionState.CANDIDATE_SPEAKING,  # interrupt
        SessionState.ENDING,
    },
    SessionState.WAITING_FOR_CANDIDATE: {
        SessionState.CANDIDATE_SPEAKING,
        SessionState.ENDING,
    },
    SessionState.CANDIDATE_SPEAKING: {
        SessionState.PROCESSING,
        SessionState.ENDING,
    },
    SessionState.PROCESSING: {
        SessionState.INTERVIEWER_SPEAKING,
        SessionState.ENDING,
    },
    SessionState.ENDING: {SessionState.ENDED},
    SessionState.ENDED: {SessionState.EVALUATING},
    SessionState.EVALUATING: {SessionState.REVIEWED},
    SessionState.REVIEWED: set(),
}

# States that are NOT considered active.
_INACTIVE_STATES: frozenset[SessionState] = frozenset(
    {
        SessionState.IDLE,
        SessionState.ENDED,
        SessionState.EVALUATING,
        SessionState.REVIEWED,
    }
)


class SessionStateMachine:
    """Tracks the live state of a single interview session."""

    def __init__(self) -> None:
        self.state: SessionState = SessionState.IDLE
        self.started_at: float | None = None
        self.turn_count: int = 0

    def transition(self, new_state: SessionState) -> None:
        """Advance to *new_state*, or raise InvalidTransition."""
        allowed = TRANSITIONS[self.state]
        if new_state not in allowed:
            raise InvalidTransition(
                f"Cannot transition from {self.state.name} to {new_state.name}"
            )

        # Record the moment the session begins.
        if new_state == SessionState.STARTING:
            self.started_at = time.time()

        # Each time the candidate starts speaking counts as a new turn.
        if new_state == SessionState.CANDIDATE_SPEAKING:
            self.turn_count += 1

        self.state = new_state

    @property
    def elapsed_seconds(self) -> int | None:
        """Seconds since the session started, or None if not yet started."""
        if self.started_at is None:
            return None
        return int(time.time() - self.started_at)

    @property
    def is_active(self) -> bool:
        """True when the session is in progress (not idle or terminal)."""
        return self.state not in _INACTIVE_STATES
