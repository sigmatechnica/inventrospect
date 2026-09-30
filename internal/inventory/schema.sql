CREATE TABLE users (
 id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
 password_hash BLOB NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','editor','viewer')),
 disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN (0,1)), created_at TEXT NOT NULL
);
CREATE TABLE sessions (
 token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), expires_at TEXT NOT NULL
);
CREATE TABLE categories (id INTEGER PRIMARY KEY, name TEXT NOT NULL CHECK(length(trim(name))>0), parent_id INTEGER REFERENCES categories(id), CHECK(parent_id IS NULL OR parent_id != id));
CREATE TABLE locations (id INTEGER PRIMARY KEY, name TEXT NOT NULL CHECK(length(trim(name))>0), parent_id INTEGER REFERENCES locations(id), CHECK(parent_id IS NULL OR parent_id != id));
CREATE TABLE items (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL CHECK(length(trim(name))>0), description TEXT NOT NULL DEFAULT '',
 category_id INTEGER REFERENCES categories(id), manufacturer TEXT NOT NULL DEFAULT '', mpn TEXT NOT NULL DEFAULT '',
 condition TEXT NOT NULL DEFAULT '', notes TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),
 attributes TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(attributes)), archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, created_by INTEGER NOT NULL REFERENCES users(id), updated_by INTEGER NOT NULL REFERENCES users(id)
);
CREATE INDEX items_category ON items(category_id);
CREATE TABLE stock_movements (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), location_id INTEGER NOT NULL REFERENCES locations(id),
 quantity INTEGER NOT NULL CHECK(typeof(quantity)='integer' AND quantity != 0 AND abs(quantity)<=1000000000000000), reason TEXT NOT NULL CHECK(length(trim(reason))>0),
 transfer_id TEXT, actor_id INTEGER NOT NULL REFERENCES users(id), created_at TEXT NOT NULL
);
CREATE INDEX stock_item_location ON stock_movements(item_id, location_id);
CREATE INDEX stock_location ON stock_movements(location_id,item_id);
CREATE TRIGGER stock_no_update BEFORE UPDATE ON stock_movements BEGIN SELECT RAISE(ABORT,'stock history is immutable'); END;
CREATE TRIGGER stock_no_delete BEFORE DELETE ON stock_movements BEGIN SELECT RAISE(ABORT,'stock history is immutable'); END;
CREATE TABLE documents (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), title TEXT NOT NULL, filename TEXT NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('datasheet','manual','drawing','application_note','photo','other')),
 source_url TEXT NOT NULL DEFAULT '', manufacturer TEXT NOT NULL DEFAULT '', revision TEXT NOT NULL DEFAULT '',
 sha256 TEXT NOT NULL, size INTEGER NOT NULL, storage_key TEXT NOT NULL UNIQUE, uploaded_at TEXT NOT NULL,
 uploaded_by INTEGER NOT NULL REFERENCES users(id)
);
CREATE INDEX documents_item ON documents(item_id);
CREATE TABLE audit_events (
 id INTEGER PRIMARY KEY, actor_id INTEGER NOT NULL REFERENCES users(id), action TEXT NOT NULL,
 resource TEXT NOT NULL, resource_id INTEGER NOT NULL, created_at TEXT NOT NULL
);
