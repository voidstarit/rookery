package lightvm

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestConsoleAccessTokens(t *testing.T) {
	token, err := NewConsoleAccessToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if !strings.HasPrefix(token, ConsoleAccessTokenPrefix) {
		t.Fatalf("token = %q", token)
	}
	if !ValidConsoleAccessToken(token) {
		t.Fatalf("token %q should be valid", token)
	}
	if ValidConsoleAccessToken("") || ValidConsoleAccessToken("nope") {
		t.Fatal("short tokens should be invalid")
	}
	short := ConsoleAccessTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte("short"))
	if ValidConsoleAccessToken(short) {
		t.Fatal("wrong-length token should be invalid")
	}
	other, err := NewConsoleAccessToken()
	if err != nil {
		t.Fatalf("new second token: %v", err)
	}
	if token == other {
		t.Fatal("tokens should be unique")
	}
	if HashConsoleAccessToken(token) == token || len(HashConsoleAccessToken(token)) != 64 {
		t.Fatalf("hash = %q", HashConsoleAccessToken(token))
	}
	firstHash := HashConsoleAccessToken(token)
	secondHash := HashConsoleAccessToken(token)
	if firstHash != secondHash {
		t.Fatal("hash should be deterministic")
	}
	if got := ConsoleAccessPath(token); got != "/ws/lightvm-console/"+token {
		t.Fatalf("path = %q", got)
	}
}
