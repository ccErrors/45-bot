// Package fitimport validates an uploaded .fit file and extracts its GPS track.
//
// A FIT file is plain binary data that is only parsed, never executed, so the
// risk is a crafted file that crashes or exhausts the parser. Defences, in order:
// hard size limit, header/size consistency, CRC of header and data (done by the
// decoder), panic recovery and a decode timeout, file type must be "activity",
// and plausibility checks on every extracted point. The file itself is never
// stored — only the extracted points and the file's SHA-256 (for dedupe).
package fitimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/typedef"

	"github.com/ccErrors/race-bot/internal/geo"
)

const (
	MaxFileSize   = 10 << 20 // 10 MB: a 10-hour 1 Hz ride is ~2 MB
	MaxPoints     = 100_000  // ~28 hours at 1 Hz
	decodeTimeout = 10 * time.Second
)

// fitEpoch is the zero of FIT timestamps (1989-12-31 00:00 UTC); anything
// earlier cannot come from a real device.
var fitEpoch = time.Date(1989, 12, 31, 0, 0, 0, 0, time.UTC)

// Error is a validation failure; its message is safe to show to the user.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

var (
	ErrTooBig      = &Error{"Файл слишком большой (максимум 10 МБ)."}
	ErrNotFIT      = &Error{"Это не FIT-файл."}
	ErrCorrupt     = &Error{"Файл повреждён: не сходится контрольная сумма или структура."}
	ErrNotActivity = &Error{"Это не тренировка (activity): курсы, настройки и мониторинг не поддерживаются."}
	ErrNoGPS       = &Error{"В тренировке нет GPS-трека — нечего рисовать."}
	ErrTooMany     = &Error{"Слишком много точек в треке."}
	ErrBadData     = &Error{"В файле некорректные данные (время или координаты)."}
)

type Activity struct {
	Sport  string // FIT sport name: "cycling", "running", ...; "" if unknown
	Points []geo.Point
	SHA256 string // hex digest of the file, for duplicate detection
}

// Parse reads at most MaxFileSize bytes from r and extracts the activity.
// now bounds timestamps from above (a file can't be from the future).
func Parse(ctx context.Context, r io.Reader, now time.Time) (*Activity, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, ErrTooBig
	}
	if err := checkHeader(data); err != nil {
		return nil, err
	}
	act, err := decode(ctx, data, now)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	act.SHA256 = hex.EncodeToString(sum[:])
	return act, nil
}

// checkHeader verifies the FIT header before handing bytes to the decoder:
// header size 12 or 14, ".FIT" signature, and header + data + CRC == file size.
// The last check also rejects chained FIT files and trailing garbage.
func checkHeader(data []byte) error {
	if len(data) < 12 {
		return ErrNotFIT
	}
	hsize := int(data[0])
	if (hsize != 12 && hsize != 14) || len(data) < hsize || string(data[8:12]) != ".FIT" {
		return ErrNotFIT
	}
	dataSize := int64(binary.LittleEndian.Uint32(data[4:8]))
	if dataSize == 0 || int64(hsize)+dataSize+2 != int64(len(data)) {
		return ErrCorrupt
	}
	return nil
}

func decode(ctx context.Context, data []byte, now time.Time) (act *Activity, err error) {
	defer func() {
		// The decoder is memory-safe Go, but a crafted file must never take the bot down.
		if p := recover(); p != nil {
			act, err = nil, ErrCorrupt
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, decodeTimeout)
	defer cancel()

	fit, err := decoder.New(bytes.NewReader(data)).DecodeWithContext(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		if errors.Is(err, decoder.ErrNotFITFile) {
			return nil, ErrNotFIT
		}
		return nil, ErrCorrupt
	}

	a := filedef.NewActivity(fit.Messages...)
	if a.FileId.Type != typedef.FileActivity {
		return nil, ErrNotActivity
	}
	if len(a.Records) > MaxPoints {
		return nil, ErrTooMany
	}

	out := &Activity{}
	if len(a.Sessions) > 0 && a.Sessions[0].Sport != typedef.SportInvalid {
		out.Sport = sportName(a.Sessions[0].Sport)
	}
	maxTime := now.Add(24 * time.Hour) // tolerate a wrong device clock/timezone
	for _, rec := range a.Records {
		lat, lon := rec.PositionLatDegrees(), rec.PositionLongDegrees()
		if math.IsNaN(lat) || math.IsNaN(lon) {
			continue // records without a GPS fix (e.g. indoor start, tunnels)
		}
		if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
			return nil, ErrBadData
		}
		ts := rec.Timestamp
		if ts.Before(fitEpoch) || ts.After(maxTime) {
			return nil, ErrBadData
		}
		out.Points = append(out.Points, geo.Point{Lat: lat, Lon: lon, Time: ts})
	}
	if len(out.Points) < 2 {
		return nil, ErrNoGPS
	}
	return out, nil
}

// sportName turns typedef.Sport into a stable lowercase name ("cycling", "running").
func sportName(s typedef.Sport) string {
	name := s.String()
	if strings.Contains(name, "(") { // unknown value, e.g. "SportInvalid(123)"
		return ""
	}
	return strings.ToLower(name)
}

// IsFITName reports whether a Telegram document looks like a FIT file by name.
func IsFITName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".fit")
}

// String is for logs.
func (a *Activity) String() string {
	return fmt.Sprintf("%s, %d points, sha256 %s", a.Sport, len(a.Points), a.SHA256[:12])
}
