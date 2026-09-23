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
