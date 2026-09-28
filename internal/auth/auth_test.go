package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestNormalizeEmail(t *testing.T) {
	valid := []struct{ in, want string }{
		{"user@example.com", "user@example.com"},
		{"  User@Example.COM  ", "user@example.com"},
		{"first.last+tag@sub.example.co.uk", "first.last+tag@sub.example.co.uk"},
	}
	for _, tt := range valid {
		got, err := NormalizeEmail(tt.in)
		if err != nil {
			t.Errorf("NormalizeEmail(%q) returned error %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	invalid := []string{
		"",
		"not-an-email",
		"user@localhost",             // no dot in the domain
		"user@example.",              // empty last label
		"user@@example.com",          // two @
		`"Display Name" <a@b.com>`,   // name form
		"<a@b.com>",                  // angle brackets
		"a@b.com, c@d.com",           // list, not one address
		"user@.example.com",          // empty first label
		"a@b.com (comment)",          // trailing comment
		"Display Name <user@ex.com>", // name form without quotes
	}
	for _, in := range invalid {
		if got, err := NormalizeEmail(in); err == nil {
			t.Errorf("NormalizeEmail(%q) = %q, want an error", in, got)
		}
	}
}

func TestNormalizeEmailList(t *testing.T) {
	valid, invalid := NormalizeEmailList("A@x.com, b@y.com; b@y.com\nbroken@ , c@z.com")
	want := []string{"a@x.com", "b@y.com", "c@z.com"}
	if len(valid) != len(want) {
		t.Fatalf("valid = %v, want %v", valid, want)
	}
	for i := range want {
		if valid[i] != want[i] {
			t.Errorf("valid[%d] = %q, want %q", i, valid[i], want[i])
		}
	}
	if len(invalid) != 1 || invalid[0] != "broken@" {
		t.Errorf("invalid = %v, want [broken@]", invalid)
	}
}

func TestPasswordHashing(t *testing.T) {
	if err := ValidatePassword("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("ValidatePassword(short) = %v, want ErrPasswordTooShort", err)
	}
	// Ten Cyrillic characters are ten characters, not twenty bytes.
	if err := ValidatePassword("паролатаа1"); err != nil {
		t.Errorf("ValidatePassword on a 10-rune password: %v", err)
	}

	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(hash, "correct horse battery") {
		t.Error("CheckPassword rejected the correct password")
	}
	if CheckPassword(hash, "wrong horse battery") {
		t.Error("CheckPassword accepted the wrong password")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("HashPassword accepted a too-short password")
	}
}

func TestEncrypterRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	e, err := NewEncrypter(key)
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}

	sealed, err := e.Encrypt("smtp-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := e.Decrypt(sealed)
	if err != nil || got != "smtp-secret" {
		t.Fatalf("Decrypt = %q, %v; want %q", got, err, "smtp-secret")
	}

	// An empty ciphertext means "not set", not an error.
	if got, err := e.Decrypt(nil); err != nil || got != "" {
		t.Errorf("Decrypt(nil) = %q, %v; want empty", got, err)
	}

	// A different key must not open the ciphertext.
	other, _ := NewEncrypter(make([]byte, 32))
	if _, err := other.Decrypt(sealed); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt with the wrong key = %v, want ErrDecrypt", err)
	}

	if _, err := NewEncrypter(make([]byte, 16)); err == nil {
		t.Error("NewEncrypter accepted a 16-byte key")
	}
}

func TestVerifyTOTPAcceptsSkewAndReportsStep(t *testing.T) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "a@b.com"})
	if err != nil {
		t.Fatalf("totp.Generate: %v", err)
	}
	secret := key.Secret()
	now := time.Date(2026, 9, 17, 12, 0, 30, 0, time.UTC)

	opts := totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

	for _, delta := range []int{-1, 0, 1} {
		at := now.Add(time.Duration(delta) * 30 * time.Second)
		code, err := totp.GenerateCodeCustom(secret, at, opts)
		if err != nil {
			t.Fatalf("GenerateCodeCustom: %v", err)
		}
		step, err := VerifyTOTP(secret, code, now)
		if err != nil {
			t.Errorf("VerifyTOTP at step %+d: %v", delta, err)
			continue
		}
		if want := at.Unix() / 30; step != want {
			t.Errorf("VerifyTOTP at step %+d returned step %d, want %d", delta, step, want)
		}
	}

	// Two steps away is outside the accepted window.
	far, _ := totp.GenerateCodeCustom(secret, now.Add(120*time.Second), opts)
	if _, err := VerifyTOTP(secret, far, now); !errors.Is(err, ErrInvalidTOTPCode) {
		t.Errorf("VerifyTOTP outside the window = %v, want ErrInvalidTOTPCode", err)
	}
}

func TestVerifyTOTPRejectsMalformedCodes(t *testing.T) {
	key, _ := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "a@b.com"})
	now := time.Now()
	for _, code := range []string{"", "12345", "1234567", "12345a", "abcdef", "  "} {
		if _, err := VerifyTOTP(key.Secret(), code, now); !errors.Is(err, ErrInvalidTOTPCode) {
			t.Errorf("VerifyTOTP(%q) = %v, want ErrInvalidTOTPCode", code, err)
		}
	}
}

func TestNewEnrollmentProducesUsableSecret(t *testing.T) {
	e, err := NewEnrollment("admin@example.com")
	if err != nil {
		t.Fatalf("NewEnrollment: %v", err)
	}
	if e.Secret == "" {
		t.Fatal("enrollment has an empty secret")
	}
	if len(e.QRDataURI) < 100 || e.QRDataURI[:22] != "data:image/png;base64," {
		t.Errorf("QRDataURI is not an inline PNG: %.40q", e.QRDataURI)
	}
	if e.ManualEntry == "" {
		t.Error("ManualEntry is empty")
	}

	code, err := totp.GenerateCode(e.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if _, err := VerifyTOTP(e.Secret, code, time.Now()); err != nil {
		t.Errorf("a freshly generated code did not verify: %v", err)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(10)
	if err != nil {
		t.Fatalf("NewRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}

	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("code %q is not in XXXXX-XXXXX form", c)
		}
		if seen[c] {
			t.Errorf("duplicate recovery code %q", c)
		}
		seen[c] = true
	}

	// The same code hashes identically however it is typed.
	h := HashRecoveryCode(codes[0])
	for _, variant := range []string{
		codes[0],
		NormalizeRecoveryCode(codes[0]),
		" " + codes[0] + " ",
	} {
		if HashRecoveryCode(variant) != h {
			t.Errorf("HashRecoveryCode(%q) differs from the canonical hash", variant)
		}
	}
	if HashRecoveryCode(codes[1]) == h {
		t.Error("different codes hashed to the same value")
	}
}

func TestHashTokenIsStableAndDistinct(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	b, _ := NewToken()
	if a == b {
		t.Fatal("NewToken returned the same token twice")
	}
	first, second := HashToken(a), HashToken(a)
	if first != second {
		t.Error("HashToken is not stable")
	}
	if HashToken(a) == HashToken(b) {
		t.Error("distinct tokens hashed to the same value")
	}
	if HashToken(a) == a {
		t.Error("HashToken returned the token itself")
	}
}
