package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

// loginAttemptLockClass is the first key of the two-key advisory locks that
// serialize limit checks per rate-limit key (the second key is derived from
// the key hash). Two-key advisory locks do not collide with the single bigint
// keys used elsewhere.
const loginAttemptLockClass int32 = 2026110120

const (
	loginKindIP      = "ip"
	loginKindAccount = "account"
)

// LoginThrottle holds the sign-in limits: failed attempts per account and
// attempts of any outcome per client IP, each within a rolling window.
type LoginThrottle struct {
	AccountFailureLimit  int
	AccountFailureWindow time.Duration
	IPAttemptLimit       int
	IPAttemptWindow      time.Duration
}

// LoginAttempt identifies the provisional account failure recorded by
// BeginLoginAttempt.
type LoginAttempt struct {
	accountRowID int64
	accountHash  []byte
}

// loginKeyHash returns HMAC-SHA256 of kind and key under the installation's
// secret, so the table holds neither e-mail addresses nor client addresses.
func (s *Store) loginKeyHash(ctx context.Context, kind, key string) ([]byte, error) {
	secret := s.loginSecret.Load()
	if secret == nil {
		var value []byte
		if err := s.pool.QueryRow(ctx, `SELECT secret FROM login_rate_limit_secret WHERE id = 1`).Scan(&value); err != nil {
			return nil, err
		}
		if len(value) != sha256.Size {
			return nil, errors.New("login rate limit secret has an invalid length")
		}
		s.loginSecret.Store(&value)
		secret = &value
	}
	mac := hmac.New(sha256.New, *secret)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(key))
	return mac.Sum(nil), nil
}

// BeginLoginAttempt checks both limits for a sign-in attempt and, when it is
// allowed, records it atomically: one attempt for the IP and one provisional
// failure for the account. A positive wait means the attempt is refused and
// nothing was recorded. Concurrent calls for the same IP or account, from any
// API instance, are serialized by transaction-scoped advisory locks, so they
// cannot all pass the check before any of them is recorded.
func (s *Store) BeginLoginAttempt(ctx context.Context, throttle LoginThrottle, ip, account string, now time.Time) (LoginAttempt, time.Duration, error) {
	ipHash, err := s.loginKeyHash(ctx, loginKindIP, ip)
	if err != nil {
		return LoginAttempt{}, 0, err
	}
	accountHash, err := s.loginKeyHash(ctx, loginKindAccount, account)
	if err != nil {
		return LoginAttempt{}, 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginAttempt{}, 0, err
	}
	defer tx.Rollback(ctx)
	// Lock in ascending order so two transactions never wait for each other in a cycle.
	first, second := loginLockKey(ipHash), loginLockKey(accountHash)
	if first > second {
		first, second = second, first
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, loginAttemptLockClass, first); err != nil {
		return LoginAttempt{}, 0, err
	}
	if second != first {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, loginAttemptLockClass, second); err != nil {
			return LoginAttempt{}, 0, err
		}
	}
	// Each subquery returns the limit-th newest attempt inside the window, if
	// any: the key is at its limit until that attempt leaves the window.
	var ipOldest, accountOldest *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
		  (SELECT attempted_at FROM login_attempts
		   WHERE kind = 'ip' AND key_hash = $1 AND attempted_at > $2
		   ORDER BY attempted_at DESC OFFSET $3 LIMIT 1),
		  (SELECT attempted_at FROM login_attempts
		   WHERE kind = 'account' AND key_hash = $4 AND attempted_at > $5
		   ORDER BY attempted_at DESC OFFSET $6 LIMIT 1)`,
		ipHash, now.Add(-throttle.IPAttemptWindow), throttle.IPAttemptLimit-1,
		accountHash, now.Add(-throttle.AccountFailureWindow), throttle.AccountFailureLimit-1,
	).Scan(&ipOldest, &accountOldest); err != nil {
		return LoginAttempt{}, 0, err
	}
	var wait time.Duration
	if ipOldest != nil {
		wait = max(wait, ipOldest.Add(throttle.IPAttemptWindow).Sub(now))
	}
	if accountOldest != nil {
		wait = max(wait, accountOldest.Add(throttle.AccountFailureWindow).Sub(now))
	}
	if wait > 0 {
		return LoginAttempt{}, wait, nil
	}
	attempt := LoginAttempt{accountHash: accountHash}
	if err := tx.QueryRow(ctx, `
		WITH ip_attempt AS (
		  INSERT INTO login_attempts (kind, key_hash, attempted_at) VALUES ('ip', $1, $3)
		)
		INSERT INTO login_attempts (kind, key_hash, attempted_at) VALUES ('account', $2, $3)
		RETURNING id`, ipHash, accountHash, now).Scan(&attempt.accountRowID); err != nil {
		return LoginAttempt{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginAttempt{}, 0, err
	}
	return attempt, 0, nil
}

// ClearLoginFailures removes every recorded failure of the attempt's account
// after a correct password. The IP's attempts remain counted.
func (s *Store) ClearLoginFailures(ctx context.Context, attempt LoginAttempt) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM login_attempts WHERE kind = 'account' AND key_hash = $1`, attempt.accountHash)
	return err
}

// CancelLoginAttempt withdraws the provisional account failure, for outcomes
// that are not credential failures (for example a server error).
func (s *Store) CancelLoginAttempt(ctx context.Context, attempt LoginAttempt) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM login_attempts WHERE id = $1`, attempt.accountRowID)
	return err
}

// DeleteExpiredLoginAttempts removes attempts that have left their window and
// therefore no longer affect any limit.
func (s *Store) DeleteExpiredLoginAttempts(ctx context.Context, throttle LoginThrottle, now time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, `
		DELETE FROM login_attempts
		WHERE (kind = 'ip' AND attempted_at <= $1) OR (kind = 'account' AND attempted_at <= $2)`,
		now.Add(-throttle.IPAttemptWindow), now.Add(-throttle.AccountFailureWindow))
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

func loginLockKey(hash []byte) int32 {
	return int32(binary.BigEndian.Uint32(hash))
}
