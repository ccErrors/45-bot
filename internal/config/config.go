// Package config reads bot settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	TelegramToken string
	DBPath        string
	TileCacheDir  string
	TileURL       string
	TileUserAgent string
	Location      *time.Location
	IdleTimeout   time.Duration
	MaxAccuracyM  float64
	MaxSpeedKmh   float64
}

func Load() (*Config, error) {
	c := &Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		DBPath:        env("DB_PATH", "data/bot.db"),
		TileCacheDir:  env("TILE_CACHE_DIR", "data/tiles"),
		TileURL:       env("TILE_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
		TileUserAgent: env("TILE_USER_AGENT", "race-bot/1.0"),
	}
	if c.TelegramToken == "" {
		return nil, fmt.Errorf("TELEGRAM_TOKEN is required")
	}

	var err error
	if c.Location, err = time.LoadLocation(env("TZ", "Europe/Moscow")); err != nil {
		return nil, fmt.Errorf("TZ: %w", err)
	}
	if c.IdleTimeout, err = time.ParseDuration(env("IDLE_TIMEOUT", "2h")); err != nil {
		return nil, fmt.Errorf("IDLE_TIMEOUT: %w", err)
	}
	if c.MaxAccuracyM, err = strconv.ParseFloat(env("MAX_ACCURACY_M", "100"), 64); err != nil {
		return nil, fmt.Errorf("MAX_ACCURACY_M: %w", err)
	}
	if c.MaxSpeedKmh, err = strconv.ParseFloat(env("MAX_SPEED_KMH", "120"), 64); err != nil {
		return nil, fmt.Errorf("MAX_SPEED_KMH: %w", err)
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
