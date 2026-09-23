// Package geo computes distances, per-segment speeds and track statistics.
package geo

import (
	"math"
	"slices"
	"time"
)

const earthRadiusM = 6371008.8

// SpeedHalfWindow is the half-width of the median speed filter: a segment's
// speed becomes the median over segments whose midpoints are within ±2 s.
// With 1 Hz FIT data that's ~5 segments and removes GPS jitter spikes; with
// live location (10+ s between points) the window holds one segment, so the
// filter changes nothing.
const SpeedHalfWindow = 2 * time.Second

type Point struct {
	Lat, Lon float64
	Time     time.Time
}

// Distance returns the great-circle distance in meters (haversine).
func Distance(a, b Point) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLat, dLon := lat2-lat1, rad(b.Lon-a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }

// Segment is a piece of track between two consecutive kept points.
type Segment struct {
	From, To  Point
	DistanceM float64 // raw distance between the points
	SpeedKmh  float64 // median-smoothed speed, see SpeedHalfWindow
}

func (s Segment) mid() time.Time { return s.From.Time.Add(s.To.Time.Sub(s.From.Time) / 2) }

type Stats struct {
	DistanceM   float64
	Duration    time.Duration
	MinSpeedKmh float64
	MaxSpeedKmh float64
}

// Analyze splits the track into segments and computes statistics.
// A point is dropped if its timestamp is not after the previous kept point,
// or if reaching it would require more than maxSpeedKmh (a GPS glitch);
// maxSpeedKmh <= 0 disables the glitch filter. Distances stay raw, speeds are
// median-smoothed (see SpeedHalfWindow); stored points are never modified.
func Analyze(pts []Point, maxSpeedKmh float64) ([]Segment, Stats) {
	var segs []Segment
	if len(pts) == 0 {
		return nil, Stats{}
	}
	a := pts[0]
	for _, b := range pts[1:] {
		dt := b.Time.Sub(a.Time).Seconds()
		if dt <= 0 {
			continue
		}
		d := Distance(a, b)
		v := d / dt * 3.6
		if maxSpeedKmh > 0 && v > maxSpeedKmh {
			continue
		}
		segs = append(segs, Segment{From: a, To: b, DistanceM: d, SpeedKmh: v})
		a = b
	}
	smoothSpeeds(segs, SpeedHalfWindow)
	return segs, Combine(segs)
}

// smoothSpeeds replaces each segment's speed with the median of the raw speeds
// of segments whose midpoints lie within ±half of its own midpoint.
func smoothSpeeds(segs []Segment, half time.Duration) {
	raw := make([]float64, len(segs))
	for i, s := range segs {
		raw[i] = s.SpeedKmh
	}
	var (
		lo, hi int // window is segs[lo:hi]
		buf    []float64
	)
	for i := range segs {
		m := segs[i].mid()
		for segs[lo].mid().Before(m.Add(-half)) {
			lo++
		}
		for hi < len(segs) && !segs[hi].mid().After(m.Add(half)) {
			hi++
		}
		buf = append(buf[:0], raw[lo:hi]...)
		slices.Sort(buf)
		n := len(buf)
		if n%2 == 1 {
			segs[i].SpeedKmh = buf[n/2]
		} else {
			segs[i].SpeedKmh = (buf[n/2-1] + buf[n/2]) / 2
		}
	}
}

// Combine computes statistics over several tracks: distance and duration are
// summed, the speed range spans all segments. Empty tracks are ignored.
func Combine(tracks ...[]Segment) Stats {
	var (
		st    Stats
		first = true
	)
	for _, segs := range tracks {
		if len(segs) == 0 {
			continue
		}
		st.Duration += segs[len(segs)-1].To.Time.Sub(segs[0].From.Time)
		for _, s := range segs {
			st.DistanceM += s.DistanceM
			if first || s.SpeedKmh < st.MinSpeedKmh {
				st.MinSpeedKmh = s.SpeedKmh
			}
			if first || s.SpeedKmh > st.MaxSpeedKmh {
				st.MaxSpeedKmh = s.SpeedKmh
			}
			first = false
		}
	}
	return st
}
