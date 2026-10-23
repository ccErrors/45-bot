package bot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/geo"
	"github.com/ccErrors/race-bot/internal/render"
	"github.com/ccErrors/race-bot/internal/storage"
)

const (
	heatmapsListLimit = 50
	// Telegram rejects photos over 10 MB; the full-size file goes as a document anyway.
	maxPhotoBytes = 10 << 20
)

// zone is a heatmap zone: the user's races whose tracks come closer than
// geo.ZoneRadiusM to each other (transitively). Built on the fly from the
// races, never stored, so it always reflects the current set of races.
type zone struct {
	races  []*storage.Race // oldest first
	tracks [][]geo.Segment // tracks[i] is the track of races[i]
}

// id names the zone in /heat_<hex>: its smallest race id. Any race of the zone
// opens it, so old links keep working when a new race merges two zones.
func (z zone) id() int64 {
	id := z.races[0].ID
	for _, r := range z.races {
		id = min(id, r.ID)
	}
	return id
}

func (z zone) lastPointAt() time.Time {
	var t time.Time
	for _, r := range z.races {
		if r.LastPointAt.After(t) {
			t = r.LastPointAt
		}
	}
	return t
}

func (z zone) has(raceID int64) bool {
	return slices.ContainsFunc(z.races, func(r *storage.Race) bool { return r.ID == raceID })
}

func heatCmd(id int64) string { return "/heat_" + strconv.FormatInt(id, 16) }

// userZones groups all of the user's races with a track into zones, biggest
// first, then most recently ridden. Only the user's own races are ever used:
// heatmaps are private and can't include other people's tracks.
func (b *Bot) userZones(ctx context.Context, userID int64) ([]zone, error) {
	races, err := b.store.UserRaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	tracks := make([][]geo.Segment, len(races))
	for i, r := range races {
		if tracks[i], err = b.segments(ctx, r.ID); err != nil {
			return nil, err
		}
	}
	var zones []zone
	for _, idx := range geo.Zones(tracks, geo.ZoneRadiusM) {
		var z zone
		for _, i := range idx {
			z.races = append(z.races, races[i])
			z.tracks = append(z.tracks, tracks[i])
		}
		zones = append(zones, z)
	}
	slices.SortStableFunc(zones, func(a, b zone) int {
		if len(a.races) != len(b.races) {
			return len(b.races) - len(a.races)
		}
		return b.lastPointAt().Compare(a.lastPointAt())
	})
	return zones, nil
}

func (b *Bot) cmdHeatmaps(ctx context.Context, m *tgbotapi.Message) error {
	zones, err := b.userZones(ctx, m.From.ID)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		b.reply(m.Chat.ID, "Заездов с треком пока нет — тепловую карту не из чего строить. Запишите заезд или пришлите .fit файл.")
		return nil
	}
	b.reply(m.Chat.ID, b.heatmapsList(zones))
	return nil
}

func (b *Bot) heatmapsList(zones []zone) string {
	var sb strings.Builder
	sb.WriteString("🔥 Тепловые карты — места, где ваши заезды проходят ближе 1 км друг к другу:\n\n")
	for i, z := range zones {
		if i == heatmapsListLimit {
			fmt.Fprintf(&sb, "…и ещё %d\n", len(zones)-i)
			break
		}
		fmt.Fprintf(&sb, "%s  %s · %s\n", heatCmd(z.id()), pluralRaces(len(z.races)), b.zoneSpan(z))
	}
	return sb.String()
}

func (b *Bot) cmdHeatmap(ctx context.Context, m *tgbotapi.Message, hexID string) error {
	id, err := strconv.ParseInt(hexID, 16, 64)
	if err != nil {
		b.reply(m.Chat.ID, "Неверный id тепловой карты. Список: /heatmap")
		return nil
	}
	return b.showHeatmapOf(ctx, m.Chat.ID, m.From.ID, id)
}

func (b *Bot) cbRaceHeatmap(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	return "", b.showHeatmapOf(ctx, c.chatID, c.userID, r.ID)
}

// showHeatmapOf sends the heatmap of the user's zone that contains raceID.
// Someone else's race is in none of the user's zones, so it's "not found".
func (b *Bot) showHeatmapOf(ctx context.Context, chatID, userID, raceID int64) error {
	b.api.Request(tgbotapi.NewChatAction(chatID, tgbotapi.ChatUploadPhoto))
	zones, err := b.userZones(ctx, userID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(zones, func(z zone) bool { return z.has(raceID) })
	if i < 0 {
		b.reply(chatID, "Тепловая карта не найдена. Список: /heatmap")
		return nil
	}
	return b.sendHeatmap(ctx, chatID, zones[i])
}

// sendHeatmap renders the zone at double resolution and sends it twice: as a
// photo to look at in the chat (Telegram downscales it) and as a file with
// every pixel, to download.
func (b *Bot) sendHeatmap(ctx context.Context, chatID int64, z zone) error {
	st := geo.Combine(z.tracks...)
	rctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	img, err := render.RenderHeatmap(rctx, b.tiles, z.tracks, st, render.HeatmapFrameOptions)
	if err != nil {
		return err
	}
	full, err := encodeJPEG(img, 92)
	if err != nil {
		return err
	}
	photoBytes := full
	if len(photoBytes) > maxPhotoBytes {
		if photoBytes, err = encodeJPEG(img, 75); err != nil {
			return err
		}
	}

	name := heatCmd(z.id())[1:]
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileBytes{Name: name + ".jpg", Bytes: photoBytes})
	photo.Caption = b.heatmapCaption(z, st)
	if _, err := b.api.Send(photo); err != nil {
		return err
	}
	bounds := img.Bounds()
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FileBytes{Name: name + ".jpg", Bytes: full})
	doc.Caption = fmt.Sprintf("🔥 %s в полном разрешении, %d×%d", heatCmd(z.id()), bounds.Dx(), bounds.Dy())
	_, err = b.api.Send(doc)
	return err
}

func (b *Bot) heatmapCaption(z zone, st geo.Stats) string {
	return fmt.Sprintf("🔥 Тепловая карта %s · %s\n%s\n%s",
		heatCmd(z.id()), pluralRaces(len(z.races)), b.zoneSpan(z), summary(st))
}

// zoneSpan formats "first start — last point" over all races of the zone.
func (b *Bot) zoneSpan(z zone) string {
	return b.fmtTime(z.races[0].StartedAt) + " — " + b.fmtTime(z.lastPointAt())
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes(), err
}
