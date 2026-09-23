package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/storage"
)

const aggregationsListLimit = 50

// menuCommands are shown in Telegram's command menu (set on startup).
var menuCommands = []tgbotapi.BotCommand{
	{Command: "races", Description: "Мои заезды"},
	{Command: "aggregations", Description: "Мои агрегации заездов"},
	{Command: "help", Description: "Как записать заезд"},
}

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
	case cmd == "aggregations" || cmd == "aggregation" || cmd == "agregation":
		return b.cmdAggregations(ctx, m)
	case strings.HasPrefix(cmd, "agr_"):
		return b.cmdAggregation(ctx, m, strings.TrimPrefix(cmd, "agr_"))
	default:
		b.reply(m.Chat.ID, "Не знаю такой команды. /help")
		return nil
	}
}

const helpText = `Я записываю велозаезды 🚴

1. Нажмите 📎 → Геопозиция → «Транслировать геопозицию» и выберите срок.
2. Катайтесь — я сохраняю точки трека.
3. Остановите трансляцию — заезд завершится.

Или просто пришлите .fit файл тренировки с часов или велокомпьютера — я создам заезд из него.

/races — список ваших заездов
/race_<id> — карта заезда с треком
/aggregations — агрегации: несколько заездов на одной карте

Под картой заезда есть кнопки:
⚙️ Настройки — сделать заезд публичным (его сможет открыть любой по /race_<id>) или приватным.
🔗 Агрегировать с… — объединить с соседним заездом на одной карте.`

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
		mark := ""
		if r.Public {
			mark = "  🌐"
		}
		fmt.Fprintf(&sb, "%s  %s%s%s\n", raceCmd(r.ID), sportIcon(r.Sport), b.raceSpan(r), mark)
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
	return b.showRace(ctx, m.Chat.ID, r, m.From.ID)
}

func (b *Bot) cmdAggregations(ctx context.Context, m *tgbotapi.Message) error {
	aggs, err := b.store.ListAggregations(ctx, m.From.ID, aggregationsListLimit)
	if err != nil {
		return err
	}
	if len(aggs) == 0 {
		b.reply(m.Chat.ID, "Агрегаций пока нет. Откройте заезд из /races и нажмите «🔗 Агрегировать с…».")
		return nil
	}
	var sb strings.Builder
	for _, a := range aggs {
		fmt.Fprintf(&sb, "%s  %s", aggCmd(a.ID), pluralRaces(a.Races))
		if a.Races > 0 {
			fmt.Fprintf(&sb, " · %s — %s", b.fmtTime(a.FirstStartedAt), b.fmtTime(a.LastPointAt))
		}
		sb.WriteString("\n")
	}
	b.reply(m.Chat.ID, sb.String())
	return nil
}

func (b *Bot) cmdAggregation(ctx context.Context, m *tgbotapi.Message, hexID string) error {
	id, err := strconv.ParseInt(hexID, 16, 64)
	if err != nil {
		b.reply(m.Chat.ID, "Неверный id агрегации. Список: /aggregations")
		return nil
	}
	a, err := b.store.GetAggregation(ctx, id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && !canUseAggregation(a, m.From.ID)) {
		b.reply(m.Chat.ID, "Агрегация не найдена. Список: /aggregations")
		return nil
	}
	if err != nil {
		return err
	}
	return b.showAggregation(ctx, m.Chat.ID, a)
}
