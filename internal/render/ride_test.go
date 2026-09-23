package render

import (
	"context"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/ccErrors/race-bot/internal/geo"
)

// TestRenderRide renders the sample ride from testdata. Set RENDER_OUT=path.png
// to save the result (drawn over flat placeholder tiles, no network).
func TestRenderRide(t *testing.T) {
	f, err := os.Open("../../testdata/ride.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pts, err := geo.ReadCSV(f)
	if err != nil {
		t.Fatal(err)
	}

	segs, st := geo.Analyze(pts, 120)
	// The glitch point and the duplicate timestamp must be dropped.
	if want := len(pts) - 1 - 2; len(segs) != want {
		t.Fatalf("segments: got %d, want %d", len(segs), want)
	}
	if math.Abs(st.DistanceM-10050) > 200 {
		t.Errorf("distance %.0f m, want ≈10 km", st.DistanceM)
	}
	if st.MinSpeedKmh > 1 || st.MaxSpeedKmh < 30 || st.MaxSpeedKmh > 45 {
		t.Errorf("speeds %.1f..%.1f km/h", st.MinSpeedKmh, st.MaxSpeedKmh)
	}

	img, err := Render(context.Background(), flatTiles{}, [][]geo.Segment{segs}, st, DefaultFrameOptions)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() > 2000 || b.Dy() > 2000 {
		t.Fatalf("image too big: %v", b)
	}
	if out := os.Getenv("RENDER_OUT"); out != "" {
		w, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if err := png.Encode(w, img); err != nil {
			t.Fatal(err)
		}
	}
}
