package render

import (
	"context"
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/ccErrors/race-bot/internal/geo"
)

func track(pts ...[2]float64) []geo.Point {
	out := make([]geo.Point, len(pts))
	for i, p := range pts {
		out[i] = geo.Point{Lat: p[0], Lon: p[1], Time: time.Unix(int64(i*30), 0)}
	}
	return out
}

func bbox(f Frame, pts []geo.Point) (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x, y := f.Project(p.Lat, p.Lon)
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	return
}

func TestComputeFrameMargins(t *testing.T) {
	pts := track([2]float64{55.70, 37.55}, [2]float64{55.78, 37.70}, [2]float64{55.74, 37.62})
	f := ComputeFrame(pts, DefaultFrameOptions)
	if f.Width > 2000 || f.Height > 2000 {
		t.Fatalf("too big: %dx%d", f.Width, f.Height)
	}
	// Next zoom level would not fit — i.e. the zoom is maximal.
	if f.Width*2 <= 2000*1 && f.Height*2 <= 2000 {
		t.Fatalf("zoom %d is not maximal: %dx%d", f.Zoom, f.Width, f.Height)
	}
	minX, minY, maxX, maxY := bbox(f, pts)
	for _, c := range []struct {
		lo, hi float64
		size   int
	}{{minX, maxX, f.Width}, {minY, maxY, f.Height}} {
		if c.size == DefaultFrameOptions.MinSide {
			continue
		}
		left, right := c.lo, float64(c.size)-c.hi
		want := 0.1 * float64(c.size)
		if math.Abs(left-want) > 2 || math.Abs(right-want) > 2 {
			t.Errorf("margins %.1f / %.1f, want %.1f (size %d)", left, right, want, c.size)
		}
	}
}

func TestComputeFrameTinyTrack(t *testing.T) {
	pts := track([2]float64{55.75, 37.62}, [2]float64{55.75, 37.62})
	f := ComputeFrame(pts, DefaultFrameOptions)
	if f.Zoom != 18 || f.Width != 400 || f.Height != 400 {
		t.Fatalf("got %+v", f)
	}
	x, y := f.Project(55.75, 37.62)
	if math.Abs(x-200) > 1 || math.Abs(y-200) > 1 {
		t.Fatalf("not centered: %.1f,%.1f", x, y)
	}
}

func TestSpeedColor(t *testing.T) {
	if c := SpeedColor(0); c.B < 200 || c.R > 10 {
		t.Errorf("slow = %v, want blue", c)
	}
	if c := SpeedColor(1); c.R < 200 || c.B > 10 {
		t.Errorf("fast = %v, want red", c)
	}
}

type flatTiles struct{}

func (flatTiles) Tile(context.Context, int, int, int) (image.Image, error) {
	return image.NewUniform(color.RGBA{240, 235, 225, 255}), nil
}

func TestRender(t *testing.T) {
	pts := track([2]float64{55.70, 37.55}, [2]float64{55.78, 37.70}, [2]float64{55.74, 37.62})
	segs, st := geo.Analyze(pts, 0)
	img, err := Render(context.Background(), flatTiles{}, [][]geo.Segment{segs}, st, DefaultFrameOptions)
	if err != nil {
		t.Fatal(err)
	}
	f := ComputeFrame(pts, DefaultFrameOptions)
	if b := img.Bounds(); b.Dx() != f.Width || b.Dy() != f.Height {
		t.Fatalf("bounds %v", b)
	}
}
