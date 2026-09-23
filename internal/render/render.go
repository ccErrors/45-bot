// Package render draws a race track over OpenStreetMap tiles.
package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"sync"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/ccErrors/race-bot/internal/geo"
)

var (
	fontRegular = mustParseFont(goregular.TTF)
	fontBold    = mustParseFont(gobold.TTF)
)

func mustParseFont(ttf []byte) *truetype.Font {
	f, err := truetype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	return f
}

func face(f *truetype.Font, size float64) font.Face {
	return truetype.NewFace(f, &truetype.Options{Size: size, Hinting: font.HintingFull})
}

// SpeedColor maps t∈[0,1] to a hue gradient: blue (slowest) → cyan → green → yellow → red (fastest).
func SpeedColor(t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	return hsv(240*(1-t), 1, 0.95)
}

func hsv(h, s, v float64) color.RGBA {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{uint8((r + m) * 255), uint8((g + m) * 255), uint8((b + m) * 255), 255}
}

// Render draws one or more tracks (segments as returned by geo.Analyze) on a
// single map. st must cover all tracks (geo.Combine): it sets the shared color
// scale and the numbers in the stats panel. Empty tracks are skipped.
func Render(ctx context.Context, tiles TileSource, tracks [][]geo.Segment, st geo.Stats, opt FrameOptions) (image.Image, error) {
	var (
		pts      []geo.Point
		nonEmpty [][]geo.Segment
	)
	for _, segs := range tracks {
		if len(segs) == 0 {
			continue
		}
		nonEmpty = append(nonEmpty, segs)
		pts = append(pts, segs[0].From)
		for _, s := range segs {
			pts = append(pts, s.To)
		}
	}
	if len(nonEmpty) == 0 {
		return nil, fmt.Errorf("empty track")
	}
	f := ComputeFrame(pts, opt)
	dc := gg.NewContext(f.Width, f.Height)
	dc.SetRGB(0.9, 0.9, 0.9)
	dc.Clear()

	if err := drawTiles(ctx, dc, tiles, f); err != nil {
		return nil, err
	}

	big := float64(max(f.Width, f.Height))
	lineW := math.Max(3, big/300)
	for _, segs := range nonEmpty {
		drawHalo(dc, f, segs, lineW)
	}
	for _, segs := range nonEmpty {
		drawTrack(dc, f, segs, st, lineW)
	}
	for _, segs := range nonEmpty {
		drawMarkers(dc, f, segs[0].From, segs[len(segs)-1].To, lineW)
	}

	fontSize := math.Max(14, big/60)
	drawStats(dc, st, len(nonEmpty), fontSize)
	drawAttribution(dc, math.Max(11, fontSize*0.45))
	return dc.Image(), nil
}

func drawTiles(ctx context.Context, dc *gg.Context, tiles TileSource, f Frame) error {
	n := 1 << f.Zoom
	x0 := int(math.Floor(f.OriginX / TileSize))
	y0 := int(math.Floor(f.OriginY / TileSize))
	x1 := int(math.Floor((f.OriginX + float64(f.Width) - 1) / TileSize))
	y1 := int(math.Floor((f.OriginY + float64(f.Height) - 1) / TileSize))

	type placed struct {
		img  image.Image
		x, y int
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result []placed
		failed int
		total  int
	)
	for ty := y0; ty <= y1; ty++ {
		if ty < 0 || ty >= n {
			continue
		}
		for tx := x0; tx <= x1; tx++ {
			total++
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				img, err := tiles.Tile(ctx, f.Zoom, ((tx%n)+n)%n, ty)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failed++
					log.Printf("tile %d/%d/%d: %v", f.Zoom, tx, ty, err)
					return
				}
				result = append(result, placed{img, tx*TileSize - int(f.OriginX), ty*TileSize - int(f.OriginY)})
			}(tx, ty)
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if total > 0 && failed == total {
		return fmt.Errorf("failed to load any map tiles")
	}
	for _, p := range result {
		dc.DrawImage(p.img, p.x, p.y)
	}
	return nil
}

// drawHalo draws a dark outline under a track so light colors stay visible on the map.
// Halos of all tracks go first, so overlapping tracks don't cover each other's colors.
func drawHalo(dc *gg.Context, f Frame, segs []geo.Segment, lineW float64) {
	dc.SetLineCapRound()
	dc.SetLineJoinRound()
	for _, s := range segs {
		x1, y1 := f.Project(s.From.Lat, s.From.Lon)
		x2, y2 := f.Project(s.To.Lat, s.To.Lon)
		dc.DrawLine(x1, y1, x2, y2)
	}
	dc.SetRGBA(0, 0, 0, 0.55)
	dc.SetLineWidth(lineW + math.Max(2, lineW*0.6))
	dc.Stroke()
}

func drawTrack(dc *gg.Context, f Frame, segs []geo.Segment, st geo.Stats, lineW float64) {
	dc.SetLineCapRound()
	dc.SetLineJoinRound()
	span := st.MaxSpeedKmh - st.MinSpeedKmh
	dc.SetLineWidth(lineW)
	for _, s := range segs {
		t := 0.5
		if span > 0 {
			t = (s.SpeedKmh - st.MinSpeedKmh) / span
		}
		x1, y1 := f.Project(s.From.Lat, s.From.Lon)
		x2, y2 := f.Project(s.To.Lat, s.To.Lon)
		dc.DrawLine(x1, y1, x2, y2)
		dc.SetColor(SpeedColor(t))
		dc.Stroke()
	}
}

func drawMarkers(dc *gg.Context, f Frame, start, finish geo.Point, lineW float64) {
	r := lineW * 1.6
	marker := func(p geo.Point, fill color.Color) {
		x, y := f.Project(p.Lat, p.Lon)
		dc.DrawCircle(x, y, r)
		dc.SetColor(fill)
		dc.FillPreserve()
		dc.SetRGB(1, 1, 1)
		dc.SetLineWidth(math.Max(2, lineW*0.5))
		dc.Stroke()
	}
	marker(start, color.RGBA{30, 170, 60, 255})
	marker(finish, color.RGBA{20, 20, 20, 255})
}

func drawStats(dc *gg.Context, st geo.Stats, tracks int, size float64) {
	var lines []string
	if tracks > 1 {
		lines = append(lines, fmt.Sprintf("Заездов: %d", tracks))
	}
	lines = append(lines,
		fmt.Sprintf("Дистанция: %.2f км", st.DistanceM/1000),
		fmt.Sprintf("Макс. скорость: %.1f км/ч", st.MaxSpeedKmh),
	)
	pad := size * 0.6
	lineH := size * 1.35
	textFace := face(fontBold, size)
	smallFace := face(fontRegular, size*0.7)

	dc.SetFontFace(textFace)
	textW := 0.0
	for _, l := range lines {
		w, _ := dc.MeasureString(l)
		textW = math.Max(textW, w)
	}
	barH := size * 0.5
	labelH := size * 0.9
	boxW := textW + 2*pad
	boxH := pad + float64(len(lines))*lineH + pad*0.5 + barH + labelH + pad*0.6
	x0 := pad
	y0 := float64(dc.Height()) - pad - boxH

	dc.DrawRoundedRectangle(x0, y0, boxW, boxH, size*0.4)
	dc.SetRGBA(1, 1, 1, 0.85)
	dc.Fill()

	dc.SetRGB(0.1, 0.1, 0.1)
	y := y0 + pad
	for _, l := range lines {
		dc.DrawStringAnchored(l, x0+pad, y+lineH/2, 0, 0.35)
		y += lineH
	}

	// Speed legend: gradient bar with min/max labels.
	y += pad * 0.5
	barX, barW := x0+pad, textW
	for i := 0; i < int(barW); i++ {
		dc.SetColor(SpeedColor(float64(i) / barW))
		dc.DrawRectangle(barX+float64(i), y, 1.5, barH)
		dc.Fill()
	}
	y += barH + labelH*0.55
	dc.SetFontFace(smallFace)
	dc.SetRGB(0.2, 0.2, 0.2)
	dc.DrawStringAnchored(fmt.Sprintf("%.0f км/ч", st.MinSpeedKmh), barX, y, 0, 0.35)
	dc.DrawStringAnchored(fmt.Sprintf("%.0f км/ч", st.MaxSpeedKmh), barX+barW, y, 1, 0.35)
}

func drawAttribution(dc *gg.Context, size float64) {
	const text = "© OpenStreetMap contributors"
	dc.SetFontFace(face(fontRegular, size))
	w, _ := dc.MeasureString(text)
	pad := size * 0.4
	x := float64(dc.Width()) - w - 2*pad
	y := float64(dc.Height()) - size - 2*pad
	dc.DrawRectangle(x, y, w+2*pad, size+2*pad)
	dc.SetRGBA(1, 1, 1, 0.75)
	dc.Fill()
	dc.SetRGB(0.2, 0.2, 0.2)
	dc.DrawStringAnchored(text, x+pad, y+pad+size/2, 0, 0.35)
}
