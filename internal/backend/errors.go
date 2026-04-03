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
)

// ValidatePasswordLength returns ErrPasswordLength if the password is too short or too long.
func ValidatePasswordLength(password string) error {
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		return ErrPasswordLength
	}
	return nil
}
