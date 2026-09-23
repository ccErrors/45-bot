package bot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/fitimport"
	"github.com/ccErrors/race-bot/internal/geo"
	"github.com/ccErrors/race-bot/internal/storage"
)

var downloadClient = &http.Client{Timeout: time.Minute}

// onDocument imports an uploaded .fit file as a finished race.
func (b *Bot) onDocument(ctx context.Context, m *tgbotapi.Message) error {
	doc := m.Document
	if m.From == nil {
		return nil
	}
	if !fitimport.IsFITName(doc.FileName) {
		b.reply(m.Chat.ID, "Я умею загружать только .fit файлы — тренировки с часов или велокомпьютера.")
		return nil
	}
	// Telegram reports the size up front: refuse big files before downloading.
	if doc.FileSize > fitimport.MaxFileSize {
		b.reply(m.Chat.ID, fitimport.ErrTooBig.Error())
		return nil
	}
	b.api.Request(tgbotapi.NewChatAction(m.Chat.ID, tgbotapi.ChatTyping))

	act, err := b.downloadFIT(ctx, doc.FileID)
	var fe *fitimport.Error
	if errors.As(err, &fe) {
		b.reply(m.Chat.ID, "Не удалось загрузить тренировку: "+fe.Error())
		return nil
	}
	if err != nil {
		return err
	}

	pts := make([]storage.Point, len(act.Points))
	for i, p := range act.Points {
		pts[i] = storage.Point{Lat: p.Lat, Lon: p.Lon, Time: p.Time}
	}
	start, end := act.Points[0].Time, act.Points[len(act.Points)-1].Time
	r, created, err := b.store.ImportRace(ctx, &storage.Race{
		UserID:      m.From.ID,
		ChatID:      m.Chat.ID,
		MessageID:   m.MessageID,
		StartedAt:   start,
		LastPointAt: end,
		FinishedAt:  &end,
		Source:      storage.SourceFIT,
		Sport:       act.Sport,
		FileSHA256:  act.SHA256,
	}, pts)
	if err != nil {
		return err
	}
	if !created {
		b.reply(m.Chat.ID, fmt.Sprintf("Этот файл уже загружен: %s", raceCmd(r.ID)))
		return b.showRace(ctx, m.Chat.ID, r, m.From.ID)
	}
	_, st := geo.Analyze(act.Points, b.cfg.MaxSpeedKmh)
	b.reply(m.Chat.ID, fmt.Sprintf("🏁 Заезд %s загружен из FIT%s\n%s", raceCmd(r.ID), sportLabel(r.Sport), summary(st)))
	return b.showRace(ctx, m.Chat.ID, r, m.From.ID)
}

func (b *Bot) downloadFIT(ctx context.Context, fileID string) (*fitimport.Activity, error) {
	// The direct URL embeds the bot token: never log it or errors that contain it.
	link, err := b.api.GetFileDirectURL(fileID)
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, errors.New("download: bad request")
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL
		}
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	return fitimport.Parse(ctx, resp.Body, time.Now()) // reads at most MaxFileSize
}

var sportIcons = map[string]string{
	"cycling":              "🚴",
	"e_biking":             "🚴",
	"running":              "🏃",
	"walking":              "🚶",
	"hiking":               "🥾",
	"swimming":             "🏊",
	"cross_country_skiing": "⛷",
	"inline_skating":       "🛼",
}

var sportNames = map[string]string{
	"cycling":              "велосипед",
	"e_biking":             "электровелосипед",
	"running":              "бег",
	"walking":              "ходьба",
	"hiking":               "поход",
	"swimming":             "плавание",
	"cross_country_skiing": "лыжи",
	"inline_skating":       "ролики",
}

// sportIcon returns an emoji prefix for the sport, or "" if unknown.
func sportIcon(sport string) string {
	if i, ok := sportIcons[sport]; ok {
		return i + " "
	}
	return ""
}

// sportLabel returns " (🏃 бег)" or "" for an unknown sport.
func sportLabel(sport string) string {
	if n, ok := sportNames[sport]; ok {
		return " (" + sportIcons[sport] + " " + n + ")"
	}
	return ""
}
