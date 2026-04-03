package backend

import "fmt"

const (
	MinPasswordLen = 8
	MaxPasswordLen = 72 // bcrypt truncates beyond 72 bytes
)

var (
	ErrInvalidCredentials = fmt.Errorf("invalid email or password")
	ErrDuplicateEmail     = fmt.Errorf("email already registered")
	ErrInvalidToken       = fmt.Errorf("invalid or expired token")
	ErrPasswordLength     = fmt.Errorf("password must be between %d and %d characters", MinPasswordLen, MaxPasswordLen)
	ErrMissingFields      = fmt.Errorf("email, password, and display_name are required")
	ErrUserNotFound       = fmt.Errorf("user not found")
	ErrSessionNotFound    = fmt.Errorf("session not found")
	ErrSessionNotActive   = fmt.Errorf("session is not active")
	ErrSessionNotOwned    = fmt.Errorf("session does not belong to user")
	ErrQuestionNotFound   = fmt.Errorf("question not found")
	ErrInvalidDuration    = fmt.Errorf("duration must be between 1 and 180 minutes")
	ErrEvaluationNotReady  = fmt.Errorf("evaluation not ready")
	ErrNotEvaluationFailed = fmt.Errorf("session is not in evaluation_failed status")
	ErrAlreadyExists       = fmt.Errorf("educator analysis already exists")
	ErrNoNewSessions       = fmt.Errorf("no new sessions since last analysis")

	ErrInsufficientBalance    = fmt.Errorf("insufficient minute balance")
	ErrConcurrentSessionLimit = fmt.Errorf("concurrent session limit reached")
	ErrDurationExceedsPlan    = fmt.Errorf("duration exceeds plan maximum")
	ErrNoPaidBalance          = fmt.Errorf("paid minute balance required")
	ErrNoStripeAccount        = fmt.Errorf("no billing account")
)

// ValidatePasswordLength returns ErrPasswordLength if the password is too short or too long.
func ValidatePasswordLength(password string) error {
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		return ErrPasswordLength
	}
	return nil
}
