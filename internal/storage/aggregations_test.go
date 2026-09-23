package storage

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAggregations(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Five races of user 1, an hour apart, plus one of user 2 in the middle.
	t0 := time.Unix(1_700_000_000, 0)
	var races []*Race
	for i := range 5 {
		r := &Race{UserID: 1, ChatID: 1, MessageID: i + 1, StartedAt: t0.Add(time.Duration(i) * time.Hour), LastPointAt: t0.Add(time.Duration(i)*time.Hour + 30*time.Minute)}
		if err := s.CreateRace(ctx, r); err != nil {
			t.Fatal(err)
		}
		races = append(races, r)
	}
	other := &Race{UserID: 2, ChatID: 2, MessageID: 1, StartedAt: t0.Add(2 * time.Hour), LastPointAt: t0.Add(3 * time.Hour)}
	if err := s.CreateRace(ctx, other); err != nil {
		t.Fatal(err)
	}

	prev, next, err := s.NeighborRaces(ctx, 1, races[2], races[2], 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids(prev) != "2,1" || ids(next) != "4,5" {
		t.Fatalf("neighbors: prev %s next %s", ids(prev), ids(next))
	}

	aggID, err := s.CreateAggregation(ctx, 1, []int64{races[2].ID, races[3].ID})
	if err != nil {
		t.Fatal(err)
	}
	// Neighbors of the aggregation skip its members.
	exclude := map[int64]bool{races[2].ID: true, races[3].ID: true}
	prev, next, _ = s.NeighborRaces(ctx, 1, races[2], races[3], 2, exclude)
	if ids(prev) != "2,1" || ids(next) != "5" {
		t.Fatalf("agg neighbors: prev %s next %s", ids(prev), ids(next))
	}

	if err := s.AddAggregationRace(ctx, aggID, races[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAggregationRace(ctx, aggID, races[0].ID); err != nil { // duplicate is a no-op
		t.Fatal(err)
	}
	got, err := s.AggregationRaces(ctx, aggID)
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "1,3,4" {
		t.Fatalf("members: %s", ids(got))
	}

	list, err := s.ListAggregations(ctx, 1, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if a := list[0]; a.Races != 3 || !a.FirstStartedAt.Equal(t0) || !a.LastPointAt.Equal(races[3].LastPointAt) {
		t.Fatalf("summary: %+v", a)
	}
	if l, _ := s.ListAggregations(ctx, 2, 10); len(l) != 0 {
		t.Fatal("other user sees aggregation")
	}

	if err := s.RemoveAggregationRace(ctx, aggID, races[2].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AggregationRaces(ctx, aggID); ids(got) != "1,4" {
		t.Fatalf("after remove: %s", ids(got))
	}
	if _, err := s.GetRace(ctx, races[2].ID); err != nil {
		t.Fatal("removed race must stay in the DB")
	}

	if err := s.DeleteAggregation(ctx, aggID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAggregation(ctx, aggID); err != ErrNotFound {
		t.Fatalf("deleted aggregation: %v", err)
	}
	if got, _ := s.AggregationRaces(ctx, aggID); len(got) != 0 {
		t.Fatal("membership rows must be deleted")
	}
	if l, _ := s.ListRaces(ctx, 1, 10); len(l) != 5 {
		t.Fatalf("races after aggregation delete: %d", len(l))
	}
}

func ids(rs []*Race) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = strconv.FormatInt(r.ID, 10)
	}
	return strings.Join(parts, ",")
}
