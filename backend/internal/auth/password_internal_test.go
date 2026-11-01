package auth

import "testing"

// The dummy hash must be a well-formed Argon2id hash with production
// parameters; otherwise VerifyPassword would return early and a missing
// account would be cheaper to check than an existing one.
func TestDummyHashRunsFullPasswordVerification(t *testing.T) {
	if !VerifyPassword(dummyHash(), dummyPassword) {
		t.Fatal("dummy hash does not verify its own password; the full Argon2id path is not exercised")
	}
	if VerifyPassword(dummyHash(), "some-other-password") {
		t.Fatal("dummy hash verified an unrelated password")
	}
	if dummyHash() != dummyHash() {
		t.Fatal("dummy hash must be computed once and reused")
	}
}
