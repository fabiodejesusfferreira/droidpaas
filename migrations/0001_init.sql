CREATE TABLE schema_migrations (
  version     INTEGER PRIMARY KEY,
  applied_at  INTEGER NOT NULL
);

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,                 -- argon2id (PHC string)
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,               -- SHA-256 do token do cookie
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_secret  TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  user_agent   TEXT,
  ip           TEXT
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,           -- SHA-256 do token
  prefix       TEXT NOT NULL,                  -- primeiros chars, p/ identificar na UI
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  revoked_at   INTEGER
);

CREATE TABLE ssh_keys (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  public_key  TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  created_at  INTEGER NOT NULL
);

CREATE TABLE apps (
  id                 INTEGER PRIMARY KEY,
  name               TEXT NOT NULL UNIQUE,     -- ^[a-z][a-z0-9-]{1,30}$
  runtime            TEXT NOT NULL DEFAULT 'auto',  -- auto|node|python|go|static|custom
  port               INTEGER UNIQUE,           -- NULL para static
  branch             TEXT NOT NULL DEFAULT 'main',
  build_cmd          TEXT,
  start_cmd          TEXT,
  health_path        TEXT,
  health_timeout_s   INTEGER NOT NULL DEFAULT 30,
  static_dir         TEXT,
  nice               INTEGER NOT NULL DEFAULT 10,
  desired_state      TEXT NOT NULL DEFAULT 'running',  -- running|stopped
  current_release_id INTEGER REFERENCES releases(id) ON DELETE SET NULL,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
);

CREATE TABLE env_vars (
  app_id    INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  key       TEXT NOT NULL,                     -- ^[A-Za-z_][A-Za-z0-9_]*$
  value_enc BLOB NOT NULL,                     -- AES-256-GCM (nonce||ciphertext)
  is_secret INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (app_id, key)
);

CREATE TABLE domains (
  id         INTEGER PRIMARY KEY,
  app_id     INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  hostname   TEXT NOT NULL UNIQUE,
  is_primary INTEGER NOT NULL DEFAULT 0,
  auto       INTEGER NOT NULL DEFAULT 0,       -- 1 = <app>.<base_domain>
  created_at INTEGER NOT NULL
);

CREATE TABLE releases (
  id         INTEGER PRIMARY KEY,
  app_id     INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  git_sha    TEXT NOT NULL,
  git_ref    TEXT NOT NULL,
  message    TEXT,
  status     TEXT NOT NULL,                    -- building|ready|failed|pruned
  path       TEXT NOT NULL,
  size_bytes INTEGER,
  created_at INTEGER NOT NULL
);

CREATE TABLE deploys (
  id          INTEGER PRIMARY KEY,
  app_id      INTEGER NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  release_id  INTEGER REFERENCES releases(id) ON DELETE SET NULL,
  trigger     TEXT NOT NULL,                   -- git_push|manual|rollback|rebuild
  status      TEXT NOT NULL,                   -- ver §7.2
  stage       TEXT,
  error       TEXT,
  log_path    TEXT NOT NULL,
  queued_at   INTEGER NOT NULL,
  started_at  INTEGER,
  finished_at INTEGER
);

CREATE TABLE events (
  id      INTEGER PRIMARY KEY,
  ts      INTEGER NOT NULL,
  level   TEXT NOT NULL,                       -- info|warn|error
  kind    TEXT NOT NULL,                       -- ex.: deploy.succeeded
  app_id  INTEGER REFERENCES apps(id) ON DELETE SET NULL,
  message TEXT NOT NULL,
  data    TEXT                                 -- JSON
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL                          -- segredos cifrados com a master key
);

CREATE INDEX idx_releases_app   ON releases(app_id, created_at DESC);
CREATE INDEX idx_deploys_app    ON deploys(app_id, queued_at DESC);
CREATE INDEX idx_events_ts      ON events(ts DESC);
CREATE INDEX idx_sessions_exp   ON sessions(expires_at);
