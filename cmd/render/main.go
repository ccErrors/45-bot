// Command render draws a race to an image file — handy for debugging the
// renderer without Telegram. It can also import a CSV track into the bot's DB.
//
//	go run ./cmd/render -race 1a -o race.png                     # from the bot's DB
//	go run ./cmd/render -csv testdata/ride.csv -o race.png       # lat,lon,unix_ts per line
//	go run ./cmd/render -csv a.csv,b.csv -o agg.png              # several tracks on one map (aggregation)
//	go run ./cmd/render -csv ride.fit -o race.png                # .fit files are accepted too
//	go run ./cmd/render -csv testdata/ride.csv -seed-user 12345  # save as a race of Telegram user 12345
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ccErrors/race-bot/internal/fitimport"
	"github.com/ccErrors/race-bot/internal/geo"
	"github.com/ccErrors/race-bot/internal/render"
	"github.com/ccErrors/race-bot/internal/storage"
)

func main() {
	dbPath := flag.String("db", "data/bot.db", "SQLite database path")
	raceHex := flag.String("race", "", "race id (hex) to render from the database")
	csvPath := flag.String("csv", "", "CSV track file(s), comma-separated: lat,lon,unix_ts")
	seedUser := flag.Int64("seed-user", 0, "with -csv: store the track as a finished race of this Telegram user id and exit")
	out := flag.String("o", "race.png", "output PNG file")
	cacheDir := flag.String("tiles", "data/tiles", "tile cache dir")
	maxSpeed := flag.Float64("max-speed", 120, "glitch speed threshold, km/h")
	flag.Parse()

	var tracks [][]geo.Point
	switch {
	case *csvPath != "":
		for _, p := range strings.Split(*csvPath, ",") {
			pts, err := readCSV(p)
			if err != nil {
				log.Fatalf("%s: %v", p, err)
			}
			tracks = append(tracks, pts)
		}
	case *raceHex != "":
		pts, err := readDB(*dbPath, *raceHex)
		if err != nil {
			log.Fatal(err)
		}
		tracks = append(tracks, pts)
	default:
		flag.Usage()
		os.Exit(2)
	}

	if *seedUser != 0 {
		if *csvPath == "" {
			log.Fatal("-seed-user requires -csv")
		}
		for _, pts := range tracks {
			id, err := seed(*dbPath, *seedUser, pts)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("saved %d points as /race_%x for user %d\n", len(pts), id, *seedUser)
		}
		return
	}

	var (
		segs   [][]geo.Segment
		points int
	)
	for _, pts := range tracks {
		s, _ := geo.Analyze(pts, *maxSpeed)
		segs = append(segs, s)
		points += len(pts)
	}
	st := geo.Combine(segs...)
	tiles := render.NewHTTPTiles("https://tile.openstreetmap.org/{z}/{x}/{y}.png", "race-bot/1.0 (render cli)", *cacheDir)
	start := time.Now()
	img, err := render.Render(context.Background(), tiles, segs, st, render.DefaultFrameOptions)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	b := img.Bounds()
	fmt.Printf("%s: %dx%d, %d track(s), %d points, %.2f km, %.1f..%.1f km/h, rendered in %s\n",
		*out, b.Dx(), b.Dy(), len(tracks), points, st.DistanceM/1000, st.MinSpeedKmh, st.MaxSpeedKmh, time.Since(start).Round(time.Millisecond))
}

// readCSV reads a CSV track, or a .fit file through the same validation the bot uses.
func readCSV(path string) ([]geo.Point, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fitimport.IsFITName(path) {
		act, err := fitimport.Parse(context.Background(), f, time.Now())
		if err != nil {
			return nil, err
		}
		return act.Points, nil
	}
	return geo.ReadCSV(f)
}

func readDB(path, hexID string) ([]geo.Point, error) {
	id, err := strconv.ParseInt(hexID, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("bad race id %q", hexID)
	}
	store, err := storage.Open(path)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	sp, err := store.Points(context.Background(), id)
	if err != nil {
		return nil, err
	}
	pts := make([]geo.Point, len(sp))
	for i, p := range sp {
		pts[i] = geo.Point{Lat: p.Lat, Lon: p.Lon, Time: p.Time}
	}
	return pts, nil
}

// seed stores the track as a finished race. The fake message id is negative
// so it can never collide with a real Telegram live-location message.
func seed(path string, userID int64, pts []geo.Point) (int64, error) {
	if len(pts) == 0 {
		return 0, fmt.Errorf("empty track")
	}
	ctx := context.Background()
	store, err := storage.Open(path)
	if err != nil {
		return 0, err
	}
	defer store.Close()
	start, end := pts[0].Time, pts[len(pts)-1].Time
	r := &storage.Race{
		UserID:      userID,
		ChatID:      userID, // private chat id equals user id
		MessageID:   -int(time.Now().UnixNano() % 1e9),
		StartedAt:   start,
		LastPointAt: start,
		LiveUntil:   &end,
	}
	if err := store.CreateRace(ctx, r); err != nil {
		return 0, err
	}
	for _, p := range pts {
		if err := store.AddPoint(ctx, r.ID, storage.Point{Lat: p.Lat, Lon: p.Lon, Time: p.Time}); err != nil {
			return 0, err
		}
	}
	if _, err := store.FinishRace(ctx, r.ID, end); err != nil {
		return 0, err
	}
	return r.ID, nil
}
