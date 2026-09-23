package bot

import (
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/geo"
	"github.com/ccErrors/race-bot/internal/render"
	"github.com/ccErrors/race-bot/internal/storage"
)

// Telegram limits photo captions to 1024 characters; keep race lists short.
const captionRaceLines = 10

// showRace sends the race map. The owner also gets the settings/aggregate buttons.
func (b *Bot) showRace(ctx context.Context, chatID int64, r *storage.Race, viewerID int64) error {
	segs, err := b.segments(ctx, r.ID)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		b.reply(chatID, "Слишком мало точек для построения трека.")
		return nil
	}
	st := geo.Combine(segs)
	caption := fmt.Sprintf("%s  %s\n%s", raceCmd(r.ID), b.raceSpan(r), summary(st))
	var kb *tgbotapi.InlineKeyboardMarkup
	if canEdit(r, viewerID) {
		k := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Настройки", callbackData(cbRaceSettings, r.ID, "")),
			tgbotapi.NewInlineKeyboardButtonData("🔗 Агрегировать с…", callbackData(cbRaceAggregate, r.ID, "")),
		))
		kb = &k
	}
	return b.sendMap(ctx, chatID, [][]geo.Segment{segs}, st, raceCmd(r.ID)[1:], caption, kb)
}

// showAggregation sends all of the aggregation's tracks on one map.
func (b *Bot) showAggregation(ctx context.Context, chatID int64, a *storage.Aggregation) error {
	races, err := b.store.AggregationRaces(ctx, a.ID)
	if err != nil {
		return err
	}
	var tracks [][]geo.Segment
	for _, r := range races {
		segs, err := b.segments(ctx, r.ID)
		if err != nil {
			return err
		}
		if len(segs) > 0 {
			tracks = append(tracks, segs)
		}
	}
	if len(tracks) == 0 {
		b.reply(chatID, fmt.Sprintf("В агрегации %s нет заездов с треком.", aggCmd(a.ID)))
		return nil
	}
	st := geo.Combine(tracks...)

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s · %s\n", aggCmd(a.ID), pluralRaces(len(races)))
	for i, r := range races {
		if i == captionRaceLines {
			fmt.Fprintf(&sb, "…и ещё %d\n", len(races)-i)
			break
		}
		fmt.Fprintf(&sb, "%s  %s\n", raceCmd(r.ID), b.raceSpan(r))
	}
	sb.WriteString(summary(st))

	kb := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⚙️ Настройки", callbackData(cbAggSettings, a.ID, "")),
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить заезд", callbackData(cbAggAddPicker, a.ID, "")),
	))
	return b.sendMap(ctx, chatID, tracks, st, aggCmd(a.ID)[1:], sb.String(), &kb)
}

func (b *Bot) sendMap(ctx context.Context, chatID int64, tracks [][]geo.Segment, st geo.Stats, name, caption string, kb *tgbotapi.InlineKeyboardMarkup) error {
	b.api.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatUploadPhoto))
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	img, err := render.Render(rctx, b.tiles, tracks, st, render.DefaultFrameOptions)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return err
	}
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: name + ".jpg", Bytes: buf.Bytes()})
	photo.Caption = caption
	if kb != nil {
		photo.ReplyMarkup = *kb
	}
	_, err = b.api.Send(photo)
	return err
}

func (b *Bot) segments(ctx context.Context, raceID int64) ([]geo.Segment, error) {
	pts, err := b.points(ctx, raceID)
	if err != nil {
		return nil, err
	}
	segs, _ := geo.Analyze(pts, b.cfg.MaxSpeedKmh)
	return segs, nil
}

// raceSpan formats "start — end" (or "start — идёт" for an active race).
func (b *Bot) raceSpan(r *storage.Race) string {
	end := "идёт"
	if !r.Active() {
		end = b.fmtTime(r.LastPointAt)
	}
	return b.fmtTime(r.StartedAt) + " — " + end
}
