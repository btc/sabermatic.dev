package backend

import "errors"

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrDuplicateEmail     = errors.New("email already registered")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrPasswordLength     = errors.New("password must be between 8 and 128 characters")
	ErrMissingFields      = errors.New("email, password, and display_name are required")
	ErrUserNotFound       = errors.New("user not found")
)
