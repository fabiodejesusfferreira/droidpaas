-- runtime resolvido (node|python|go|static|custom) da release, usado para NODE_ENV, start e rollback
ALTER TABLE releases ADD COLUMN runtime TEXT NOT NULL DEFAULT '';
