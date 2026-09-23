// Package storage keeps races and their track points in SQLite.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Race struct {
	ID          int64
	UserID      int64
	ChatID      int64
	MessageID   int
	StartedAt   time.Time
	LastPointAt time.Time
	LiveUntil   *time.Time // nil = indefinite live period
	FinishedAt  *time.Time // nil = still active
	Public      bool       // visible to everyone, not only the owner
}

func (r *Race) Active() bool { return r.FinishedAt == nil }

type Point struct {
	Lat, Lon float64
	Time     time.Time
	Accuracy float64
}

type Store struct {
	db *sql.DB
}

// migrations[i] upgrades the schema from version i to i+1 (PRAGMA user_version).
// Append new steps; never edit the ones already deployed.
var migrations = []string{
	// v1: initial schema. IF NOT EXISTS because databases created before
	// versioning already have these tables at user_version 0.
	`
CREATE TABLE IF NOT EXISTS races (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL,
  chat_id       INTEGER NOT NULL,
  message_id    INTEGER NOT NULL,
  started_at    INTEGER NOT NULL,
  last_point_at INTEGER NOT NULL,
  live_until    INTEGER,
  finished_at   INTEGER,
  UNIQUE (chat_id, message_id)
);
CREATE INDEX IF NOT EXISTS races_user ON races(user_id, started_at DESC);

CREATE TABLE IF NOT EXISTS points (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  race_id   INTEGER NOT NULL REFERENCES races(id) ON DELETE CASCADE,
  lat       REAL NOT NULL,
  lon       REAL NOT NULL,
  ts        INTEGER NOT NULL,
  accuracy  REAL
);
CREATE INDEX IF NOT EXISTS points_race ON points(race_id, ts);
`,
	// v2: race visibility.
	`ALTER TABLE races ADD COLUMN public INTEGER NOT NULL DEFAULT 0`,
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for v := version; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("v%d: %w", v+1, err)
		}
		// PRAGMA does not accept placeholders; v is an int we control.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

const raceCols = `id, user_id, chat_id, message_id, started_at, last_point_at, live_until, finished_at, public`

type scanner interface{ Scan(dest ...any) error }

func scanRace(row scanner) (*Race, error) {
	var (
		r                     Race
		started, last         int64
		liveUntil, finishedAt sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.UserID, &r.ChatID, &r.MessageID, &started, &last, &liveUntil, &finishedAt, &r.Public); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.StartedAt = time.Unix(started, 0)
	r.LastPointAt = time.Unix(last, 0)
	if liveUntil.Valid {
		t := time.Unix(liveUntil.Int64, 0)
		r.LiveUntil = &t
	}
	if finishedAt.Valid {
		t := time.Unix(finishedAt.Int64, 0)
		r.FinishedAt = &t
	}
	return &r, nil
}

func nullTime(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// CreateRace inserts a new active race and fills r.ID.
func (s *Store) CreateRace(ctx context.Context, r *Race) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO races (user_id, chat_id, message_id, started_at, last_point_at, live_until) VALUES (?, ?, ?, ?, ?, ?)`,
		r.UserID, r.ChatID, r.MessageID, r.StartedAt.Unix(), r.LastPointAt.Unix(), nullTime(r.LiveUntil))
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetRace(ctx context.Context, id int64) (*Race, error) {
	return scanRace(s.db.QueryRowContext(ctx, `SELECT `+raceCols+` FROM races WHERE id = ?`, id))
}

func (s *Store) GetRaceByMessage(ctx context.Context, chatID int64, messageID int) (*Race, error) {
	return scanRace(s.db.QueryRowContext(ctx,
		`SELECT `+raceCols+` FROM races WHERE chat_id = ? AND message_id = ?`, chatID, messageID))
}

func (s *Store) queryRaces(ctx context.Context, query string, args ...any) ([]*Race, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Race
	for rows.Next() {
		r, err := scanRace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRaces returns the user's latest races, newest first.
func (s *Store) ListRaces(ctx context.Context, userID int64, limit int) ([]*Race, error) {
	return s.queryRaces(ctx,
		`SELECT `+raceCols+` FROM races WHERE user_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, userID, limit)
}

func (s *Store) ActiveRaces(ctx context.Context) ([]*Race, error) {
	return s.queryRaces(ctx, `SELECT `+raceCols+` FROM races WHERE finished_at IS NULL`)
}

func (s *Store) ActiveRacesByUser(ctx context.Context, userID int64) ([]*Race, error) {
	return s.queryRaces(ctx, `SELECT `+raceCols+` FROM races WHERE user_id = ? AND finished_at IS NULL`, userID)
}

// FinishRace marks the race finished. Returns false if it was already finished.
func (s *Store) FinishRace(ctx context.Context, id int64, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE races SET finished_at = ? WHERE id = ? AND finished_at IS NULL`, at.Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) SetPublic(ctx context.Context, id int64, public bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE races SET public = ? WHERE id = ?`, public, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddPoint appends a point and bumps the race's last_point_at.
func (s *Store) AddPoint(ctx context.Context, raceID int64, p Point) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO points (race_id, lat, lon, ts, accuracy) VALUES (?, ?, ?, ?, ?)`,
		raceID, p.Lat, p.Lon, p.Time.Unix(), p.Accuracy); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE races SET last_point_at = MAX(last_point_at, ?) WHERE id = ?`, p.Time.Unix(), raceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Points(ctx context.Context, raceID int64) ([]Point, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT lat, lon, ts, COALESCE(accuracy, 0) FROM points WHERE race_id = ? ORDER BY ts, id`, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		var ts int64
		if err := rows.Scan(&p.Lat, &p.Lon, &ts, &p.Accuracy); err != nil {
			return nil, err
		}
		p.Time = time.Unix(ts, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}
