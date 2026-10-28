package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Theyashsawarkar/prasar/internal/app"
	"github.com/Theyashsawarkar/prasar/internal/fake"
	"github.com/Theyashsawarkar/prasar/internal/linkedin"
	"github.com/Theyashsawarkar/prasar/internal/post"
)

func newApp(t *testing.T) (*app.App, *fake.LinkedIn, *[]string) {
	t.Setenv("PRASAR_NO_KEYRING", "1")
	t.Setenv("NO_NERD_FONT", "1")
	t.Setenv("TMUX", "")
	f := fake.New()
	t.Cleanup(f.Close)
	t.Setenv("PRASAR_LINKEDIN_URL", f.URL)
	h := t.TempDir()
	a, err := app.Open(app.Paths{Config: filepath.Join(h, "c"), Data: filepath.Join(h, "d"), State: filepath.Join(h, "s")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	var opened []string
	a.Opener = func(u string) error { opened = append(opened, u); return nil }
	a.Clipboard = func(string) error { return nil }
	a.Notifier = func(string, string, string) bool { return false }
	a.Tokens.Save(linkedin.Token{Access: "fake-token", Sub: "fake-member", Name: "Asha Demo", Expires: time.Now().Add(40 * 24 * time.Hour).Unix()})
	return a, f, &opened
}

func screen(m *Model) string { return ansi.Strip(m.View()) }

func size(m *Model) { m.Update(tea.WindowSizeMsg{Width: 150, Height: 40}) }

func typeText(m *Model, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

// run feeds a command's message back, like Bubble Tea does.
func run(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 5; i++ {
		msg := cmd()
		if msg == nil {
			return
		}
		if _, ok := msg.(tea.BatchMsg); ok {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func key(m *Model, k string) tea.Cmd {
	var msg tea.KeyMsg
	switch k {
	case "ctrl+s":
		msg = tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+t":
		msg = tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+x":
		msg = tea.KeyMsg{Type: tea.KeyCtrlX}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	_, cmd := m.Update(msg)
	return cmd
}

func TestComposeShowsBothPreviews(t *testing.T) {
	a, _, _ := newApp(t)
	m := New(a, Options{})
	size(m)
	typeText(m, "one part\n---\nsecond part #go")
	s := screen(m)
	for _, want := range []string{"Write", "one part 1/2", "second part #go 2/2", "thread of 2", "Asha Demo", "@ashademo", "25/3,000", "✓ Asha Demo"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	key(m, "ctrl+x") // X off
	if !strings.Contains(screen(m), "Not posting to X") {
		t.Error("X still on")
	}
}

func TestPostNowFlow(t *testing.T) {
	a, f, opened := newApp(t)
	m := New(a, Options{})
	size(m)
	typeText(m, "Hello both")
	key(m, "ctrl+s")
	if !strings.Contains(screen(m), "Post it now?") || !strings.Contains(screen(m), "goes out at once, as Asha Demo") {
		t.Fatal(screen(m))
	}
	run(m, key(m, "y"))
	if len(f.Posts()) != 1 || len(*opened) != 1 || !strings.Contains((*opened)[0], "text=Hello+both") {
		t.Fatal(f.Posts(), *opened)
	}
	if !strings.Contains(screen(m), "Post it on X") {
		t.Fatal("no X popup:\n" + screen(m))
	}
	key(m, "enter")
	ps, _ := a.Store.List(post.Posted)
	if len(ps) != 1 {
		t.Fatal("not posted", ps)
	}
	if m.ed.Value() != "" {
		t.Fatal("editor kept the posted text")
	}
}

func TestSchedule(t *testing.T) {
	a, f, _ := newApp(t)
	m := New(a, Options{})
	size(m)
	typeText(m, "Later please")
	key(m, "ctrl+t")
	typeText(m, "in 2h")
	run(m, key(m, "enter"))
	ps, _ := a.Store.List(post.Scheduled)
	if len(ps) != 1 || len(f.Posts()) != 0 {
		t.Fatal(ps, f.Posts())
	}
	if !strings.Contains(screen(m), "Scheduled #") {
		t.Fatal(screen(m))
	}
}

func TestApproveScreen(t *testing.T) {
	a, f, _ := newApp(t)
	p := a.New()
	p.Text, p.Origin, p.Requester, p.Note = "From the agent", post.Agent, "Claude Code", "launch post"
	p.Networks = []string{post.LinkedIn}
	p, _ = a.Submit(context.Background(), p, false)
	m := New(a, Options{Approve: p.ID})
	size(m)
	s := screen(m)
	for _, want := range []string{"Approve post", "Claude Code", "wants to post this as you", "From the agent", "why: launch post", "APPROVE"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	run(m, key(m, "y"))
	if len(f.Posts()) != 1 || f.Posts()[0].Commentary != "From the agent" {
		t.Fatal(f.Posts())
	}
}

func TestQuitKeepsTheDraft(t *testing.T) {
	a, _, _ := newApp(t)
	m := New(a, Options{PopupKey: "B"})
	size(m)
	typeText(m, "unfinished thought")
	key(m, "esc")
	cmd := key(m, "B")
	if cmd == nil {
		t.Fatal("the popup key didn't quit")
	}
	ds, _ := a.Store.List(post.Draft)
	if len(ds) != 1 || ds[0].Text != "unfinished thought" {
		t.Fatal(ds)
	}
}

func TestHelpAndLists(t *testing.T) {
	a, _, _ := newApp(t)
	a.SaveDraft(post.Post{Text: "a draft", Networks: []string{post.X}})
	m := New(a, Options{})
	size(m)
	key(m, "esc")
	key(m, "?")
	if !strings.Contains(screen(m), "WHILE WRITING") || !strings.Contains(screen(m), "QUEUE, DRAFTS, POSTED") {
		t.Fatal(screen(m))
	}
	key(m, "x") // any key closes
	key(m, "3")
	if !strings.Contains(screen(m), "Drafts · 1") || !strings.Contains(screen(m), "a draft") {
		t.Fatal(screen(m))
	}
}
