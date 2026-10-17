// Package artifacts stores the enricher's about text, security file, search
// vector, icon, and publisher pictures in data/artifacts.db.
package artifacts

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS app (
  id TEXT PRIMARY KEY,
  about TEXT NOT NULL DEFAULT '',
  security TEXT NOT NULL DEFAULT '',
  vector BLOB,
  icon BLOB,
  version INTEGER NOT NULL DEFAULT 0,
  apk TEXT NOT NULL DEFAULT '',
  feature TEXT NOT NULL DEFAULT '',
  facts TEXT NOT NULL DEFAULT '',
  icon_from TEXT NOT NULL DEFAULT '',
  about_hash TEXT NOT NULL DEFAULT '',
  queries TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS avatar (
  pubkey TEXT PRIMARY KEY,
  picture TEXT NOT NULL DEFAULT '',
  webp BLOB
);
CREATE TABLE IF NOT EXISTS cache (
  kind TEXT NOT NULL,
  key TEXT NOT NULL,
  value BLOB NOT NULL,
  at INTEGER NOT NULL,
  PRIMARY KEY (kind, key)
);
`

// Path is data/artifacts.db.
func Path(data string) string {
	return filepath.Join(data, "artifacts.db")
}

// Memo is the enricher cache for one app. Hashes are hex SHA-256.
type Memo struct {
	Version int
	APK     string
	Feature string
	Facts   string
	Icon    string
	About   string
}

// App is one app's stored artifacts. Present is false when the row is missing.
type App struct {
	About    string
	Security string
	Vector   []byte
	Icon     []byte
	Queries  string
	Memo     Memo
	Present  bool
}

// Avatar is a publisher picture and the URL it was fetched from.
type Avatar struct {
	Picture string
	WebP    []byte
}

// DB is one artifacts.db connection.
type DB struct {
	sql *sql.DB
}

// Open creates the database when it is missing and applies the schema.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_fk=1")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := prepare(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{sql: db}, nil
}

// OpenRead reads an existing database. It does not create the file or apply schema.
func OpenRead(ctx context.Context, path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_query_only=1&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &DB{sql: db}, nil
}

func prepare(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("artifacts schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE app ADD COLUMN queries TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("artifacts queries: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("artifacts wal: %w", err)
	}
	return nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

// IDs returns every app id, ascending.
func (db *DB) IDs(ctx context.Context) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id FROM app ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Load reads one app. Present is false when it has no row.
func (db *DB) Load(ctx context.Context, id string) (App, error) {
	if err := validID(id); err != nil {
		return App{}, err
	}
	var app App
	err := db.sql.QueryRowContext(ctx, `
		SELECT about, security, vector, icon, queries, version, apk, feature, facts, icon_from, about_hash
		FROM app WHERE id = ?`, id).Scan(
		&app.About, &app.Security, &app.Vector, &app.Icon, &app.Queries,
		&app.Memo.Version, &app.Memo.APK, &app.Memo.Feature, &app.Memo.Facts, &app.Memo.Icon, &app.Memo.About,
	)
	if err == sql.ErrNoRows {
		return App{}, nil
	}
	if err != nil {
		return App{}, err
	}
	app.Present = true
	return app, nil
}

// SetAbout stores trimmed text plus a trailing newline. An empty body writes nothing.
func (db *DB) SetAbout(ctx context.Context, id, body string) error {
	return db.setText(ctx, id, "about", body)
}

// SetSecurity stores trimmed text plus a trailing newline. An empty body writes nothing.
func (db *DB) SetSecurity(ctx context.Context, id, body string) error {
	return db.setText(ctx, id, "security", body)
}

// SetQueries stores likely-query lines. An empty body clears them.
func (db *DB) SetQueries(ctx context.Context, id, body string) error {
	body = strings.TrimSpace(body)
	if body != "" {
		body += "\n"
	}
	return db.set(ctx, id, "queries", body)
}

func (db *DB) setText(ctx context.Context, id, column, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	return db.set(ctx, id, column, body+"\n")
}

// SetVector stores the int8 vector. An empty slice writes nothing.
func (db *DB) SetVector(ctx context.Context, id string, vec []byte) error {
	if len(vec) == 0 {
		return nil
	}
	return db.set(ctx, id, "vector", vec)
}

// SetIcon stores the webp bytes. An empty slice writes nothing.
func (db *DB) SetIcon(ctx context.Context, id string, icon []byte) error {
	if len(icon) == 0 {
		return nil
	}
	return db.set(ctx, id, "icon", icon)
}

// SetMemo stores the enricher cache columns for one app.
func (db *DB) SetMemo(ctx context.Context, id string, m Memo) error {
	if err := validID(id); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO app (id, version, apk, feature, facts, icon_from, about_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			version = excluded.version,
			apk = excluded.apk,
			feature = excluded.feature,
			facts = excluded.facts,
			icon_from = excluded.icon_from,
			about_hash = excluded.about_hash`,
		id, m.Version, m.APK, m.Feature, m.Facts, m.Icon, m.About)
	return err
}

// Delete removes one app row. A missing row is not an error.
func (db *DB) Delete(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx, `DELETE FROM app WHERE id = ?`, id)
	return err
}

// Avatar reads one publisher picture. ok is false when the row is missing or has no bytes.
func (db *DB) Avatar(ctx context.Context, pubkey string) (Avatar, bool, error) {
	if err := validPubkey(pubkey); err != nil {
		return Avatar{}, false, err
	}
	var avatar Avatar
	err := db.sql.QueryRowContext(ctx, `SELECT picture, webp FROM avatar WHERE pubkey = ?`, pubkey).Scan(&avatar.Picture, &avatar.WebP)
	if err == sql.ErrNoRows || (err == nil && len(avatar.WebP) == 0) {
		return Avatar{}, false, nil
	}
	if err != nil {
		return Avatar{}, false, err
	}
	return avatar, true, nil
}

// SetAvatar stores the picture URL and webp bytes.
func (db *DB) SetAvatar(ctx context.Context, pubkey, picture string, webp []byte) error {
	if err := validPubkey(pubkey); err != nil {
		return err
	}
	if len(webp) == 0 {
		return nil
	}
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO avatar (pubkey, picture, webp) VALUES (?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET picture = excluded.picture, webp = excluded.webp`,
		pubkey, picture, webp)
	return err
}

// CacheGet reads an intermediate result of kind. A value older than maxAge
// is a miss. A zero maxAge never expires.
func (db *DB) CacheGet(ctx context.Context, kind, key string, maxAge time.Duration) ([]byte, bool, error) {
	var value []byte
	var at int64
	err := db.sql.QueryRowContext(ctx, `SELECT value, at FROM cache WHERE kind = ? AND key = ?`, kind, key).Scan(&value, &at)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if maxAge > 0 && time.Since(time.Unix(at, 0)) > maxAge {
		return nil, false, nil
	}
	return value, true, nil
}

// CachePut stores an intermediate result of kind.
func (db *DB) CachePut(ctx context.Context, kind, key string, value []byte) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO cache (kind, key, value, at) VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, key) DO UPDATE SET value = excluded.value, at = excluded.at`,
		kind, key, value, time.Now().Unix())
	return err
}

// CachePrune drops cached results written before cutoff.
func (db *DB) CachePrune(ctx context.Context, cutoff time.Time) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM cache WHERE at < ?`, cutoff.Unix())
	return err
}

func (db *DB) set(ctx context.Context, id, column string, value any) error {
	if err := validID(id); err != nil {
		return err
	}
	query := fmt.Sprintf(`
		INSERT INTO app (id, %s) VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET %s = excluded.%s`, column, column, column)
	_, err := db.sql.ExecContext(ctx, query, id, value)
	return err
}

func validID(id string) error {
	if id == "" || id != filepath.Base(id) || strings.Contains(id, "..") {
		return fmt.Errorf("app id %q", id)
	}
	return nil
}

func validPubkey(pubkey string) error {
	if len(pubkey) != 64 {
		return fmt.Errorf("avatar pubkey")
	}
	for _, c := range pubkey {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return fmt.Errorf("avatar pubkey")
		}
	}
	return nil
}
