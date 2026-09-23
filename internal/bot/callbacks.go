package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ccErrors/race-bot/internal/storage"
)

// Callback data: "<action>:<hex id>[:<arg>]", well under Telegram's 64-byte limit.
// Buttons stay in chats forever, so never change the meaning of an existing action.
const (
	cbRaceSettings   = "set"   // race: open settings
	cbRaceVisibility = "pub"   // race, arg "1"/"0": make public/private
	cbRaceAggregate  = "agp"   // race: pick a neighbor to aggregate with
	cbAggCreate      = "agn"   // race, arg other race: create aggregation of both
	cbAggSettings    = "aset"  // aggregation: open settings
	cbAggAddPicker   = "aadd"  // aggregation: pick a race to add
	cbAggAdd         = "aput"  // aggregation, arg race: add it
	cbAggRemove      = "arm"   // aggregation, arg race: remove it
	cbAggRedraw      = "adraw" // aggregation: send the map again
	cbAggDelete      = "adel"  // aggregation: delete it (races stay)
	cbRaceDelete     = "rdel"  // race: ask to confirm deletion
	cbRaceDeleteYes  = "rdely" // race: delete it for real
	cbRaceSettingsIn = "sback" // race: show settings in place (cancel deletion)
)

// neighborCount is how many races before and after are offered for aggregation.
const neighborCount = 2

func callbackData(action string, id int64, arg string) string {
	d := action + ":" + strconv.FormatInt(id, 16)
	if arg != "" {
		d += ":" + arg
	}
	return d
}

func parseCallback(data string) (action string, id int64, arg string, ok bool) {
	parts := strings.SplitN(data, ":", 3)
	if len(parts) < 2 {
		return "", 0, "", false
	}
	id, err := strconv.ParseInt(parts[1], 16, 64)
	if err != nil {
		return "", 0, "", false
	}
	if len(parts) == 3 {
		arg = parts[2]
	}
	return parts[0], id, arg, true
}

// callback is one button press being handled.
type callback struct {
	userID int64
	chatID int64
	msgID  int
	id     int64  // race or aggregation id from the button
	arg    string // optional argument from the button
}

// errToast ends a handler with a user-facing message instead of an internal error.
type errToast string

func (e errToast) Error() string { return string(e) }

type callbackHandler func(b *Bot, ctx context.Context, c callback) (toast string, err error)

var callbackHandlers = map[string]callbackHandler{
	cbRaceSettings:   (*Bot).cbRaceSettings,
	cbRaceVisibility: (*Bot).cbRaceVisibility,
	cbRaceAggregate:  (*Bot).cbRaceAggregate,
	cbAggCreate:      (*Bot).cbAggCreate,
	cbAggSettings:    (*Bot).cbAggSettings,
	cbAggAddPicker:   (*Bot).cbAggAddPicker,
	cbAggAdd:         (*Bot).cbAggAdd,
	cbAggRemove:      (*Bot).cbAggRemove,
	cbAggRedraw:      (*Bot).cbAggRedraw,
	cbAggDelete:      (*Bot).cbAggDelete,
	cbRaceDelete:     (*Bot).cbRaceDelete,
	cbRaceDeleteYes:  (*Bot).cbRaceDeleteYes,
	cbRaceSettingsIn: (*Bot).cbRaceSettingsIn,
}

func (b *Bot) onCallback(ctx context.Context, q *tgbotapi.CallbackQuery) error {
	// Telegram shows a spinner on the button until the callback is answered.
	var toast string
	defer func() {
		if _, err := b.api.Request(tgbotapi.NewCallback(q.ID, toast)); err != nil {
			log.Printf("answer callback: %v", err)
		}
	}()

	action, id, arg, ok := parseCallback(q.Data)
	handler := callbackHandlers[action]
	if !ok || handler == nil || q.Message == nil || q.From == nil {
		toast = "Кнопка устарела"
		return nil
	}
	toast, err := handler(b, ctx, callback{
		userID: q.From.ID, chatID: q.Message.Chat.ID, msgID: q.Message.MessageID, id: id, arg: arg,
	})
	var t errToast
	if errors.As(err, &t) {
		toast = string(t)
		return nil
	}
	if err != nil {
		toast = "Ошибка, попробуйте позже"
	}
	return err
}

// --- loading with access checks ---

func (b *Bot) ownRace(ctx context.Context, id, userID int64) (*storage.Race, error) {
	r, err := b.store.GetRace(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, errToast("Заезд не найден")
	}
	if err != nil {
		return nil, err
	}
	if !canEdit(r, userID) {
		return nil, errToast("Это доступно только автору заезда")
	}
	return r, nil
}

func (b *Bot) ownAggregation(ctx context.Context, id, userID int64) (*storage.Aggregation, []*storage.Race, error) {
	a, err := b.store.GetAggregation(ctx, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, errToast("Агрегация не найдена")
	}
	if err != nil {
		return nil, nil, err
	}
	if !canUseAggregation(a, userID) {
		return nil, nil, errToast("Это доступно только автору агрегации")
	}
	races, err := b.store.AggregationRaces(ctx, a.ID)
	return a, races, err
}

func parseHexArg(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 16, 64)
	if err != nil {
		return 0, errToast("Кнопка устарела")
	}
	return id, nil
}

// --- race settings ---

func (b *Bot) cbRaceSettings(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	text, kb := settingsView(r)
	return "", b.sendWithKeyboard(c.chatID, text, kb)
}

func (b *Bot) cbRaceVisibility(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	public := c.arg == "1"
	// The button carries the target state, so a repeated tap is a no-op.
	if r.Public != public {
		if err := b.store.SetPublic(ctx, r.ID, public); err != nil {
			return "", err
		}
		r.Public = public
	}
	text, kb := settingsView(r)
	b.editWithKeyboard(c.chatID, c.msgID, text, kb)
	if public {
		return "Заезд теперь публичный", nil
	}
	return "Заезд теперь приватный", nil
}

// settingsView renders the settings message for a race.
func settingsView(r *storage.Race) (string, tgbotapi.InlineKeyboardMarkup) {
	cmd := raceCmd(r.ID)
	text := "⚙️ Настройки заезда " + cmd + "\n\nВидимость: "
	var btn tgbotapi.InlineKeyboardButton
	if r.Public {
		text += "🌐 публичный — любой может открыть его командой " + cmd
		btn = tgbotapi.NewInlineKeyboardButtonData("🔒 Сделать приватным", callbackData(cbRaceVisibility, r.ID, "0"))
	} else {
		text += "🔒 приватный — виден только вам"
		btn = tgbotapi.NewInlineKeyboardButtonData("🌐 Сделать публичным", callbackData(cbRaceVisibility, r.ID, "1"))
	}
	return text, tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btn),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить заезд", callbackData(cbRaceDelete, r.ID, ""))),
	)
}

func (b *Bot) cbRaceSettingsIn(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	text, kb := settingsView(r)
	b.editWithKeyboard(c.chatID, c.msgID, text, kb)
	return "", nil
}

// --- deleting a race ---

func (b *Bot) cbRaceDelete(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	aggs, err := b.store.RaceAggregations(ctx, r.ID)
	if err != nil {
		return "", err
	}
	text, kb := b.deleteConfirmView(r, aggs)
	b.editWithKeyboard(c.chatID, c.msgID, text, kb)
	return "", nil
}

func (b *Bot) deleteConfirmView(r *storage.Race, aggs []storage.AggregationSummary) (string, tgbotapi.InlineKeyboardMarkup) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🗑 Удалить заезд %s (%s)?\n\nТрек удалится безвозвратно.", raceCmd(r.ID), b.raceSpan(r))
	if r.Active() {
		sb.WriteString(" Заезд ещё записывается — новые точки больше сохраняться не будут.")
	}
	var kept, dropped []string
	for _, a := range aggs {
		if a.Races <= 2 {
			dropped = append(dropped, aggCmd(a.ID))
		} else {
			kept = append(kept, aggCmd(a.ID))
		}
	}
	if len(kept) > 0 {
		fmt.Fprintf(&sb, "\n\nЗаезд уберётся из агрегаций: %s.", strings.Join(kept, ", "))
	}
	if len(dropped) > 0 {
		fmt.Fprintf(&sb, "\n\nВ агрегациях %s останется меньше 2 заездов — они тоже удалятся (другие заезды не пострадают).", strings.Join(dropped, ", "))
	}
	return sb.String(), tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("✅ Да, удалить", callbackData(cbRaceDeleteYes, r.ID, "")),
		tgbotapi.NewInlineKeyboardButtonData("↩️ Отмена", callbackData(cbRaceSettingsIn, r.ID, "")),
	))
}

func (b *Bot) cbRaceDeleteYes(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	deletedAggs, err := b.store.DeleteRace(ctx, r.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return "Заезд уже удалён", nil
	}
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("🗑 Заезд %s удалён.", raceCmd(r.ID))
	if len(deletedAggs) > 0 {
		names := make([]string, len(deletedAggs))
		for i, id := range deletedAggs {
			names[i] = aggCmd(id)
		}
		text += " Удалены агрегации: " + strings.Join(names, ", ") + "."
	}
	b.editText(c.chatID, c.msgID, text)
	return "Заезд удалён", nil
}

// --- creating aggregations ---

func (b *Bot) cbRaceAggregate(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	prev, next, err := b.store.NeighborRaces(ctx, c.userID, r, r, neighborCount, map[int64]bool{r.ID: true})
	if err != nil {
		return "", err
	}
	if len(prev)+len(next) == 0 {
		return "Других заездов пока нет", nil
	}
	text := "🔗 С каким заездом объединить " + raceCmd(r.ID) + "?\n\n⬅️ — предыдущие, ➡️ — следующие."
	kb := b.neighborKeyboard(prev, next, func(o *storage.Race) string {
		return callbackData(cbAggCreate, r.ID, strconv.FormatInt(o.ID, 16))
	})
	return "", b.sendWithKeyboard(c.chatID, text, kb)
}

func (b *Bot) cbAggCreate(ctx context.Context, c callback) (string, error) {
	r, err := b.ownRace(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	otherID, err := parseHexArg(c.arg)
	if err != nil {
		return "", err
	}
	other, err := b.ownRace(ctx, otherID, c.userID)
	if err != nil {
		return "", err
	}
	aggID, err := b.store.CreateAggregation(ctx, c.userID, []int64{r.ID, other.ID})
	if err != nil {
		return "", err
	}
	// Replace the picker so it can't create a second aggregation by accident.
	b.editText(c.chatID, c.msgID, fmt.Sprintf("✅ Создана агрегация %s: %s + %s", aggCmd(aggID), raceCmd(r.ID), raceCmd(other.ID)))
	a, err := b.store.GetAggregation(ctx, aggID)
	if err != nil {
		return "", err
	}
	return "Агрегация создана", b.showAggregation(ctx, c.chatID, a)
}

// neighborKeyboard lists earlier races (oldest first) then later ones, one per row.
func (b *Bot) neighborKeyboard(prev, next []*storage.Race, data func(*storage.Race) string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := len(prev) - 1; i >= 0; i-- {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
			"⬅️ "+raceCmd(prev[i].ID)+" · "+b.fmtShort(prev[i].StartedAt), data(prev[i]))))
	}
	for _, r := range next {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
			"➡️ "+raceCmd(r.ID)+" · "+b.fmtShort(r.StartedAt), data(r))))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// --- aggregation settings ---

func (b *Bot) cbAggSettings(ctx context.Context, c callback) (string, error) {
	a, races, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	text, kb := b.aggSettingsView(a, races)
	return "", b.sendWithKeyboard(c.chatID, text, kb)
}

func (b *Bot) aggSettingsView(a *storage.Aggregation, races []*storage.Race) (string, tgbotapi.InlineKeyboardMarkup) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "⚙️ Агрегация %s · %s\n\n", aggCmd(a.ID), pluralRaces(len(races)))
	for _, r := range races {
		fmt.Fprintf(&sb, "%s  %s\n", raceCmd(r.ID), b.raceSpan(r))
	}
	sb.WriteString("\n❌ — убрать заезд из агрегации (сам заезд останется в /races).")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, r := range races {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(
			"❌ "+raceCmd(r.ID)+" · "+b.fmtShort(r.StartedAt),
			callbackData(cbAggRemove, a.ID, strconv.FormatInt(r.ID, 16)))))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Перерисовать", callbackData(cbAggRedraw, a.ID, "")),
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить заезд", callbackData(cbAggAddPicker, a.ID, "")),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить агрегацию", callbackData(cbAggDelete, a.ID, "")),
		),
	)
	return sb.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) cbAggAddPicker(ctx context.Context, c callback) (string, error) {
	a, races, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	if len(races) == 0 {
		return "В агрегации нет заездов", nil
	}
	members := make(map[int64]bool, len(races))
	for _, r := range races {
		members[r.ID] = true
	}
	prev, next, err := b.store.NeighborRaces(ctx, c.userID, races[0], races[len(races)-1], neighborCount, members)
	if err != nil {
		return "", err
	}
	if len(prev)+len(next) == 0 {
		return "Больше нечего добавить", nil
	}
	text := "➕ Какой заезд добавить в " + aggCmd(a.ID) + "?\n\n⬅️ — раньше первого заезда, ➡️ — позже последнего."
	kb := b.neighborKeyboard(prev, next, func(r *storage.Race) string {
		return callbackData(cbAggAdd, a.ID, strconv.FormatInt(r.ID, 16))
	})
	return "", b.sendWithKeyboard(c.chatID, text, kb)
}

func (b *Bot) cbAggAdd(ctx context.Context, c callback) (string, error) {
	a, _, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	raceID, err := parseHexArg(c.arg)
	if err != nil {
		return "", err
	}
	r, err := b.ownRace(ctx, raceID, c.userID)
	if err != nil {
		return "", err
	}
	if err := b.store.AddAggregationRace(ctx, a.ID, r.ID); err != nil {
		return "", err
	}
	b.editText(c.chatID, c.msgID, fmt.Sprintf("✅ %s добавлен в %s", raceCmd(r.ID), aggCmd(a.ID)))
	return "Заезд добавлен", b.showAggregation(ctx, c.chatID, a)
}

func (b *Bot) cbAggRemove(ctx context.Context, c callback) (string, error) {
	a, races, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	raceID, err := parseHexArg(c.arg)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, r := range races {
		if r.ID == raceID {
			idx = i
		}
	}
	if idx < 0 {
		return "Этого заезда уже нет в агрегации", nil
	}
	if len(races) <= 2 {
		return "В агрегации должно остаться минимум 2 заезда. Можно удалить её целиком.", nil
	}
	if err := b.store.RemoveAggregationRace(ctx, a.ID, raceID); err != nil {
		return "", err
	}
	races = append(races[:idx], races[idx+1:]...)
	text, kb := b.aggSettingsView(a, races)
	b.editWithKeyboard(c.chatID, c.msgID, text, kb)
	return raceCmd(raceID) + " убран. Нажмите «Перерисовать», чтобы увидеть карту", nil
}

func (b *Bot) cbAggRedraw(ctx context.Context, c callback) (string, error) {
	a, _, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	return "", b.showAggregation(ctx, c.chatID, a)
}

func (b *Bot) cbAggDelete(ctx context.Context, c callback) (string, error) {
	a, _, err := b.ownAggregation(ctx, c.id, c.userID)
	if err != nil {
		return "", err
	}
	if err := b.store.DeleteAggregation(ctx, a.ID); err != nil {
		return "", err
	}
	b.editText(c.chatID, c.msgID, fmt.Sprintf("🗑 Агрегация %s удалена. Заезды остались в /races.", aggCmd(a.ID)))
	return "Агрегация удалена", nil
}

// --- message helpers ---

func (b *Bot) sendWithKeyboard(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = kb
	_, err := b.api.Send(msg)
	return err
}

// editWithKeyboard updates a message in place. Telegram rejects edits that change
// nothing ("message is not modified"), which happens on repeated taps; that's harmless.
func (b *Bot) editWithKeyboard(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	b.api.Request(tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, kb))
}

// editText replaces a message's text and drops its buttons.
func (b *Bot) editText(chatID int64, msgID int, text string) {
	b.api.Request(tgbotapi.NewEditMessageText(chatID, msgID, text))
}
