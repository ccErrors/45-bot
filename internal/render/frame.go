package render

import (
	"math"

	"github.com/ccErrors/race-bot/internal/geo"
)

const maxLat = 85.05112878

// worldPixel projects lat/lon to Web Mercator pixel coordinates at zoom 0
// (the whole world is TileSize x TileSize).
func worldPixel(lat, lon float64) (x, y float64) {
	lat = math.Max(-maxLat, math.Min(maxLat, lat))
	x = (lon + 180) / 360 * TileSize
	y = (1 - math.Asinh(math.Tan(lat*math.Pi/180))/math.Pi) / 2 * TileSize
	return x, y
}

// Frame is the map viewport: zoom level, image size and the world-pixel
// position of the image's top-left corner at that zoom.
type Frame struct {
	Zoom             int
	Width, Height    int
	OriginX, OriginY float64
}

type FrameOptions struct {
	MaxSide int     // max image side, px
	MinSide int     // min image side, px
	MaxZoom int     // highest tile zoom level to use
	Fill    float64 // share of the image the track occupies (0.8 → 10% margin per side)
}

var DefaultFrameOptions = FrameOptions{MaxSide: 2000, MinSide: 400, MaxZoom: 18, Fill: 0.8}

// ComputeFrame picks the highest zoom at which the track, scaled so it takes
// opt.Fill of the image, fits into opt.MaxSide, and centers the image on the
// center of the track's bounding box.
func ComputeFrame(pts []geo.Point, opt FrameOptions) Frame {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x, y := worldPixel(p.Lat, p.Lon)
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	w0, h0 := maxX-minX, maxY-minY

	z := opt.MaxZoom
	for ; z > 0; z-- {
		s := math.Exp2(float64(z))
		if w0*s/opt.Fill <= float64(opt.MaxSide) && h0*s/opt.Fill <= float64(opt.MaxSide) {
			break
		}
	}
	s := math.Exp2(float64(z))

	side := func(trackPx float64) int {
		v := int(math.Ceil(trackPx / opt.Fill))
		return max(opt.MinSide, min(opt.MaxSide, v))
	}
	f := Frame{Zoom: z, Width: side(w0 * s), Height: side(h0 * s)}
	cx, cy := (minX+maxX)/2*s, (minY+maxY)/2*s
	// Integer origin keeps tiles pixel-aligned (no resampling blur).
	f.OriginX = math.Round(cx - float64(f.Width)/2)
	f.OriginY = math.Round(cy - float64(f.Height)/2)
	return f
}

// Project converts lat/lon to image pixel coordinates within the frame.
func (f Frame) Project(lat, lon float64) (x, y float64) {
	wx, wy := worldPixel(lat, lon)
	s := math.Exp2(float64(f.Zoom))
	return wx*s - f.OriginX, wy*s - f.OriginY
}
