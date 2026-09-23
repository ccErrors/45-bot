package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestRaceLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	t0 := time.Unix(1_700_000_000, 0)
	until := t0.Add(time.Hour)
	r := &Race{UserID: 7, ChatID: 7, MessageID: 42, StartedAt: t0, LastPointAt: t0, LiveUntil: &until}
	if err := s.CreateRace(ctx, r); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := s.AddPoint(ctx, r.ID, Point{Lat: 55 + float64(i)*0.001, Lon: 37, Time: t0.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetRaceByMessage(ctx, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active() || !got.LastPointAt.Equal(t0.Add(2*time.Minute)) || !got.LiveUntil.Equal(until) {
		t.Fatalf("race: %+v", got)
	}
	pts, err := s.Points(ctx, r.ID)
	if err != nil || len(pts) != 3 {
		t.Fatalf("points: %v %v", len(pts), err)
	}
	if ok, _ := s.FinishRace(ctx, r.ID, t0.Add(time.Hour)); !ok {
		t.Fatal("finish failed")
	}
	if ok, _ := s.FinishRace(ctx, r.ID, t0.Add(time.Hour)); ok {
		t.Fatal("finished twice")
	}
	list, _ := s.ListRaces(ctx, 7, 10)
	if len(list) != 1 || list[0].Active() {
		t.Fatalf("list: %+v", list)
	}
	if other, _ := s.ListRaces(ctx, 8, 10); len(other) != 0 {
		t.Fatal("other user sees races")
	}
}

// A database created before schema versioning (user_version 0, no public column)
// must be upgraded in place without losing races.
func TestMigrateLegacyDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO races (user_id, chat_id, message_id, started_at, last_point_at) VALUES (1, 1, 1, 100, 200)`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.GetRace(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Public {
		t.Fatal("legacy race must stay private")
	}
	if err := s.SetPublic(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRace(ctx, 1); !r.Public {
		t.Fatal("SetPublic did not persist")
	}
	if err := s.SetPublic(ctx, 99, true); err != ErrNotFound {
		t.Fatalf("SetPublic on missing race: %v", err)
	}

	// Reopening an up-to-date DB is a no-op.
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("user_version %d, want %d", v, len(migrations))
	}
}
