-- escopo de token (TSD §13.2) e marca persistente de crash loop (§8.2.5: sair de `crashed` exige ação explícita)
ALTER TABLE api_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'full';
ALTER TABLE apps ADD COLUMN crashed_at INTEGER;
