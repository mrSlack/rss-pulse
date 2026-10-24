package store

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type User struct {
	ID                     int64  `json:"id"`
	Email                  string `json:"email"`
	Role                   string `json:"role"`
	RefreshIntervalMinutes *int   `json:"refreshIntervalMinutes"`
	FirstName              string `json:"firstName"`
	LastName               string `json:"lastName"`
	Nickname               string `json:"nickname"`
}

type AdminSettings struct {
	RefreshIntervalMinutes int `json:"refreshIntervalMinutes"`
	SharedFeedTTLMinutes   int `json:"sharedFeedTTLMinutes"`
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}

func (s *Store) UserCredentialByEmail(ctx context.Context, email string) (User, string, error) {
	var user User
	var passwordHash string
	err := s.pool.QueryRow(ctx, `SELECT id,email,role,refresh_interval_minutes,first_name,last_name,nickname,password_hash FROM users WHERE LOWER(email)=$1`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&user.ID, &user.Email, &user.Role, &user.RefreshIntervalMinutes, &user.FirstName, &user.LastName, &user.Nickname, &passwordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return user, passwordHash, err
}

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, tokenHash, userID, expiresAt)
	return err
}

func (s *Store) UserForSession(ctx context.Context, tokenHash string) (User, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`); err != nil {
		return User{}, err
	}
	var user User
	err := s.pool.QueryRow(ctx, `SELECT u.id,u.email,u.role,u.refresh_interval_minutes,u.first_name,u.last_name,u.nickname FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>NOW()`, tokenHash).
		Scan(&user.ID, &user.Email, &user.Role, &user.RefreshIntervalMinutes, &user.FirstName, &user.LastName, &user.Nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) UpdateUserProfile(ctx context.Context, userID int64, firstName, lastName, nickname string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `UPDATE users SET first_name=$2,last_name=$3,nickname=$4,updated_at=NOW() WHERE id=$1
		RETURNING id,email,role,refresh_interval_minutes,first_name,last_name,nickname`, userID, firstName, lastName, nickname).
		Scan(&user.ID, &user.Email, &user.Role, &user.RefreshIntervalMinutes, &user.FirstName, &user.LastName, &user.Nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *Store) ChangeUserPassword(ctx context.Context, userID int64, passwordHash, currentSessionHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=NOW() WHERE id=$1`, userID, passwordHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, userID, currentSessionHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,email,role,refresh_interval_minutes FROM users ORDER BY LOWER(email),id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.Role, &user.RefreshIntervalMinutes); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `INSERT INTO users (email,password_hash,role) VALUES ($1,$2,$3) RETURNING id,email,role,refresh_interval_minutes`, strings.ToLower(strings.TrimSpace(email)), passwordHash, role).
		Scan(&user.ID, &user.Email, &user.Role, &user.RefreshIntervalMinutes)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return User{}, ErrDuplicate
		}
	}
	return user, err
}

func (s *Store) UpdateUserRole(ctx context.Context, userID int64, role string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102403)`); err != nil {
		return err
	}
	var currentRole string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&currentRole); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if currentRole == "Administrator" && role != "Administrator" {
		var administrators int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='Administrator'`).Scan(&administrators); err != nil {
			return err
		}
		if administrators <= 1 {
			return ErrLastAdministrator
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET role=$2,updated_at=NOW() WHERE id=$1`, userID, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateUserAdmin(ctx context.Context, userID int64, role *string, updateInterval bool, interval *int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026102403)`); err != nil {
		return err
	}
	var currentRole string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&currentRole); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if role != nil && currentRole == "Administrator" && *role != "Administrator" {
		var administrators int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='Administrator'`).Scan(&administrators); err != nil {
			return err
		}
		if administrators <= 1 {
			return ErrLastAdministrator
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET
		role=COALESCE($2,role),
		refresh_interval_minutes=CASE WHEN $3 THEN $4 ELSE refresh_interval_minutes END,
		updated_at=NOW() WHERE id=$1`, userID, role, updateInterval, interval); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateUserRefreshInterval(ctx context.Context, userID int64, interval *int) error {
	command, err := s.pool.Exec(ctx, `UPDATE users SET refresh_interval_minutes=$2,updated_at=NOW() WHERE id=$1`, userID, interval)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetAdminSettings(ctx context.Context) (AdminSettings, error) {
	var settings AdminSettings
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT value FROM system_settings WHERE key='refresh_interval_minutes'),
		(SELECT value FROM system_settings WHERE key='shared_feed_ttl_minutes')`).
		Scan(&settings.RefreshIntervalMinutes, &settings.SharedFeedTTLMinutes)
	return settings, err
}

func (s *Store) UpdateAdminSettings(ctx context.Context, settings AdminSettings) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE system_settings SET value=$1,updated_at=NOW() WHERE key='refresh_interval_minutes'`, settings.RefreshIntervalMinutes); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE system_settings SET value=$1,updated_at=NOW() WHERE key='shared_feed_ttl_minutes'`, settings.SharedFeedTTLMinutes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
