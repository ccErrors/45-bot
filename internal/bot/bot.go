// Package bot wires Telegram updates to race recording and rendering.
package bot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/config"
	"github.com/ccErrors/race-bot/internal/geo"
	"github.com/ccErrors/race-bot/internal/render"
	"github.com/ccErrors/race-bot/internal/storage"
)

const (
	// Telegram uses this live_period value for "until I stop sharing".
	indefiniteLivePeriod = 0x7FFFFFFF
	liveGrace            = 5 * time.Minute
	racesListLimit       = 50
	dateLayout           = "02.01.2006 15:04"
)

type Bot struct {
	api   *tgbotapi.BotAPI
	store *storage.Store
	tiles render.TileSource
	cfg   *config.Config
}

func New(api *tgbotapi.BotAPI, store *storage.Store, tiles render.TileSource, cfg *config.Config) *Bot {
	return &Bot{api: api, store: store, tiles: tiles, cfg: cfg}
}

func (b *Bot) Run(ctx context.Context) error {
	go b.janitor(ctx)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	u.AllowedUpdates = []string{"message", "edited_message"}
	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return ctx.Err()
		case upd := <-updates:
			b.handle(ctx, upd)
		}
	}
}

func (b *Bot) handle(ctx context.Context, upd tgbotapi.Update) {
	var err error
	switch {
	case upd.EditedMessage != nil && upd.EditedMessage.Location != nil:
		err = b.onLocationUpdate(ctx, upd.EditedMessage)
	case upd.Message != nil && upd.Message.Location != nil:
		err = b.onLocationStart(ctx, upd.Message)
	case upd.Message != nil && upd.Message.IsCommand():
		// Rendering can take a while; don't block location updates of other users.
		go func(m *tgbotapi.Message) {
			if err := b.onCommand(ctx, m); err != nil {
				log.Printf("command %q: %v", m.Text, err)
				b.reply(m.Chat.ID, "Что-то пошло не так, попробуйте позже.")
			}
		}(upd.Message)
	}
	if err != nil {
		log.Printf("update %d: %v", upd.UpdateID, err)
	}
}

// --- recording ---

func (b *Bot) onLocationStart(ctx context.Context, m *tgbotapi.Message) error {
	loc := m.Location
	if loc.LivePeriod == 0 {
		b.reply(m.Chat.ID, "Это обычная геопозиция. Чтобы записать заезд, отправьте «Транслировать геопозицию» 📍")
		return nil
	}
	if m.From == nil {
		return nil
	}

	active, err := b.store.ActiveRacesByUser(ctx, m.From.ID)
	if err != nil {
		return err
	}
	for _, r := range active {
		b.finish(ctx, r, "началась новая трансляция")
	}

	start := time.Unix(int64(m.Date), 0)
	r := &storage.Race{
		UserID:      m.From.ID,
		ChatID:      m.Chat.ID,
		MessageID:   m.MessageID,
		StartedAt:   start,
		LastPointAt: start,
	}
	if loc.LivePeriod != indefiniteLivePeriod {
		until := start.Add(time.Duration(loc.LivePeriod) * time.Second)
		r.LiveUntil = &until
	}
	if err := b.store.CreateRace(ctx, r); err != nil {
		return err
	}
	if err := b.addPoint(ctx, r.ID, loc, start); err != nil {
		return err
	}
	b.reply(m.Chat.ID, fmt.Sprintf("🚴 Заезд %s начат. Записываю трек, пока вы транслируете геопозицию.", raceCmd(r.ID)))
	return nil
}

func (b *Bot) onLocationUpdate(ctx context.Context, m *tgbotapi.Message) error {
	r, err := b.store.GetRaceByMessage(ctx, m.Chat.ID, m.MessageID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !r.Active() {
		return nil
	}
	// Telegram drops live_period from the location when sharing is stopped.
	if m.Location.LivePeriod == 0 {
		b.finish(ctx, r, "")
		return nil
	}
	ts := time.Now()
	if m.EditDate != 0 {
		ts = time.Unix(int64(m.EditDate), 0)
	}
	return b.addPoint(ctx, r.ID, m.Location, ts)
}

func (b *Bot) addPoint(ctx context.Context, raceID int64, loc *tgbotapi.Location, ts time.Time) error {
	if loc.HorizontalAccuracy > 0 && loc.HorizontalAccuracy > b.cfg.MaxAccuracyM {
		return nil
	}
	return b.store.AddPoint(ctx, raceID, storage.Point{
		Lat: loc.Latitude, Lon: loc.Longitude, Time: ts, Accuracy: loc.HorizontalAccuracy,
	})
}

// janitor finishes races whose live period expired or that went silent.
func (b *Bot) janitor(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		b.finishStale(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (b *Bot) finishStale(ctx context.Context, now time.Time) {
	races, err := b.store.ActiveRaces(ctx)
	if err != nil {
		log.Printf("janitor: %v", err)
		return
	}
	for _, r := range races {
		expired := r.LiveUntil != nil && now.After(r.LiveUntil.Add(liveGrace))
		idle := now.Sub(r.LastPointAt) > b.cfg.IdleTimeout
		if expired || idle {
			b.finish(ctx, r, "")
		}
	}
}

func (b *Bot) finish(ctx context.Context, r *storage.Race, reason string) {
	ok, err := b.store.FinishRace(ctx, r.ID, time.Now())
	if err != nil {
		log.Printf("finish race %d: %v", r.ID, err)
		return
	}
	if !ok {
		return // already finished concurrently
	}
	pts, err := b.points(ctx, r.ID)
	if err != nil {
		log.Printf("finish race %d: %v", r.ID, err)
		return
	}
	_, st := geo.Analyze(pts, b.cfg.MaxSpeedKmh)
	msg := fmt.Sprintf("🏁 Заезд %s завершён", raceCmd(r.ID))
	if reason != "" {
		msg += " (" + reason + ")"
	}
	msg += "\n" + summary(st)
	b.reply(r.ChatID, msg)
}

// --- commands ---

func (b *Bot) onCommand(ctx context.Context, m *tgbotapi.Message) error {
	if m.From == nil {
		return nil
	}
	cmd := m.Command()
	switch {
	case cmd == "start" || cmd == "help":
		b.reply(m.Chat.ID, helpText)
		return nil
	case cmd == "races":
		return b.cmdRaces(ctx, m)
	case strings.HasPrefix(cmd, "race_"):
		return b.cmdRace(ctx, m, strings.TrimPrefix(cmd, "race_"))
	default:
		b.reply(m.Chat.ID, "Не знаю такой команды. /help")
		return nil
	}
}

const helpText = `Я записываю велозаезды 🚴

1. Нажмите 📎 → Геопозиция → «Транслировать геопозицию» и выберите срок.
2. Катайтесь — я сохраняю точки трека.
3. Остановите трансляцию — заезд завершится.

/races — список ваших заездов
/race_<id> — карта заезда с треком`

func (b *Bot) cmdRaces(ctx context.Context, m *tgbotapi.Message) error {
	races, err := b.store.ListRaces(ctx, m.From.ID, racesListLimit)
	if err != nil {
		return err
	}
	if len(races) == 0 {
		b.reply(m.Chat.ID, "Заездов пока нет. Начните трансляцию геопозиции, чтобы записать первый.")
		return nil
	}
	var sb strings.Builder
	for _, r := range races {
		end := "идёт"
		if !r.Active() {
			end = b.fmtTime(r.LastPointAt)
		}
		fmt.Fprintf(&sb, "%s  %s — %s\n", raceCmd(r.ID), b.fmtTime(r.StartedAt), end)
	}
	b.reply(m.Chat.ID, sb.String())
	return nil
}

func (b *Bot) cmdRace(ctx context.Context, m *tgbotapi.Message, hexID string) error {
	id, err := strconv.ParseInt(hexID, 16, 64)
	if err != nil {
		b.reply(m.Chat.ID, "Неверный id заезда. Список: /races")
		return nil
	}
	r, err := b.store.GetRace(ctx, id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !canView(r, m.From.ID)) {
		b.reply(m.Chat.ID, "Заезд не найден. Список: /races")
		return nil
	}
	if err != nil {
		return err
	}
	pts, err := b.points(ctx, r.ID)
	if err != nil {
		return err
	}
	segs, st := geo.Analyze(pts, b.cfg.MaxSpeedKmh)
	if len(segs) == 0 {
		b.reply(m.Chat.ID, "Слишком мало точек для построения трека.")
		return nil
	}

	b.api.Request(tgbotapi.NewChatAction(m.Chat.ID, tgbotapi.ChatUploadPhoto))
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	img, err := render.Render(rctx, b.tiles, segs, st, render.DefaultFrameOptions)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return err
	}

	end := "идёт"
	if !r.Active() {
		end = b.fmtTime(r.LastPointAt)
	}
	photo := tgbotapi.NewPhoto(m.Chat.ID, tgbotapi.FileBytes{Name: raceCmd(r.ID)[1:] + ".jpg", Bytes: buf.Bytes()})
	photo.Caption = fmt.Sprintf("%s  %s — %s\n%s", raceCmd(r.ID), b.fmtTime(r.StartedAt), end, summary(st))
	_, err = b.api.Send(photo)
	return err
}

// --- helpers ---

// canView is the single access check for opening a race.
// For now only the owner may see it; sharing rules will extend this later.
func canView(r *storage.Race, userID int64) bool {
	return r.UserID == userID
}

func (b *Bot) points(ctx context.Context, raceID int64) ([]geo.Point, error) {
	sp, err := b.store.Points(ctx, raceID)
	if err != nil {
		return nil, err
	}
	pts := make([]geo.Point, len(sp))
	for i, p := range sp {
		pts[i] = geo.Point{Lat: p.Lat, Lon: p.Lon, Time: p.Time}
	}
	return pts, nil
}

func (b *Bot) reply(chatID int64, text string) {
	if _, err := b.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		log.Printf("send to %d: %v", chatID, err)
	}
}

func (b *Bot) fmtTime(t time.Time) string { return t.In(b.cfg.Location).Format(dateLayout) }

func raceCmd(id int64) string { return "/race_" + strconv.FormatInt(id, 16) }

func summary(st geo.Stats) string {
	return fmt.Sprintf("Дистанция: %.2f км · Время: %s · Макс.: %.1f км/ч",
		st.DistanceM/1000, fmtDuration(st.Duration), st.MaxSpeedKmh)
}

func fmtDuration(d time.Duration) string {
	m := int(math.Round(d.Minutes()))
	return fmt.Sprintf("%d:%02d", m/60, m%60)
}
