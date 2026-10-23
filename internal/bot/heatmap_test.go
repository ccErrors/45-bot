package bot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ccErrors/race-bot/internal/config"
	"github.com/ccErrors/race-bot/internal/storage"
)

func TestUserZones(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	b := &Bot{store: store, cfg: &config.Config{Location: time.UTC, MaxSpeedKmh: 120}}

	t0 := time.Unix(1_700_000_000, 0)
	day := 0
	// race adds a finished race of ~1 km going east from (lat, lon).
	race := func(userID int64, lat, lon float64) int64 {
		day++
		start := t0.Add(time.Duration(day) * 24 * time.Hour)
		pts := make([]storage.Point, 10)
		for i := range pts {
			pts[i] = storage.Point{Lat: lat, Lon: lon + float64(i)*0.0016, Time: start.Add(time.Duration(i) * 20 * time.Second)}
		}
		r, _, err := store.ImportRace(ctx, &storage.Race{
			UserID: userID, ChatID: userID, MessageID: -day, StartedAt: start, LastPointAt: pts[9].Time, FinishedAt: &pts[9].Time,
		}, pts)
		if err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	moscowA := race(1, 55.75, 37.6)
	far := race(1, 44.8, 20.46)
	stranger := race(2, 55.751, 37.6) // right next to moscowA, but someone else's
	moscowB := race(1, 55.757, 37.61) // ~780 m from moscowA
	race(1, 44.8, 20.46)              // second ride in the far zone…
	race(1, 44.8, 20.46)              // …and a third: now the biggest zone
	// A race without points has no track and belongs to no zone.
	if err := store.CreateRace(ctx, &storage.Race{UserID: 1, ChatID: 1, MessageID: 100, StartedAt: t0, LastPointAt: t0}); err != nil {
		t.Fatal(err)
	}

	zones, err := b.userZones(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 2 {
		t.Fatalf("%d zones", len(zones))
	}
	if z := zones[0]; len(z.races) != 3 || z.id() != far {
		t.Fatalf("biggest zone first: %d races, id %x", len(z.races), z.id())
	}
	moscow := zones[1]
	if len(moscow.races) != 2 || !moscow.has(moscowA) || !moscow.has(moscowB) || moscow.has(stranger) {
		t.Fatalf("moscow zone: %v", raceIDs(moscow))
	}
	if moscow.id() != moscowA {
		t.Fatalf("zone id %x, want smallest race id %x", moscow.id(), moscowA)
	}

	list := b.heatmapsList(zones)
	if !strings.Contains(list, heatCmd(far)+"  3 заезда") || !strings.Contains(list, heatCmd(moscowA)+"  2 заезда") {
		t.Fatalf("list: %q", list)
	}

	// The stranger sees only their own race.
	if zs, _ := b.userZones(ctx, 2); len(zs) != 1 || len(zs[0].races) != 1 || zs[0].id() != stranger {
		t.Fatalf("stranger zones: %+v", zs)
	}
}

func raceIDs(z zone) []int64 {
	var ids []int64
	for _, r := range z.races {
		ids = append(ids, r.ID)
	}
	return ids
}
