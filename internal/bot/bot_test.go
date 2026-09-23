package bot

import (
	"strings"
	"testing"

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
	d := callbackData(cbVisibility, 0x1a, "1")
	if d != "pub:1a:1" {
		t.Fatalf("data %q", d)
	}
	action, id, arg, ok := parseCallback(d)
	if !ok || action != cbVisibility || id != 0x1a || arg != "1" {
		t.Fatalf("parsed %q %x %q %v", action, id, arg, ok)
	}
	if _, id, arg, ok := parseCallback(callbackData(cbSettings, 0xff, "")); !ok || id != 0xff || arg != "" {
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
