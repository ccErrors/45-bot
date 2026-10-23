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

// The heatmap shows the same area as a race map at twice the resolution.
func TestRenderHeatmapDoubleResolution(t *testing.T) {
	var tracks [][]geo.Segment
	for _, name := range []string{"ride.csv", "ride2.csv"} {
		f, err := os.Open("../../testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		pts, err := geo.ReadCSV(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		segs, _ := geo.Analyze(pts, 120)
		tracks = append(tracks, segs)
	}
	st := geo.Combine(tracks...)
	race, err := Render(context.Background(), flatTiles{}, tracks, st, DefaultFrameOptions)
	if err != nil {
		t.Fatal(err)
	}
	heat, err := RenderHeatmap(context.Background(), flatTiles{}, tracks, st, HeatmapFrameOptions)
	if err != nil {
		t.Fatal(err)
	}
	rb, hb := race.Bounds(), heat.Bounds()
	// ±1 px: each side is rounded up separately.
	if abs(hb.Dx()-2*rb.Dx()) > 1 || abs(hb.Dy()-2*rb.Dy()) > 1 {
		t.Fatalf("heatmap %v, race map %v: want twice the size", hb, rb)
	}
	if hb.Dx() > 4000 || hb.Dy() > 4000 {
		t.Fatalf("heatmap too big: %v", hb)
	}
}

func abs(x int) int { return max(x, -x) }
