package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	saltLength          = 16
)

// argonSlots bounds concurrent Argon2id computations. Each one allocates up to
// argonMemory KiB (64 MiB), so unbounded parallel sign-ins could exhaust API
// memory. Callers block until a slot is free; the wait is not cancellable by
// a request context, which is acceptable because each computation is short and
// sign-in attempts are rate limited before they reach this point.
var argonSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)))

// argonKey is the raw key derivation, replaceable in tests.
var argonKey = argon2.IDKey

func boundedArgon2ID(password, salt []byte, iterations, memory uint32, threads uint8, keyLen uint32) []byte {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	return argonKey(password, salt, iterations, memory, threads, keyLen)
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := boundedArgon2ID([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, encodedSalt, encodedHash), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	for _, parameter := range strings.Split(parts[3], ",") {
		keyValue := strings.SplitN(parameter, "=", 2)
		if len(keyValue) != 2 {
			return false
		}
		value, err := strconv.ParseUint(keyValue[1], 10, 32)
		if err != nil {
			return false
		}
		switch keyValue[0] {
		case "m":
			memory = uint32(value)
		case "t":
			iterations = uint32(value)
		case "p":
			if value > 255 {
				return false
			}
			parallelism = uint8(value)
		default:
			return false
		}
	}
	// Encoded hashes come from the database; cap their parameters so a corrupt
	// row cannot turn login into an unbounded memory or CPU allocation.
	if memory < 8*uint32(parallelism) || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 8 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false
	}
	actual := boundedArgon2ID([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

const dummyPassword = "pulse-missing-account-timing-equalizer"

var dummy struct {
	once sync.Once
	hash string
}

// dummyHash returns a valid Argon2id hash with production parameters, computed
// once per process.
func dummyHash() string {
	dummy.once.Do(func() {
		if hash, err := HashPassword(dummyPassword); err == nil {
			dummy.hash = hash
		}
	})
	return dummy.hash
}

// SimulatePasswordVerification performs the same Argon2id work as
// VerifyPassword for a sign-in attempt whose account does not exist, so the
// response time does not reveal whether an e-mail address is registered.
func SimulatePasswordVerification(password string) {
	VerifyPassword(dummyHash(), password)
}

func NewSessionToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func HashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
