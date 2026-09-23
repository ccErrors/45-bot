package fitimport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/ccErrors/race-bot/internal/geo"
)

var now = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func samplePoints(t *testing.T) []geo.Point {
	f, err := os.Open("../../testdata/ride.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pts, err := geo.ReadCSV(f)
	if err != nil {
		t.Fatal(err)
	}
	return pts
}

// buildFIT encodes points as a FIT file of the given type and sport.
// noFix adds a leading record without position (like a watch before GPS lock).
func buildFIT(t *testing.T, fileType typedef.File, sport typedef.Sport, pts []geo.Point) []byte {
	t.Helper()
	a := filedef.NewActivity()
	a.FileId.SetType(fileType).SetManufacturer(typedef.ManufacturerDevelopment).SetTimeCreated(pts[0].Time)
	a.Records = append(a.Records, mesgdef.NewRecord(nil).SetTimestamp(pts[0].Time.Add(-time.Second)))
	for _, p := range pts {
		a.Records = append(a.Records, mesgdef.NewRecord(nil).
			SetTimestamp(p.Time).SetPositionLatDegrees(p.Lat).SetPositionLongDegrees(p.Lon))
	}
	a.Sessions = append(a.Sessions, mesgdef.NewSession(nil).SetSport(sport).SetStartTime(pts[0].Time).SetTimestamp(pts[len(pts)-1].Time))
	fit := a.ToFIT(nil)
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(&fit); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseValid(t *testing.T) {
	pts := samplePoints(t)
	data := buildFIT(t, typedef.FileActivity, typedef.SportCycling, pts)
	act, err := Parse(context.Background(), bytes.NewReader(data), now)
	if err != nil {
		t.Fatal(err)
	}
	if act.Sport != "cycling" {
		t.Errorf("sport %q", act.Sport)
	}
	if len(act.Points) != len(pts) { // the record without a fix is skipped
		t.Fatalf("points %d, want %d", len(act.Points), len(pts))
	}
	// Semicircle encoding keeps ~1 cm precision.
	if d := geo.Distance(act.Points[10], pts[10]); d > 0.05 || !act.Points[10].Time.Equal(pts[10].Time) {
		t.Errorf("point mismatch: %.3f m, %s vs %s", d, act.Points[10].Time, pts[10].Time)
	}
	if len(act.SHA256) != 64 {
		t.Errorf("sha256 %q", act.SHA256)
	}
	again, _ := Parse(context.Background(), bytes.NewReader(data), now)
	if again.SHA256 != act.SHA256 {
		t.Error("sha256 is not stable")
	}
}

func TestParseRejects(t *testing.T) {
	pts := samplePoints(t)
	valid := buildFIT(t, typedef.FileActivity, typedef.SportCycling, pts)
	flip := func(i int) []byte { d := bytes.Clone(valid); d[i] ^= 0xFF; return d }
	course := buildFIT(t, typedef.FileCourse, typedef.SportCycling, pts)
	future := make([]geo.Point, len(pts))
	for i, p := range pts {
		p.Time = now.Add(48*time.Hour + time.Duration(i)*time.Second)
		future[i] = p
	}

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrNotFIT},
		{"text file", []byte("MZ\x90\x00 this is definitely not a FIT file at all"), ErrNotFIT},
		{"wrong signature", append(bytes.Clone(valid[:8]), append([]byte(".EXE"), valid[12:]...)...), ErrNotFIT},
		{"truncated", valid[:len(valid)/2], ErrCorrupt},
		{"trailing garbage", append(bytes.Clone(valid), 0xDE, 0xAD), ErrCorrupt},
		{"flipped data byte", flip(len(valid) / 2), ErrCorrupt},
		{"flipped crc", flip(len(valid) - 1), ErrCorrupt},
		{"too big", make([]byte, MaxFileSize+1), ErrTooBig},
		{"course, not activity", course, ErrNotActivity},
		{"timestamps in the future", buildFIT(t, typedef.FileActivity, typedef.SportCycling, future), ErrBadData},
		{"no gps", buildFIT(t, typedef.FileActivity, typedef.SportCycling, pts[:1]), ErrNoGPS},
	}
	for _, c := range cases {
		_, err := Parse(context.Background(), bytes.NewReader(c.data), now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// Every single-byte corruption must be rejected cleanly (no panic, no bogus track).
func TestParseEveryByteFlip(t *testing.T) {
	pts := samplePoints(t)[:40]
	valid := buildFIT(t, typedef.FileActivity, typedef.SportCycling, pts)
	for i := range valid {
		d := bytes.Clone(valid)
		d[i] ^= 0x55
		if _, err := Parse(context.Background(), bytes.NewReader(d), now); err == nil {
			t.Fatalf("byte %d flipped: accepted", i)
		}
	}
}

// FIT_SAMPLE=/path/to/file.fit go test ./internal/fitimport -run Sample -v
func TestParseSampleFile(t *testing.T) {
	path := os.Getenv("FIT_SAMPLE")
	if path == "" {
		t.Skip("set FIT_SAMPLE to a real .fit file")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	act, err := Parse(context.Background(), f, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	segs, st := geo.Analyze(act.Points, 120)
	t.Logf("%s; %d segments, %.2f km, max %.1f km/h, %s", act, len(segs), st.DistanceM/1000, st.MaxSpeedKmh, st.Duration)
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("\x0e\x20\xa6\x52\x10\x00\x00\x00.FIT\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		Parse(context.Background(), bytes.NewReader(data), now) // must not panic or hang
	})
}
