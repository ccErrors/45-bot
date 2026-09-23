package bot

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ccErrors/race-bot/internal/config"
	"github.com/ccErrors/race-bot/internal/storage"
)

func TestAccess(t *testing.T) {
	private := &storage.Race{UserID: 1}
	public := &storage.Race{UserID: 1, Public: true}
	cases := []struct {
		name       string
		r          *storage.Race
		user       int64
		view, edit bool
	}{
		{"owner private", private, 1, true, true},
		{"stranger private", private, 2, false, false},
		{"owner public", public, 1, true, true},
		{"stranger public", public, 2, true, false},
	}
	for _, c := range cases {
		if got := canView(c.r, c.user); got != c.view {
			t.Errorf("%s: canView = %v", c.name, got)
		}
		if got := canEdit(c.r, c.user); got != c.edit {
			t.Errorf("%s: canEdit = %v", c.name, got)
		}
	}
}

func TestCallbackData(t *testing.T) {
	d := callbackData(cbRaceVisibility, 0x1a, "1")
	if d != "pub:1a:1" {
		t.Fatalf("data %q", d)
	}
	action, id, arg, ok := parseCallback(d)
	if !ok || action != cbRaceVisibility || id != 0x1a || arg != "1" {
		t.Fatalf("parsed %q %x %q %v", action, id, arg, ok)
	}
	if _, id, arg, ok := parseCallback(callbackData(cbRaceSettings, 0xff, "")); !ok || id != 0xff || arg != "" {
		t.Fatal("settings callback")
	}
	for _, bad := range []string{"", "set", "set:zz"} {
		if _, _, _, ok := parseCallback(bad); ok {
			t.Errorf("%q parsed as valid", bad)
		}
	}
}

func TestSettingsViewToggles(t *testing.T) {
	r := &storage.Race{ID: 0x1a}
	text, kb := settingsView(r)
	btn := kb.InlineKeyboard[0][0]
	if !strings.Contains(text, "приватный") || *btn.CallbackData != "pub:1a:1" {
		t.Fatalf("private view: %q / %q", text, *btn.CallbackData)
	}
	r.Public = true
	text, kb = settingsView(r)
	btn = kb.InlineKeyboard[0][0]
	if !strings.Contains(text, "публичный") || *btn.CallbackData != "pub:1a:0" {
		t.Fatalf("public view: %q / %q", text, *btn.CallbackData)
	}
}

func TestPluralRaces(t *testing.T) {
	for n, want := range map[int]string{1: "1 заезд", 2: "2 заезда", 4: "4 заезда", 5: "5 заездов", 11: "11 заездов", 12: "12 заездов", 21: "21 заезд", 22: "22 заезда", 111: "111 заездов"} {
		if got := pluralRaces(n); got != want {
			t.Errorf("pluralRaces(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCallbackDataFitsTelegramLimit(t *testing.T) {
	const maxID = int64(1<<63 - 1)
	for action := range callbackHandlers {
		d := callbackData(action, maxID, strconv.FormatInt(maxID, 16))
		if len(d) > 64 {
			t.Errorf("%s: %d bytes > 64", action, len(d))
		}
		if a, id, _, ok := parseCallback(d); !ok || a != action || id != maxID {
			t.Errorf("%s: round trip failed", action)
		}
	}
}

func TestAggSettingsView(t *testing.T) {
	b := &Bot{cfg: &config.Config{Location: time.UTC}}
	t0 := time.Unix(1_700_000_000, 0)
	fin := t0.Add(time.Hour)
	a := &storage.Aggregation{ID: 3, UserID: 1}
	races := []*storage.Race{
		{ID: 0x1a, StartedAt: t0, LastPointAt: fin, FinishedAt: &fin},
		{ID: 0x1b, StartedAt: t0.Add(24 * time.Hour), LastPointAt: t0.Add(25 * time.Hour)},
	}
	text, kb := b.aggSettingsView(a, races)
	if !strings.Contains(text, "/agr_3 · 2 заезда") || !strings.Contains(text, "/race_1b") || !strings.Contains(text, "идёт") {
		t.Fatalf("text: %q", text)
	}
	// One remove button per race, then redraw+add, then delete.
	if len(kb.InlineKeyboard) != 4 {
		t.Fatalf("rows: %d", len(kb.InlineKeyboard))
	}
	if d := *kb.InlineKeyboard[1][0].CallbackData; d != "arm:3:1b" {
		t.Fatalf("remove button: %q", d)
	}
	if d := *kb.InlineKeyboard[3][0].CallbackData; d != "adel:3" {
		t.Fatalf("delete button: %q", d)
	}
}
