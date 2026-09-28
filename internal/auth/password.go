// Package auth holds authentication primitives: password hashing, email
// validation, secret encryption, sessions, CSRF tokens, TOTP and login
// throttling.
package auth

import (
	"errors"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the shortest password the application accepts.
const MinPasswordLength = 10

// bcryptCost is deliberately above the library default.
const bcryptCost = 12

// ErrPasswordTooShort is returned by ValidatePassword.
var ErrPasswordTooShort = errors.New("паролата трябва да е поне 10 знака")

// ValidatePassword enforces the minimum length, counting characters rather
// than bytes so Cyrillic passwords are not penalised.
func ValidatePassword(p string) error {
	if utf8.RuneCountInString(p) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// HashPassword returns a bcrypt hash of p.
//
// bcrypt silently truncates at 72 bytes, so an over-long password is
// rejected rather than quietly weakened.
func HashPassword(p string) (string, error) {
	if err := ValidatePassword(p); err != nil {
		return "", err
	}
	if len(p) > 72 {
		return "", errors.New("паролата е твърде дълга (максимум 72 байта)")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword reports whether p matches the stored hash.
func CheckPassword(hash, p string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
}
