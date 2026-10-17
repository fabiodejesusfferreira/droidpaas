-- app publicado a partir de um repositório do GitHub (webhook + deploy key opcional); sem linha = só git push
CREATE TABLE github_sources (
  app_id       INTEGER PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  url          TEXT NOT NULL,
  branch       TEXT NOT NULL,
  secret       BLOB NOT NULL,            -- segredo do webhook, cifrado com a master.key
  deploy_key   BLOB,                     -- chave privada ed25519 (cifrada); NULL em repositório público
  deploy_pub   TEXT NOT NULL DEFAULT '',
  last_sync_at INTEGER,
  last_error   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL
);
