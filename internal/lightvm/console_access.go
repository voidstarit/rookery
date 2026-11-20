package lightvm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// ConsoleAccessTokenPrefix marks a bearer token issued for a LightVM console
// WebSocket. The prefix is not a secret; it only makes tokens recognizable.
const ConsoleAccessTokenPrefix = "lvc_"

const consoleAccessTokenBytes = 32

// NewConsoleAccessToken returns a new random console-access bearer token.
func NewConsoleAccessToken() (string, error) {
	raw := make([]byte, consoleAccessTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return ConsoleAccessTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashConsoleAccessToken returns the value stored in the database for a
// console-access token. Only the hash is persisted, so a database leak does
// not expose usable console credentials.
func HashConsoleAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidConsoleAccessToken reports whether token has the expected format.
func ValidConsoleAccessToken(token string) bool {
	if !strings.HasPrefix(token, ConsoleAccessTokenPrefix) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, ConsoleAccessTokenPrefix))
	return err == nil && len(raw) == consoleAccessTokenBytes
}

// ConsoleAccessPath is the absolute WebSocket path for a console-access token.
func ConsoleAccessPath(token string) string {
	return "/ws/lightvm-console/" + token
}
