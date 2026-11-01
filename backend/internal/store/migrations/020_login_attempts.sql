-- Sign-in rate limiting state, shared by every API instance and kept across restarts.
-- Each row is one attempt: kind 'ip' counts every attempt from a client address,
-- kind 'account' counts provisional or failed attempts for a normalized e-mail address.
-- key_hash is HMAC-SHA256 under login_rate_limit_secret, so neither e-mail addresses
-- (which may be typos or misplaced passwords) nor client addresses are stored in clear.
CREATE TABLE IF NOT EXISTS login_rate_limit_secret (
  id smallint PRIMARY KEY CHECK (id = 1),
  secret bytea NOT NULL CHECK (length(secret) = 32)
);

-- gen_random_uuid() uses the server's strong random source; two UUIDs give 32 bytes
-- with 244 random bits.
INSERT INTO login_rate_limit_secret (id, secret)
VALUES (1, decode(replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''), 'hex'))
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS login_attempts (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('ip', 'account')),
  key_hash bytea NOT NULL CHECK (length(key_hash) = 32),
  attempted_at timestamptz NOT NULL
);

-- The limit check reads the newest attempts of one key; the sweep deletes by age.
CREATE INDEX IF NOT EXISTS login_attempts_key_idx ON login_attempts (kind, key_hash, attempted_at DESC);
CREATE INDEX IF NOT EXISTS login_attempts_attempted_at_idx ON login_attempts (attempted_at);
