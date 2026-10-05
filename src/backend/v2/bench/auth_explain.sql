\set ON_ERROR_STOP on

-- Disposable target-gate fixture. Never run against production data.
TRUNCATE user_sessions, invitations, user_credentials, user_roles, user_identities, users
RESTART IDENTITY CASCADE;

INSERT INTO users (username)
SELECT 'auth-bench-user-' || g
FROM generate_series(1, 100000) AS g;

INSERT INTO user_credentials (user_id, kind, secret_hash)
SELECT g, 0, 'auth-bench-verifier'
FROM generate_series(1, 100000) AS g;

INSERT INTO invitations (token_hash, created_by_user_id, expires_at)
SELECT decode(md5('invite-a-' || g::text) || md5('invite-b-' || g::text), 'hex'),
       ((g - 1) % 100000) + 1,
       now() + interval '30 days'
FROM generate_series(1, 100000) AS g;

INSERT INTO user_sessions (token_hash, user_id, created_at, expires_at)
SELECT decode(md5('session-a-' || g::text) || md5('session-b-' || g::text), 'hex'),
       ((g - 1) % 100000) + 1,
       now() - (500000 - g) * interval '1 second',
       now() + interval '30 days'
FROM generate_series(1, 500000) AS g;

ANALYZE users;
ANALYZE user_credentials;
ANALYZE invitations;
ANALYZE user_sessions;

\echo '=== session token-hash -> active user ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT u.id, u.username
FROM user_sessions AS s
JOIN users AS u ON u.id = s.user_id
WHERE s.token_hash = decode(md5('session-a-250000') || md5('session-b-250000'), 'hex')
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.status = 0;

\echo '=== case-insensitive username -> password credential ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT u.id, u.username, u.status, c.secret_hash
FROM users AS u
JOIN user_credentials AS c
  ON c.user_id = u.id
 AND c.kind = 0
WHERE lower(u.username) = lower('AUTH-BENCH-USER-50000');

\echo '=== invitation token-hash availability ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT true
FROM invitations
WHERE token_hash = decode(md5('invite-a-50000') || md5('invite-b-50000'), 'hex')
  AND revoked_at IS NULL
  AND claimed_by_user_id IS NULL
  AND claimed_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

\echo '=== invitation token-hash registration lock ==='
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
SELECT id
FROM invitations
WHERE token_hash = decode(md5('invite-a-50000') || md5('invite-b-50000'), 'hex')
  AND revoked_at IS NULL
  AND claimed_by_user_id IS NULL
  AND claimed_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
FOR UPDATE;
ROLLBACK;
