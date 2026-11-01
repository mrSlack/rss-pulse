-- Accounts are disabled, never deleted, so their history and shared sources stay intact.
-- A disabled account cannot sign in, and its sessions are revoked when it is disabled.
ALTER TABLE users
    ADD COLUMN disabled_at TIMESTAMPTZ NULL,
    ADD COLUMN disabled_by_user_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL;
