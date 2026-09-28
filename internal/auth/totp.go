package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters. These are the values authenticator apps default to.
const (
	totpPeriod = 30
	totpDigits = otp.DigitsSix
	totpAlgo   = otp.AlgorithmSHA1
	// totpSkew allows one time step either side of now, absorbing clock drift
	// and the time it takes to type a code.
	totpSkew = 1
)

// TOTPIssuer labels the entry in the admin's authenticator app.
const TOTPIssuer = "Модули - заявки"

// ErrInvalidTOTPCode is returned when a code does not match any accepted step.
var ErrInvalidTOTPCode = errors.New("невалиден код")

// Enrollment is a freshly generated, not yet confirmed TOTP secret.
type Enrollment struct {
	Secret string
	// QRDataURI is an inline PNG, so the page needs no external image request.
	QRDataURI string
	// ManualEntry is the secret grouped for typing by hand.
	ManualEntry string
}

// NewEnrollment generates a TOTP secret for an admin and renders its QR code.
func NewEnrollment(accountName string) (*Enrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: accountName,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   totpAlgo,
	})
	if err != nil {
		return nil, fmt.Errorf("generate TOTP secret: %w", err)
	}

	img, err := key.Image(240, 240)
	if err != nil {
		return nil, fmt.Errorf("render TOTP QR code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode TOTP QR code: %w", err)
	}

	return &Enrollment{
		Secret:      key.Secret(),
		QRDataURI:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
		ManualEntry: FormatTOTPSecret(key.Secret()),
	}, nil
}

// VerifyTOTP checks code against secret at time now, allowing one step of
// skew, and returns the time step the code belongs to.
//
// The step is what makes replay detection possible: the caller stores it and
// refuses any later code whose step is not strictly greater, so a code
// observed over someone's shoulder cannot be used a second time within its
// window.
func VerifyTOTP(secret, code string, now time.Time) (step int64, err error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, ErrInvalidTOTPCode
	}
	// Reject anything that is not exactly six digits before doing work.
	if len(code) != 6 {
		return 0, ErrInvalidTOTPCode
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, ErrInvalidTOTPCode
		}
	}

	opts := totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      0, // handled explicitly below, so the matching step is known
		Digits:    totpDigits,
		Algorithm: totpAlgo,
	}

	for delta := -totpSkew; delta <= totpSkew; delta++ {
		at := now.Add(time.Duration(delta) * totpPeriod * time.Second)
		expected, genErr := totp.GenerateCodeCustom(secret, at, opts)
		if genErr != nil {
			return 0, fmt.Errorf("generate TOTP code: %w", genErr)
		}
		if ConstantTimeEqual(expected, code) {
			return at.UTC().Unix() / totpPeriod, nil
		}
	}
	return 0, ErrInvalidTOTPCode
}

// EnrollmentFromSecret re-renders the QR code and manual entry string for a
// secret that was already generated.
//
// A mistyped confirmation code must not force the admin to scan a new QR
// code, so the candidate secret survives the failed attempt.
func EnrollmentFromSecret(accountName, secret string) (*Enrollment, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, errors.New("empty TOTP secret")
	}

	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   "/" + TOTPIssuer + ":" + accountName,
		RawQuery: url.Values{
			"secret":    {secret},
			"issuer":    {TOTPIssuer},
			"algorithm": {totpAlgo.String()},
			"digits":    {totpDigits.String()},
			"period":    {strconv.Itoa(totpPeriod)},
		}.Encode(),
	}

	key, err := otp.NewKeyFromURL(u.String())
	if err != nil {
		return nil, fmt.Errorf("rebuild TOTP key: %w", err)
	}

	img, err := key.Image(240, 240)
	if err != nil {
		return nil, fmt.Errorf("render TOTP QR code: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode TOTP QR code: %w", err)
	}

	return &Enrollment{
		Secret:      secret,
		QRDataURI:   "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
		ManualEntry: FormatTOTPSecret(secret),
	}, nil
}
