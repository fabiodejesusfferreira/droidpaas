-- o deploy precisa carregar o que vai construir enquanto espera na fila (o DDL v0.1 só tinha release_id, criado depois)
ALTER TABLE deploys ADD COLUMN git_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE deploys ADD COLUMN git_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE deploys ADD COLUMN rebuild INTEGER NOT NULL DEFAULT 0;
