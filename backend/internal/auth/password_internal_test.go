package auth

import (
	"runtime"
	"sync"
	"testing"
)

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

func TestArgon2WorkIsBoundedBySemaphore(t *testing.T) {
	if cap(argonSlots) < 1 || cap(argonSlots) != max(1, runtime.GOMAXPROCS(0)) {
		t.Fatalf("semaphore capacity = %d, want GOMAXPROCS (min 1)", cap(argonSlots))
	}
	originalSlots, originalKey := argonSlots, argonKey
	t.Cleanup(func() { argonSlots, argonKey = originalSlots, originalKey })
	argonSlots = make(chan struct{}, 2)
	var mu sync.Mutex
	active, peak, calls := 0, 0, 0
	argonKey = func(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
		mu.Lock()
		active++
		calls++
		peak = max(peak, active)
		mu.Unlock()
		runtime.Gosched()
		mu.Lock()
		active--
		mu.Unlock()
		return make([]byte, keyLen)
	}
	encoded, err := HashPassword("a-long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(2)
		go func() { defer wg.Done(); VerifyPassword(encoded, "a-long-test-password") }()
		go func() { defer wg.Done(); _, _ = HashPassword("another-password") }()
	}
	wg.Wait()
	if calls != 65 {
		t.Fatalf("Argon2id calls = %d, want 65 (all through the bounded path)", calls)
	}
	if peak > 2 {
		t.Fatalf("peak concurrent Argon2id computations = %d, want at most 2", peak)
	}
}
