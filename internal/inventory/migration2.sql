ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'light' CHECK(theme IN ('light','dark'));
CREATE TABLE inventory_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 default_location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL
);
INSERT INTO inventory_settings(id) VALUES(1);
