package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

const (
	totpPeriod     = 30
	totpSkew       = 1
	totpDigits     = 6
	totpIssuer     = "mc-panel"
	totpSecretSize = 20
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// newTOTPKey creates a new random TOTP secret (base32) and its otpauth:// URL.
func newTOTPKey(username string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
		Period:      totpPeriod,
		SecretSize:  totpSecretSize,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

func totpCodeAt(secret string, step int64) (string, error) {
	return totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0).UTC(), totpOpts)
}

// matchTOTP checks code against the steps now-1 … now+1 and returns the highest
// matching step. Replay protection (step > last used step) is the caller's job.
func matchTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.Join(strings.Fields(code), "")
	if len(code) != totpDigits {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	cur := totpStep(now)
	for s := cur + totpSkew; s >= cur-totpSkew; s-- {
		want, err := totpCodeAt(secret, s)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

func totpAAD(userID int64) []byte { return []byte(fmt.Sprintf("totp:%d", userID)) }

func sealTOTPSecret(box *secretbox.Box, userID int64, secret string) ([]byte, error) {
	return box.Seal([]byte(secret), totpAAD(userID))
}

func openTOTPSecret(box *secretbox.Box, userID int64, enc []byte) (string, error) {
	pt, err := box.Open(enc, totpAAD(userID))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// A secret created by POST /me/totp/setup is stored sealed as "p1|<created unix ms>|<secret>" until
// it is activated; activation re-seals the bare secret. The creation time lets activation expire
// (pendingTOTPTTL) without a schema change.
const pendingTOTPPrefix = "p1|"

var errNoPendingTOTP = errors.New("auth: no pending TOTP secret")

func sealPendingTOTPSecret(box *secretbox.Box, userID int64, secret string, now time.Time) ([]byte, error) {
	return sealTOTPSecret(box, userID, fmt.Sprintf("%s%d|%s", pendingTOTPPrefix, now.UnixMilli(), secret))
}

func openPendingTOTPSecret(box *secretbox.Box, userID int64, enc []byte) (secret string, created time.Time, err error) {
	pt, err := openTOTPSecret(box, userID, enc)
	if err != nil {
		return "", time.Time{}, err
	}
	rest, ok := strings.CutPrefix(pt, pendingTOTPPrefix)
	if !ok {
		return "", time.Time{}, errNoPendingTOTP
	}
	ms, secret, ok := strings.Cut(rest, "|")
	if !ok || secret == "" {
		return "", time.Time{}, errNoPendingTOTP
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return "", time.Time{}, errNoPendingTOTP
	}
	return secret, time.UnixMilli(n), nil
}
