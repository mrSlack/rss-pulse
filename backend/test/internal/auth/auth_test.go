package auth_test

import (
	"strings"
	"testing"

	"github.com/pulse-reader/app/internal/auth"
)

func TestHashPasswordUsesArgon2idAndVerifiesPassword(t *testing.T) {
	hash, err := auth.HashPassword("a-long-test-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash = %q, want Argon2id encoding", hash)
	}
	if !auth.VerifyPassword(hash, "a-long-test-password") {
		t.Fatal("VerifyPassword() rejected matching password")
	}
	if auth.VerifyPassword(hash, "wrong-password") {
		t.Fatal("VerifyPassword() accepted a wrong password")
	}
}

func TestSessionTokensAreOpaqueRandomAndHashed(t *testing.T) {
	first, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) < 40 {
		t.Fatalf("tokens must be distinct random opaque values; lengths %d and %d", len(first), len(second))
	}
	if auth.HashSessionToken(first) == first || auth.HashSessionToken(first) == auth.HashSessionToken(second) {
		t.Fatal("session token hash must differ from and uniquely represent the raw token")
	}
}
