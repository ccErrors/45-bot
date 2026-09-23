package geo

import (
	"math"
	"testing"
	"time"
)

func TestDistance(t *testing.T) {
	// One degree of latitude ≈ 111.2 km.
	d := Distance(Point{Lat: 55, Lon: 37}, Point{Lat: 56, Lon: 37})
	if math.Abs(d-111195) > 100 {
		t.Fatalf("got %.0f m", d)
	}
}

func TestAnalyze(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	// ~111 m steps north.
	pts := []Point{
		{Lat: 55.000, Lon: 37, Time: t0},
		{Lat: 55.001, Lon: 37, Time: t0.Add(20 * time.Second)}, // ~20 km/h
		{Lat: 55.002, Lon: 37, Time: t0.Add(20 * time.Second)}, // dt=0, skipped
		{Lat: 55.003, Lon: 37, Time: t0.Add(40 * time.Second)}, // ~40 km/h over 222 m
		{Lat: 55.100, Lon: 37, Time: t0.Add(50 * time.Second)}, // glitch, dropped
		{Lat: 55.004, Lon: 37, Time: t0.Add(60 * time.Second)}, // ~20 km/h from the last kept point
	}
	segs, st := Analyze(pts, 120)
	if len(segs) != 3 {
		t.Fatalf("segments: %d", len(segs))
	}
	if segs[2].From.Lat != 55.003 {
		t.Fatalf("segment after glitch starts at %v", segs[2].From.Lat)
	}
	if math.Abs(st.MinSpeedKmh-20) > 0.5 || math.Abs(st.MaxSpeedKmh-40) > 0.5 {
		t.Fatalf("speeds: %.2f..%.2f", st.MinSpeedKmh, st.MaxSpeedKmh)
	}
	if math.Abs(st.DistanceM-444.8) > 1 {
		t.Fatalf("distance: %.1f", st.DistanceM)
	}
	if st.Duration != 60*time.Second {
		t.Fatalf("duration: %s", st.Duration)
	}
}

func TestCombine(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	a, _ := Analyze([]Point{{Lat: 55, Lon: 37, Time: t0}, {Lat: 55.001, Lon: 37, Time: t0.Add(20 * time.Second)}}, 0) // ~20 km/h
	b, _ := Analyze([]Point{{Lat: 56, Lon: 37, Time: t0}, {Lat: 56.001, Lon: 37, Time: t0.Add(10 * time.Second)}}, 0) // ~40 km/h
	st := Combine(a, nil, b)
	if math.Abs(st.MinSpeedKmh-20) > 0.5 || math.Abs(st.MaxSpeedKmh-40) > 0.5 {
		t.Fatalf("speeds %.1f..%.1f", st.MinSpeedKmh, st.MaxSpeedKmh)
	}
	if math.Abs(st.DistanceM-222.4) > 1 || st.Duration != 30*time.Second {
		t.Fatalf("distance %.1f, duration %s", st.DistanceM, st.Duration)
	}
}

// A single jitter spike in 1 Hz data must not survive; a sustained sprint must.
func TestAnalyzeMedianSpeed(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	const step = 10.0 / 3.6 // 10 km/h in m/s
	var pts []Point
	lat := 55.0
	for i := range 120 {
		v := step
		switch {
		case i == 30:
			v = step * 3.2 // one-second GPS jitter: 32 km/h
		case i >= 60 && i < 80:
			v = step * 2 // 20 s sprint at 20 km/h
		}
		lat += v / 111195
		pts = append(pts, Point{Lat: lat, Lon: 37, Time: t0.Add(time.Duration(i) * time.Second)})
	}
	segs, st := Analyze(pts, 120)
	if st.MaxSpeedKmh > 21 || st.MaxSpeedKmh < 19 {
		t.Fatalf("max %.1f km/h, want ≈20 (spike removed, sprint kept)", st.MaxSpeedKmh)
	}
	if v := segs[29].SpeedKmh; math.Abs(v-10) > 0.5 {
		t.Errorf("speed at spike %.1f, want ≈10", v)
	}
	if v := segs[70].SpeedKmh; math.Abs(v-20) > 0.5 {
		t.Errorf("speed in sprint %.1f, want ≈20", v)
	}
	// Distance stays raw: it includes the spike's extra metres.
	raw := 0.0
	for i := 1; i < len(pts); i++ {
		raw += Distance(pts[i-1], pts[i])
	}
	if math.Abs(st.DistanceM-raw) > 0.01 {
		t.Errorf("distance %.2f, want raw %.2f", st.DistanceM, raw)
	}
}

// With live-location spacing (10 s) the window holds one segment: nothing changes.
func TestAnalyzeMedianSparse(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	pts := []Point{
		{Lat: 55.000, Lon: 37, Time: t0},
		{Lat: 55.001, Lon: 37, Time: t0.Add(10 * time.Second)},  // ~40 km/h
		{Lat: 55.0015, Lon: 37, Time: t0.Add(20 * time.Second)}, // ~20 km/h
	}
	segs, _ := Analyze(pts, 120)
	if math.Abs(segs[0].SpeedKmh-40) > 0.5 || math.Abs(segs[1].SpeedKmh-20) > 0.5 {
		t.Fatalf("speeds %.1f, %.1f", segs[0].SpeedKmh, segs[1].SpeedKmh)
	}
}
