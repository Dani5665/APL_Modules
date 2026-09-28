package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Encrypter seals secrets kept at rest: the SMTP password and each admin's
// TOTP secret.
type Encrypter struct {
	aead cipher.AEAD
}

// NewEncrypter builds an AES-GCM encrypter from a 32-byte key.
func NewEncrypter(key []byte) (*Encrypter, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encrypter{aead: aead}, nil
}

// ErrDecrypt is returned when a ciphertext cannot be opened, which usually
// means APP_ENCRYPTION_KEY changed.
var ErrDecrypt = errors.New("не може да се разшифрова запазената тайна (променен ли е APP_ENCRYPTION_KEY?)")

// Encrypt seals plaintext, prefixing the random nonce.
func (e *Encrypter) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return e.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens a ciphertext produced by Encrypt. An empty input yields an
// empty string, so an unset secret is not an error.
func (e *Encrypter) Decrypt(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	n := e.aead.NonceSize()
	if len(sealed) < n {
		return "", ErrDecrypt
	}
	plaintext, err := e.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plaintext), nil
}

// NewToken returns a URL-safe random token with 32 bytes of entropy, used for
// session cookies and pre-session cookies.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the hex SHA-256 of a token. Only hashes are stored, so a
// leaked database does not yield usable session cookies.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual compares two strings without leaking their contents
// through timing.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// recoveryCodeAlphabet omits characters that are easily confused when a code
// is read off a printout.
const recoveryCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewRecoveryCodes returns n single-use codes in the form XXXXX-XXXXX.
func NewRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		code, err := randomCode(10)
		if err != nil {
			return nil, err
		}
		out = append(out, code[:5]+"-"+code[5:])
	}
	return out, nil
}

func randomCode(length int) (string, error) {
	b := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	// len(alphabet) is 32, a divisor of 256, so masking introduces no bias.
	var sb strings.Builder
	for _, v := range b {
		sb.WriteByte(recoveryCodeAlphabet[int(v)%len(recoveryCodeAlphabet)])
	}
	return sb.String(), nil
}

// NormalizeRecoveryCode strips formatting so a code typed with or without its
// dash and in any case still matches.
func NormalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(code)))
}

// HashRecoveryCode hashes a normalised recovery code for storage.
//
// Recovery codes carry 50 bits of entropy from a uniform alphabet, so a plain
// SHA-256 is sufficient: unlike a password, there is no low-entropy guess to
// accelerate.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}

// FormatTOTPSecret renders a TOTP secret in the grouped form shown for manual
// entry into an authenticator app.
func FormatTOTPSecret(secret string) string {
	secret = strings.ToUpper(strings.TrimRight(secret, "="))
	var sb strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			sb.WriteByte(' ')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
