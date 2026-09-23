package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/bot"
	"github.com/ccErrors/race-bot/internal/config"
	"github.com/ccErrors/race-bot/internal/render"
	"github.com/ccErrors/race-bot/internal/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	store, err := storage.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("authorized as @%s", api.Self.UserName)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tiles := render.NewHTTPTiles(cfg.TileURL, cfg.TileUserAgent, cfg.TileCacheDir)
	if err := bot.New(api, store, tiles, cfg).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
