// Package tui is prasar's screen: write a post once and see it as X and
// LinkedIn will show it, side by side; post it now, schedule it or keep it
// as a draft; follow what's waiting, scheduled and posted. The same
// screen, opened by `prasar approve`, is where you answer an agent's post.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Theyashsawarkar/prasar/internal/app"
	"github.com/Theyashsawarkar/prasar/internal/linkedin"
	"github.com/Theyashsawarkar/prasar/internal/post"
	"github.com/Theyashsawarkar/prasar/internal/ui"
	"github.com/Theyashsawarkar/prasar/internal/when"
)

// Options change how the screen opens.
type Options struct {
	PopupKey string // the tmux key that opened the popup: closes it too
	Approve  int64  // open on this agent's post, to approve it
}

const (
	tabCompose = iota
	tabQueue
	tabDrafts
	tabPosted
)

var tabNames = []string{"Compose", "Queue", "Drafts", "Posted"}

// Model is the screen.
type Model struct {
	a   *app.App
	opt Options
	st  ui.Styles

	w, h   int
	tab    int
	themeT string

	ed      textarea.Model
	editing bool
	cur     post.Post // the post in the editor (ID 0: not saved yet)
	saved   string    // its text when last saved, to know if it changed

	lists [4][]post.Post
	sel   [4]int
	top   [4]int
	stamp string

	li    linkedin.Token
	liErr error
	liAt  time.Time

	prompt  *prompt
	confirm *confirm
	xwait   *xwait
	help    bool
	busy    string

	msg    string
	msgBad bool
	msgAt  time.Time

	approving bool // opened by `prasar approve`
	quitAfter bool // quit once the X step is done (approve popup)
}

type prompt struct {
	title, hint string
	in          textinput.Model
	done        func(m *Model, v string) tea.Cmd
}

type confirm struct {
	title string
	lines []string
	yes   func(m *Model) tea.Cmd
}

// xwait is X's page open in the browser: waiting for you to say you
// pressed Post (and, for a thread, for the link the next part answers).
type xwait struct {
	id          int64
	part, parts int
	in          textinput.Model
}

type tickMsg time.Time
type publishedMsg struct {
	p      post.Post
	err    error
	action string
}
type authMsg struct{ err error }

// New makes the screen.
func New(a *app.App, opt Options) *Model {
	m := &Model{a: a, opt: opt, st: ui.New(ui.LoadTheme("prasar")), themeT: ui.ThemeStamp()}
	m.ed = textarea.New()
	m.ed.Placeholder = "What do you want to say? (a line with --- starts the next post of an X thread)"
	m.ed.ShowLineNumbers = false
	m.ed.Prompt = ""
	m.ed.CharLimit = 0
	m.ed.MaxHeight = 0
	m.styleEditor()
	m.cur = a.New()
	m.loadLinkedIn()
	m.reload()
	if opt.Approve > 0 {
		p, err := a.Store.Get(opt.Approve)
		switch {
		case err != nil:
			m.flash(err.Error(), true)
		case p.Status != post.Approval:
			m.flash(fmt.Sprintf("#%d is %s: nothing to approve", p.ID, p.Status), true)
		default:
			m.approving = true
			m.load(p)
		}
	} else {
		m.editing = true
		m.ed.Focus()
	}
	return m
}

func (m *Model) styleEditor() {
	t := m.st.T
	base := lipgloss.NewStyle()
	for _, s := range []*textarea.Style{&m.ed.FocusedStyle, &m.ed.BlurredStyle} {
		s.Base = base
		s.CursorLine = base
		s.Text = ui.FG(t.Text)
		s.Placeholder = ui.FG(t.Muted)
		s.EndOfBuffer = ui.FG(t.Base)
	}
	m.ed.Cursor.Style = ui.FG(t.Accent)
}

// Init starts the clock.
func (m *Model) Init() tea.Cmd { return tea.Batch(textarea.Blink, tick()) }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) flash(s string, bad bool) { m.msg, m.msgBad, m.msgAt = s, bad, time.Now() }

func (m *Model) loadLinkedIn() {
	m.li, m.liErr = m.a.LinkedIn()
	m.liAt = time.Now()
}

func (m *Model) reload() {
	m.stamp = m.a.Store.Stamp()
	q, _ := m.a.Store.List(post.Approval, post.Ready, post.Failed, post.Scheduled)
	// waiting on you first, then the schedule soonest first
	var waiting, sched []post.Post
	for _, p := range q {
		if p.Status == post.Scheduled {
			sched = append([]post.Post{p}, sched...)
		} else {
			waiting = append(waiting, p)
		}
	}
	m.lists[tabQueue] = append(waiting, sched...)
	m.lists[tabDrafts], _ = m.a.Store.List(post.Draft)
	m.lists[tabPosted], _ = m.a.Store.List(post.Posted)
	for t := tabQueue; t <= tabPosted; t++ {
		m.sel[t] = min(m.sel[t], max(0, len(m.lists[t])-1))
	}
}

func (m *Model) load(p post.Post) {
	m.cur = p
	m.ed.SetValue(p.Text)
	m.saved = p.Text
	m.tab = tabCompose
}

func (m *Model) selected() (post.Post, bool) {
	l := m.lists[m.tab]
	if m.tab == tabCompose || len(l) == 0 {
		return post.Post{}, false
	}
	return l[m.sel[m.tab]], true
}

// ---------------------------------------------------------------- update

// Update handles a key, a resize or a tick.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tickMsg:
		if s := m.a.Store.Stamp(); s != m.stamp {
			m.reload()
		}
		if s := ui.ThemeStamp(); s != m.themeT {
			m.themeT, m.st = s, ui.New(ui.LoadTheme("prasar"))
			m.styleEditor()
		}
		if time.Since(m.liAt) > 30*time.Second {
			m.loadLinkedIn()
		}
		var cmds []tea.Cmd
		if time.Time(msg).Second()%15 == 0 {
			cmds = append(cmds, func() tea.Msg { m.a.DeliverDue(context.Background()); return nil })
		}
		return m, tea.Batch(append(cmds, tick())...)
	case publishedMsg:
		return m, m.published(msg)
	case authMsg:
		m.loadLinkedIn()
		if msg.err != nil {
			m.flash("LinkedIn: "+msg.err.Error(), true)
		} else if m.liErr == nil {
			m.flash("Signed in to LinkedIn as "+m.li.Name, false)
		}
		return m, nil
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	if m.editing {
		var cmd tea.Cmd
		m.ed, cmd = m.ed.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) layout() {
	left := m.leftWidth()
	m.ed.SetWidth(max(10, left-4))
	m.ed.SetHeight(max(3, m.editorHeight()-2))
}

func (m *Model) leftWidth() int {
	if m.w < 100 {
		return m.w
	}
	return m.w * 52 / 100
}

func (m *Model) bodyHeight() int { return max(6, m.h-2) }

func (m *Model) editorHeight() int { return m.bodyHeight() - 5 }

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		m.keep()
		return tea.Quit
	}
	switch {
	case m.help:
		m.help = false
		return nil
	case m.xwait != nil:
		return m.xwaitKey(k)
	case m.prompt != nil:
		return m.promptKey(k)
	case m.confirm != nil:
		c := m.confirm
		m.confirm = nil
		if s == "y" || s == "enter" {
			return c.yes(m)
		}
		m.flash("Nothing done.", false)
		return nil
	case m.busy != "":
		return nil
	}
	if m.editing {
		return m.editKey(k)
	}
	if m.approving {
		switch s {
		case "y", "ctrl+s":
			return m.approve()
		case "n":
			return m.decline()
		case "e", "i", "enter":
			m.editing = true
			return m.ed.Focus()
		case "q", "esc":
			return tea.Quit
		case "?":
			m.help = true
		}
		return nil
	}
	switch s {
	case "q":
		m.keep()
		return tea.Quit
	case m.opt.PopupKey:
		if s != "" {
			m.keep()
			return tea.Quit
		}
	case "?":
		m.help = true
		return nil
	case "tab":
		m.tab = (m.tab + 1) % len(tabNames)
		return nil
	case "shift+tab":
		m.tab = (m.tab + len(tabNames) - 1) % len(tabNames)
		return nil
	case "1", "2", "3", "4":
		m.tab = int(s[0] - '1')
		return nil
	case "L":
		return m.signIn()
	}
	if m.tab == tabCompose {
		switch s {
		case "i", "enter", "a":
			m.editing = true
			return m.ed.Focus()
		case "esc":
			return nil
		}
		return m.composeKey(s)
	}
	return m.listKey(s)
}

// keep saves what's in the editor as a draft when it changed: closing
// the screen never loses a post.
func (m *Model) keep() {
	if m.approving {
		return
	}
	t := m.ed.Value()
	if strings.TrimSpace(t) == "" || t == m.saved || (m.cur.Status != post.Draft && m.cur.ID != 0) {
		return
	}
	p := m.cur
	p.Text = t
	if saved, err := m.a.SaveDraft(p); err == nil {
		m.cur, m.saved = saved, t
	}
}

func (m *Model) editKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.editing = false
		m.ed.Blur()
		return nil
	}
	if m.approving && k.String() == "ctrl+s" {
		m.editing = false
		m.ed.Blur()
		return m.approve()
	}
	if cmd, ok := m.composeCtrl(k.String()); ok {
		return cmd
	}
	var cmd tea.Cmd
	m.ed, cmd = m.ed.Update(k)
	return cmd
}

// composeKey: Compose's keys outside the editor are its Ctrl keys too.
func (m *Model) composeKey(s string) tea.Cmd {
	cmd, _ := m.composeCtrl(s)
	return cmd
}

func (m *Model) composeCtrl(s string) (tea.Cmd, bool) {
	if m.approving {
		return nil, false
	}
	switch s {
	case "ctrl+s":
		return m.postNow(), true
	case "ctrl+t":
		m.ask("When should it go out?", "tomorrow 9am · fri 6pm · in 2h · 3 nov 10:30", "", func(m *Model, v string) tea.Cmd {
			at, err := when.Parse(v, m.a.Now())
			if err != nil {
				m.flash(err.Error(), true)
				return nil
			}
			p := m.editorPost()
			p.SendAt = at.UnixMilli()
			return m.submit(p, "schedule")
		})
		return nil, true
	case "ctrl+d":
		p := m.editorPost()
		saved, err := m.a.SaveDraft(p)
		if err != nil {
			m.flash(err.Error(), true)
			return nil, true
		}
		m.cur, m.saved = saved, saved.Text
		m.flash(fmt.Sprintf("Saved as draft #%d.", saved.ID), false)
		m.reload()
		return nil, true
	case "ctrl+x", "ctrl+l":
		n := post.X
		if s == "ctrl+l" {
			n = post.LinkedIn
		}
		m.cur.Networks = toggle(m.cur.Networks, n)
		return nil, true
	case "ctrl+o":
		m.ask("Add an image", "a path to a PNG, JPEG, GIF or WebP · empty removes the images", "~/", func(m *Model, v string) tea.Cmd {
			if strings.TrimSpace(v) == "" || strings.TrimSpace(v) == "~/" {
				m.cur.Images = nil
				m.flash("Images removed.", false)
				return nil
			}
			imgs, err := app.ResolveImages([]string{v})
			if err != nil {
				m.flash(err.Error(), true)
				return nil
			}
			m.cur.Images = append(m.cur.Images, imgs...)
			m.flash("Added "+filepath.Base(imgs[0])+".", false)
			return nil
		})
		return nil, true
	case "ctrl+n":
		m.keep()
		kept := m.cur.ID != 0 && strings.TrimSpace(m.ed.Value()) != ""
		m.cur, m.saved = m.a.New(), ""
		m.ed.SetValue("")
		if kept {
			m.flash("New post. The last one is in Drafts.", false)
		}
		return nil, true
	}
	return nil, false
}

func toggle(ns []string, n string) []string {
	var out []string
	found := false
	for _, x := range ns {
		if x == n {
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		out = append(out, n)
	}
	return post.Normalize(out)
}

// editorPost is the editor's post with its current text.
func (m *Model) editorPost() post.Post {
	p := m.cur
	p.Text = m.ed.Value()
	return p
}

func (m *Model) postNow() tea.Cmd {
	p := m.editorPost()
	p.SendAt = 0
	if msg := post.Check(p); msg != "" {
		m.flash(msg, true)
		return nil
	}
	m.confirm = &confirm{title: "Post it now?", lines: m.whatHappens(p), yes: func(m *Model) tea.Cmd { return m.submit(p, "post") }}
	return nil
}

// whatHappens says what posting will do, network by network.
func (m *Model) whatHappens(p post.Post) []string {
	var out []string
	if p.On(post.LinkedIn) {
		if m.liErr != nil {
			out = append(out, m.st.Red.Render("LinkedIn: ")+m.st.Sub.Render(m.liErr.Error()))
		} else {
			out = append(out, m.st.Bold.Render("LinkedIn")+m.st.Sub.Render(" goes out at once, as "+m.li.Name+"."))
		}
	}
	if p.On(post.X) {
		parts := len(post.XParts(p.Text))
		line := m.st.Bold.Render("X") + m.st.Sub.Render(" opens in your browser with it filled in: you press Post.")
		if parts > 1 {
			line += m.st.Sub.Render(fmt.Sprintf(" A thread of %d, one part at a time.", parts))
		}
		out = append(out, line)
		if len(p.Images) > 0 {
			out = append(out, m.st.Peach.Render("The image goes to your clipboard: paste it on X with Ctrl+V."))
		}
	}
	return out
}

func (m *Model) submit(p post.Post, action string) tea.Cmd {
	if p.Status == "" || p.Status == post.Draft || p.Status == post.Failed {
		p.Status = post.Draft
	}
	p.Origin = post.You
	m.busy = "Posting…"
	if action == "schedule" {
		m.busy = "Scheduling…"
	}
	a := m.a
	return func() tea.Msg {
		got, err := a.Submit(context.Background(), p, true)
		return publishedMsg{p: got, err: err, action: action}
	}
}

func (m *Model) published(r publishedMsg) tea.Cmd {
	m.busy = ""
	m.reload()
	p := r.p
	if r.err != nil && p.ID == 0 {
		m.flash(r.err.Error(), true)
		return nil
	}
	if r.action == "post" || r.action == "schedule" || r.action == "approve" {
		if m.tab == tabCompose && !m.approving {
			m.cur, m.saved = m.a.New(), ""
			m.ed.SetValue("")
		}
	}
	switch {
	case p.Status == post.Scheduled:
		m.flash(fmt.Sprintf("Scheduled #%d for %s.", p.ID, when.Say(time.UnixMilli(p.SendAt), m.a.Now())), false)
	case r.err != nil:
		m.flash(r.err.Error()+" (r in Queue retries)", true)
	default:
		m.flash(m.summary(p), false)
	}
	if p.State(post.X) == post.Opened {
		m.waitX(p)
		return nil
	}
	if m.approving {
		return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return tea.QuitMsg{} })
	}
	return nil
}

func (m *Model) summary(p post.Post) string {
	var bits []string
	for _, n := range p.Networks {
		switch p.State(n) {
		case post.Done:
			bits = append(bits, post.Name(n)+" ✓")
		case post.Opened:
			bits = append(bits, post.Name(n)+": press Post in your browser")
		case post.Error:
			bits = append(bits, post.Name(n)+" ✗")
		}
	}
	return fmt.Sprintf("#%d: %s", p.ID, strings.Join(bits, " · "))
}

func (m *Model) waitX(p post.Post) {
	in := textinput.New()
	in.Placeholder = "https://x.com/you/status/… (optional)"
	in.Prompt = ""
	in.CharLimit = 300
	in.Width = 50
	in.Focus()
	m.xwait = &xwait{id: p.ID, part: p.Results[post.X].Part, parts: len(post.XParts(p.Text)), in: in}
}

func (m *Model) xwaitKey(k tea.KeyMsg) tea.Cmd {
	w := m.xwait
	switch k.String() {
	case "esc":
		m.xwait = nil
		m.flash(fmt.Sprintf("#%d stays in Queue: o opens X again.", w.id), false)
		return m.quitIfApproving()
	case "ctrl+k":
		m.xwait = nil
		if _, err := m.a.SkipX(w.id); err != nil {
			m.flash(err.Error(), true)
		} else {
			m.flash("Skipped X for this post.", false)
		}
		m.reload()
		return m.quitIfApproving()
	case "ctrl+o":
		if _, err := m.a.OpenX(w.id); err != nil {
			m.flash(err.Error(), true)
		}
		return nil
	case "enter":
		link := strings.TrimSpace(w.in.Value())
		if link != "" && post.XStatusID(link) == "" {
			m.flash("That isn't a link to a post on X (or leave it empty).", true)
			return nil
		}
		p, more, err := m.a.PostedX(w.id, link)
		m.xwait = nil
		m.reload()
		if err != nil {
			m.flash(err.Error(), true)
			return nil
		}
		if more {
			p, err = m.a.OpenX(p.ID)
			if err != nil {
				m.flash(err.Error(), true)
				return nil
			}
			m.waitX(p)
			return nil
		}
		m.flash(m.summary(p), false)
		return m.quitIfApproving()
	}
	var cmd tea.Cmd
	w.in, cmd = w.in.Update(k)
	return cmd
}

func (m *Model) quitIfApproving() tea.Cmd {
	if m.approving {
		return tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return tea.QuitMsg{} })
	}
	return nil
}

func (m *Model) ask(title, hint, value string, done func(*Model, string) tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 400
	in.Width = 50
	in.SetValue(value)
	in.CursorEnd()
	in.Focus()
	m.prompt = &prompt{title: title, hint: hint, in: in, done: done}
}

func (m *Model) promptKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.prompt = nil
		return nil
	case "enter":
		p := m.prompt
		m.prompt = nil
		return p.done(m, p.in.Value())
	}
	var cmd tea.Cmd
	m.prompt.in, cmd = m.prompt.in.Update(k)
	return cmd
}

func (m *Model) listKey(s string) tea.Cmd {
	l := m.lists[m.tab]
	switch s {
	case "j", "down":
		m.sel[m.tab] = min(m.sel[m.tab]+1, max(0, len(l)-1))
		return nil
	case "k", "up":
		m.sel[m.tab] = max(m.sel[m.tab]-1, 0)
		return nil
	case "g", "home":
		m.sel[m.tab] = 0
		return nil
	case "G", "end":
		m.sel[m.tab] = max(0, len(l)-1)
		return nil
	}
	p, ok := m.selected()
	if !ok {
		return nil
	}
	switch s {
	case "enter", "e":
		if m.tab == tabPosted {
			return m.openLink(p)
		}
		if p.Status == post.Ready {
			m.flash("Part of it is already out: o opens X, c cancels the rest.", true)
			return nil
		}
		m.load(p)
		m.editing = true
		return m.ed.Focus()
	case "p":
		if p.Status == post.Ready || p.Status == post.Approval {
			return nil
		}
		p.SendAt = 0
		m.confirm = &confirm{title: fmt.Sprintf("Post #%d now?", p.ID), lines: m.whatHappens(p), yes: func(m *Model) tea.Cmd {
			return m.submit(p, "post")
		}}
	case "t":
		m.ask(fmt.Sprintf("When should #%d go out?", p.ID), "tomorrow 9am · fri 6pm · in 2h · 3 nov 10:30", "", func(m *Model, v string) tea.Cmd {
			at, err := when.Parse(v, m.a.Now())
			if err == nil {
				_, err = m.a.Reschedule(p.ID, at)
			}
			if err != nil {
				m.flash(err.Error(), true)
			} else {
				m.flash(fmt.Sprintf("#%d goes out %s.", p.ID, when.Say(at, m.a.Now())), false)
			}
			m.reload()
			return nil
		})
	case "o":
		if !p.On(post.X) {
			m.flash("This post isn't for X.", true)
			return nil
		}
		got, err := m.a.OpenX(p.ID)
		if err != nil {
			m.flash(err.Error(), true)
			return nil
		}
		m.reload()
		m.waitX(got)
	case "a":
		if p.Status != post.Approval {
			return nil
		}
		m.confirm = &confirm{title: fmt.Sprintf("Approve #%d?", p.ID), lines: append([]string{m.st.Sub.Render(app.OneLine(p.Text, 70))}, m.whatHappens(p)...),
			yes: func(m *Model) tea.Cmd { return m.approveID(p.ID, nil) }}
	case "n":
		if err := m.a.Decline(p.ID); err != nil {
			m.flash(err.Error(), true)
		} else {
			m.flash(fmt.Sprintf("Declined #%d.", p.ID), false)
		}
		m.reload()
	case "r":
		m.busy = "Posting to LinkedIn…"
		a := m.a
		return func() tea.Msg {
			got, err := a.Retry(context.Background(), p.ID)
			return publishedMsg{p: got, err: err, action: "retry"}
		}
	case "c":
		m.confirm = &confirm{title: fmt.Sprintf("Cancel #%d?", p.ID), lines: []string{m.st.Sub.Render("What's already posted stays posted.")},
			yes: func(m *Model) tea.Cmd {
				if err := m.a.Cancel(p.ID); err != nil {
					m.flash(err.Error(), true)
				} else {
					m.flash(fmt.Sprintf("Cancelled #%d.", p.ID), false)
				}
				m.reload()
				return nil
			}}
	case "d":
		if p.Status != post.Draft {
			return nil
		}
		m.confirm = &confirm{title: fmt.Sprintf("Delete draft #%d?", p.ID), lines: []string{m.st.Sub.Render(app.OneLine(p.Text, 70))},
			yes: func(m *Model) tea.Cmd {
				_ = m.a.Store.Delete(p.ID)
				m.flash(fmt.Sprintf("Deleted #%d.", p.ID), false)
				m.reload()
				return nil
			}}
	case "y":
		link := link(p)
		if link == "" {
			m.flash("No link yet.", true)
			return nil
		}
		if err := copyText(link); err != nil {
			m.flash(err.Error(), true)
		} else {
			m.flash("Copied "+link, false)
		}
	case "v":
		return m.openLink(p)
	}
	return nil
}

func link(p post.Post) string {
	for _, n := range []string{post.LinkedIn, post.X} {
		if u := p.Results[n].URL; u != "" {
			return u
		}
	}
	return ""
}

func (m *Model) openLink(p post.Post) tea.Cmd {
	u := link(p)
	if u == "" {
		m.flash("No link for this post (X links come from what you paste after posting).", true)
		return nil
	}
	if err := m.a.Opener(u); err != nil {
		m.flash(err.Error(), true)
	}
	return nil
}

func (m *Model) approve() tea.Cmd {
	t := m.ed.Value()
	var edited *string
	if t != m.cur.Text {
		edited = &t
	}
	return m.approveID(m.cur.ID, edited)
}

func (m *Model) approveID(id int64, text *string) tea.Cmd {
	m.busy = "Approving…"
	a := m.a
	return func() tea.Msg {
		got, err := a.Approve(context.Background(), id, text, true)
		return publishedMsg{p: got, err: err, action: "approve"}
	}
}

func (m *Model) decline() tea.Cmd {
	if err := m.a.Decline(m.cur.ID); err != nil {
		m.flash(err.Error(), true)
		return nil
	}
	m.flash("Declined. Nothing was posted.", false)
	return tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return tea.QuitMsg{} })
}

func (m *Model) signIn() tea.Cmd {
	return tea.ExecProcess(exec.Command(app.Executable(), "auth", "linkedin"), func(err error) tea.Msg { return authMsg{err} })
}

// copyText puts text on the clipboard.
func copyText(s string) error {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = exec.Command("wl-copy")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(s)
	if err := cmd.Run(); err != nil {
		return errors.New("couldn't copy: install wl-clipboard (Wayland) or xclip (X11)")
	}
	return nil
}
