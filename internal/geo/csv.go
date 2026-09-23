package geo

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"
)

// ReadCSV parses a track from "lat,lon,unix_ts" lines; lines starting with # are comments.
func ReadCSV(r io.Reader) ([]Point, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = 3
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	pts := make([]Point, 0, len(rows))
	for i, row := range rows {
		lat, err1 := strconv.ParseFloat(row[0], 64)
		lon, err2 := strconv.ParseFloat(row[1], 64)
		ts, err3 := strconv.ParseInt(row[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("record %d: bad number in %q", i+1, row)
		}
		pts = append(pts, Point{Lat: lat, Lon: lon, Time: time.Unix(ts, 0)})
	}
	return pts, nil
}
