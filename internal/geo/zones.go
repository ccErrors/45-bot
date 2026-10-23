package geo

import "math"

// ZoneRadiusM is how close two tracks must come to share a heatmap zone:
// some point of one is closer than this to some point of the other.
const ZoneRadiusM = 1000

// Zones groups tracks into heatmap zones: connected components of the
// "tracks come closer than radiusM" relation. It is transitive — if A passes
// near B and B near C, all three are one zone even if A and C are far apart.
// Track points are the segment endpoints, so points dropped by Analyze
// (GPS glitches) can't glue unrelated zones together.
//
// Returns indexes into tracks: each zone ascending, zones ordered by their
// first index. Empty tracks belong to no zone.
func Zones(tracks [][]Segment, radiusM float64) [][]int {
	// Points go onto a sphere in 3D, where a grid with cells as wide as the
	// threshold chord puts every close pair in the same or adjacent cells —
	// no special cases for longitude shrinking toward the poles or the 180° seam.
	chord := 2 * earthRadiusM * math.Sin(radiusM/(2*earthRadiusM))
	maxD2 := chord * chord

	grids := make([]map[cell][]vec3, len(tracks))
	owners := map[cell][]int{} // tracks with points in the cell
	for i, segs := range tracks {
		if len(segs) == 0 {
			continue
		}
		g := map[cell][]vec3{}
		add := func(p Point) {
			v := toVec3(p)
			c := v.cell(chord)
			if _, ok := g[c]; !ok {
				owners[c] = append(owners[c], i)
			}
			g[c] = append(g[c], v)
		}
		add(segs[0].From)
		for _, s := range segs {
			add(s.To)
		}
		grids[i] = g
	}

	parent := make([]int, len(tracks))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}

	for i, g := range grids {
		for c, pts := range g {
			for dx := int64(-1); dx <= 1; dx++ {
				for dy := int64(-1); dy <= 1; dy++ {
					for dz := int64(-1); dz <= 1; dz++ {
						n := cell{c.x + dx, c.y + dy, c.z + dz}
						for _, j := range owners[n] {
							// Each pair once; skip tracks already known to share a zone.
							if j <= i || find(i) == find(j) {
								continue
							}
							if anyClose(pts, grids[j][n], maxD2) {
								parent[find(j)] = find(i)
							}
						}
					}
				}
			}
		}
	}

	var (
		zones  [][]int
		zoneOf = map[int]int{} // root → index in zones
	)
	for i, g := range grids {
		if g == nil {
			continue
		}
		r := find(i)
		z, ok := zoneOf[r]
		if !ok {
			z = len(zones)
			zoneOf[r] = z
			zones = append(zones, nil)
		}
		zones[z] = append(zones[z], i)
	}
	return zones
}

type vec3 struct{ x, y, z float64 }

type cell struct{ x, y, z int64 }

func toVec3(p Point) vec3 {
	lat, lon := rad(p.Lat), rad(p.Lon)
	return vec3{
		earthRadiusM * math.Cos(lat) * math.Cos(lon),
		earthRadiusM * math.Cos(lat) * math.Sin(lon),
		earthRadiusM * math.Sin(lat),
	}
}

func (v vec3) cell(size float64) cell {
	return cell{int64(math.Floor(v.x / size)), int64(math.Floor(v.y / size)), int64(math.Floor(v.z / size))}
}

func anyClose(a, b []vec3, maxD2 float64) bool {
	for _, p := range a {
		for _, q := range b {
			dx, dy, dz := p.x-q.x, p.y-q.y, p.z-q.z
			if dx*dx+dy*dy+dz*dz < maxD2 {
				return true
			}
		}
	}
	return false
}
