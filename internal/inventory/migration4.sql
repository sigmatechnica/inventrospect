ALTER TABLE items ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
CREATE TABLE idempotency (
 principal TEXT NOT NULL, operation TEXT NOT NULL, request_id TEXT NOT NULL,
 request_hash TEXT NOT NULL, result TEXT NOT NULL, created_at INTEGER NOT NULL,
 PRIMARY KEY(principal,operation,request_id)
);
CREATE INDEX idempotency_age ON idempotency(created_at);
CREATE TABLE item_sequence (id INTEGER PRIMARY KEY CHECK(id=1), next_id INTEGER NOT NULL);
INSERT INTO item_sequence SELECT 1,COALESCE(MAX(id),0)+1 FROM items;
CREATE TABLE deleted_items (id INTEGER PRIMARY KEY, deleted_at TEXT NOT NULL, actor_id INTEGER NOT NULL REFERENCES users(id), token_id INTEGER REFERENCES api_tokens(id));
CREATE TABLE item_deletion_guard (item_id INTEGER PRIMARY KEY);
DROP TRIGGER stock_no_delete;
CREATE TRIGGER stock_no_delete BEFORE DELETE ON stock_movements
 WHEN NOT EXISTS(SELECT 1 FROM item_deletion_guard WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'stock history is immutable except during authorized item deletion'); END;
CREATE TABLE document_cleanup (storage_key TEXT PRIMARY KEY, queued_at TEXT NOT NULL);
CREATE TABLE resource_sequences (kind TEXT PRIMARY KEY, next_id INTEGER NOT NULL);
INSERT INTO resource_sequences SELECT 'documents',COALESCE(MAX(id),0)+1 FROM documents;
INSERT INTO resource_sequences SELECT 'stock_movements',COALESCE(MAX(id),0)+1 FROM stock_movements;
