package render

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const TileSize = 256

// TileSource provides 256x256 Web Mercator map tiles.
type TileSource interface {
	Tile(ctx context.Context, z, x, y int) (image.Image, error)
}

// HTTPTiles downloads tiles from a slippy-map server and caches them on disk.
type HTTPTiles struct {
	URL       string // template with {z}, {x}, {y}
	UserAgent string
	CacheDir  string // empty disables caching
	Client    *http.Client
	sem       chan struct{}
}

func NewHTTPTiles(url, userAgent, cacheDir string) *HTTPTiles {
	return &HTTPTiles{
		URL:       url,
		UserAgent: userAgent,
		CacheDir:  cacheDir,
		Client:    &http.Client{Timeout: 20 * time.Second},
		// OSM tile usage policy: keep the number of parallel downloads low.
		sem: make(chan struct{}, 4),
	}
}

func (t *HTTPTiles) Tile(ctx context.Context, z, x, y int) (image.Image, error) {
	var cachePath string
	if t.CacheDir != "" {
		cachePath = filepath.Join(t.CacheDir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".img")
		if data, err := os.ReadFile(cachePath); err == nil {
			if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
				return img, nil
			}
		}
	}

	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	data, err := t.download(ctx, z, x, y)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode tile %d/%d/%d: %w", z, x, y, err)
	}
	if cachePath != "" {
		writeAtomic(cachePath, data)
	}
	return img, nil
}

func (t *HTTPTiles) download(ctx context.Context, z, x, y int) ([]byte, error) {
	url := strings.NewReplacer(
		"{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y),
	).Replace(t.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", t.UserAgent)
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tile %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// writeAtomic is best effort: a failed cache write only costs a re-download.
func writeAtomic(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tile-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
	}
}
