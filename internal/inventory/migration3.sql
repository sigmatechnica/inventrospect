CREATE TABLE api_tokens (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL,
 prefix TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE,
 user_id INTEGER NOT NULL REFERENCES users(id),
 scopes TEXT NOT NULL CHECK(json_valid(scopes)),
 created_at TEXT NOT NULL,
 expires_at TEXT,
 revoked_at TEXT,
 last_used_at TEXT
);
ALTER TABLE audit_events ADD COLUMN token_id INTEGER REFERENCES api_tokens(id);
ALTER TABLE stock_movements ADD COLUMN token_id INTEGER REFERENCES api_tokens(id);
CREATE INDEX stock_history_page ON stock_movements(item_id,id DESC);
