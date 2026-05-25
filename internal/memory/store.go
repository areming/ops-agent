// Package memory persists the agent's audit trail (and, in later
// milestones, session history). It uses a pure-Go SQLite driver so the
// agent stays a single static binary.
package memory

import (
	"database/sql"
	"os"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies
// the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing database without migrating, so a viewer
// (e.g. the operator running `logs`/`todos` with only group-read access to
// the agent's DB) never attempts a write. It returns os.ErrNotExist if the
// database file is absent.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS audit (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    source         TEXT    NOT NULL,
    command        TEXT    NOT NULL,
    risk           TEXT,
    reversible     INTEGER,
    decision       TEXT,
    exit_code      INTEGER,
    output_excerpt TEXT,
    created_at     TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_calls   TEXT,
    tool_call_id TEXT,
    reasoning    TEXT,
    created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS todos (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    source           TEXT NOT NULL,
    severity         TEXT,
    title            TEXT NOT NULL,
    detail           TEXT,
    suggested_action TEXT,
    status           TEXT NOT NULL DEFAULT 'open',
    created_at       TEXT NOT NULL
)`)
	return err
}
