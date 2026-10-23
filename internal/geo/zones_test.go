package geo

import (
	"math/rand/v2"
	"reflect"
	"testing"
	"time"
)

// line makes a track of n points going east from (lat, lon), ~100 m apart, 20 s apart.
func line(lat, lon float64, n int) []Segment {
	t0 := time.Unix(1_700_000_000, 0)
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{Lat: lat, Lon: lon + float64(i)*0.0016, Time: t0.Add(time.Duration(i) * 20 * time.Second)}
	}
	segs, _ := Analyze(pts, 0)
	return segs
}

// north returns the latitude d meters north of lat.
func north(lat, d float64) float64 { return lat + d/111195 }

func TestZones(t *testing.T) {
	const lat, lon = 55.75, 37.6
	tracks := [][]Segment{
		line(lat, lon, 30),                  // 0
		line(north(lat, 900), lon+0.01, 30), // 1: 900 m from 0 → same zone
		line(north(lat, 1800), lon, 30),     // 2: 900 m from 1, 1800 m from 0 → same zone (transitive)
		nil,                                 // 3: empty → no zone
		line(north(lat, 2810), lon, 30),     // 4: 1010 m from 2 → own zone
		line(55.0, 37.0, 30),                // 5: far away
		line(north(55.0, 999), 37.0, 1),     // 6: one point, no segments → no zone
	}
	got := Zones(tracks, ZoneRadiusM)
	want := [][]int{{0, 1, 2}, {4}, {5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("zones %v, want %v", got, want)
	}
}

func TestZonesSinglePointTrack(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	// One segment = two points: start 999 m from the other track, end far away.
	segs, _ := Analyze([]Point{
		{Lat: north(55, 999), Lon: 37, Time: t0},
		{Lat: north(55, 5000), Lon: 37, Time: t0.Add(10 * time.Minute)},
	}, 0)
	got := Zones([][]Segment{line(55, 37, 5), segs}, ZoneRadiusM)
	if want := [][]int{{0, 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("zones %v, want %v", got, want)
	}
}

func TestZonesAcrossAntimeridian(t *testing.T) {
	a := line(0, 179.99, 5) // ends at 179.9964
	b := line(0, -179.999, 5)
	if got := Zones([][]Segment{a, b}, ZoneRadiusM); len(got) != 1 {
		t.Fatalf("zones %v, want one zone", got)
	}
}

// TestZonesMatchesBruteForce checks the grid against comparing every pair of points.
func TestZonesMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	t0 := time.Unix(1_700_000_000, 0)
	for iter := 0; iter < 20; iter++ {
		var tracks [][]Segment
		for range 12 {
			// Random walks in a ~10 km box: some touch, some don't.
			lat, lon := 55.7+rng.Float64()*0.09, 37.5+rng.Float64()*0.16
			var pts []Point
			for k := range 2 + rng.IntN(20) {
				lat += (rng.Float64() - 0.5) * 0.004
				lon += (rng.Float64() - 0.5) * 0.007
				pts = append(pts, Point{Lat: lat, Lon: lon, Time: t0.Add(time.Duration(k) * time.Minute)})
			}
			segs, _ := Analyze(pts, 0)
			tracks = append(tracks, segs)
		}
		if got, want := Zones(tracks, ZoneRadiusM), bruteZones(tracks, ZoneRadiusM); !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: zones %v, brute force %v", iter, got, want)
		}
	}
}

func bruteZones(tracks [][]Segment, radiusM float64) [][]int {
	pts := make([][]Point, len(tracks))
	for i, segs := range tracks {
		if len(segs) > 0 {
			pts[i] = append(pts[i], segs[0].From)
			for _, s := range segs {
				pts[i] = append(pts[i], s.To)
			}
		}
	}
	near := func(i, j int) bool {
		for _, p := range pts[i] {
			for _, q := range pts[j] {
				if Distance(p, q) < radiusM {
					return true
				}
			}
		}
		return false
	}
	zone := make([]int, len(tracks))
	for i := range zone {
		zone[i] = i
	}
	// Relabel until stable: simple and obviously correct.
	for changed := true; changed; {
		changed = false
		for i := range tracks {
			for j := range tracks {
				if zone[i] != zone[j] && pts[i] != nil && pts[j] != nil && near(i, j) {
					lo, hi := min(zone[i], zone[j]), max(zone[i], zone[j])
					for k := range zone {
						if zone[k] == hi {
							zone[k] = lo
						}
					}
					changed = true
				}
			}
		}
	}
	var out [][]int
	idx := map[int]int{}
	for i := range tracks {
		if pts[i] == nil {
			continue
		}
		z, ok := idx[zone[i]]
		if !ok {
			z = len(out)
			idx[zone[i]] = z
			out = append(out, nil)
		}
		out[z] = append(out[z], i)
	}
	return out
}
