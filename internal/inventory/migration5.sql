ALTER TABLE audit_events ADD COLUMN details TEXT NOT NULL DEFAULT '{}';
ALTER TABLE api_tokens ADD COLUMN write_units_per_minute INTEGER NOT NULL DEFAULT 600 CHECK(write_units_per_minute BETWEEN 0 AND 1000000);
ALTER TABLE api_tokens ADD COLUMN bulk_record_limit INTEGER NOT NULL DEFAULT 100 CHECK(bulk_record_limit BETWEEN 1 AND 100);
CREATE INDEX idempotency_principal_age ON idempotency(principal,created_at);
