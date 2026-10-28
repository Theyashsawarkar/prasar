package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Theyashsawarkar/prasar/internal/post"
	"github.com/Theyashsawarkar/prasar/internal/ui"
	"github.com/Theyashsawarkar/prasar/internal/when"
)

// View draws the screen.
func (m *Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	var body []string
	switch {
	case m.approving || m.tab == tabCompose:
		body = m.composeView()
	default:
		body = m.listView()
	}
	for len(body) < m.bodyHeight() {
		body = append(body, "")
	}
	out := m.header() + "\n" + strings.Join(body[:m.bodyHeight()], "\n") + "\n" + m.footer()
	switch {
	case m.help:
		out = m.overlay(out, m.helpBox())
	case m.xwait != nil:
		out = m.overlay(out, m.xwaitBox())
	case m.prompt != nil:
		lines := []string{m.st.Sub.Render(m.prompt.hint), "", m.prompt.in.View(), "", m.st.Key("Enter", "ok") + "  " + m.st.Key("Esc", "cancel")}
		out = m.overlay(out, m.st.Popup(m.prompt.title, lines, min(m.w-4, 66), ""))
	case m.confirm != nil:
		lines := append(append([]string{}, m.confirm.lines...), "", m.st.Key("y", "yes")+"  "+m.st.Key("n", "no"))
		out = m.overlay(out, m.st.Popup(m.confirm.title, lines, min(m.w-4, 76), ""))
	}
	return out
}

func (m *Model) overlay(base string, box []string) string {
	return ui.Overlay(base, box, m.w, max(1, (m.h-len(box))/2))
}

func (m *Model) header() string {
	if m.approving {
		who := m.cur.Requester
		if who == "" {
			who = "An agent"
		}
		left := " " + m.st.Pill("Approve post", m.st.T.Peach) + "  " + m.st.Bold.Render(who) + m.st.Sub.Render(" wants to post this as you")
		return ui.Row(left, m.accounts()+" ", m.w)
	}
	tabs := make([]string, len(tabNames))
	for i, t := range tabNames {
		if n := len(m.lists[i]); i > tabCompose && i < tabPosted && n > 0 {
			t = fmt.Sprintf("%s %d", t, n)
		}
		tabs[i] = t
	}
	return m.st.Tabs(tabs, m.tab, "", m.w-lipgloss.Width(m.accounts())-1) + m.accounts() + " "
}

// accounts is the top right: who LinkedIn posts as, and X's one click.
func (m *Model) accounts() string {
	li := m.st.Red.Render("✗ LinkedIn") + m.st.Muted.Render(" (L signs in)")
	if m.liErr == nil {
		days := int(m.li.Left(m.a.Now()).Hours() / 24)
		li = m.st.Green.Render("✓ ") + m.st.Text.Render(m.li.Name)
		if days < 7 {
			li += m.st.Yellow.Render(fmt.Sprintf(" (%dd left: L)", days))
		}
	}
	return li + m.st.Muted.Render("  ·  ") + m.st.Text.Render("X") + m.st.Muted.Render(" one click")
}

func (m *Model) footer() string {
	if m.busy != "" {
		return m.st.Footer("…", nil, m.st.Accent.Render(m.busy), m.w)
	}
	msg := ""
	if m.msg != "" && time.Since(m.msgAt) < 6*time.Second {
		msg = m.st.Text.Render(m.msg)
		if m.msgBad {
			msg = m.st.Red.Render(m.msg)
		}
	}
	var mode string
	var keys [][2]string
	switch {
	case m.approving && m.editing:
		mode, keys = "EDIT", [][2]string{{"^S", "approve"}, {"Esc", "done editing"}}
	case m.approving:
		mode, keys = "APPROVE", [][2]string{{"y", "approve & post"}, {"e", "edit"}, {"n", "decline"}, {"q", "later"}}
	case m.editing:
		mode, keys = "WRITE", [][2]string{{"^S", "post"}, {"^T", "schedule"}, {"^D", "draft"}, {"^X", "X"}, {"^L", "LinkedIn"},
			{"^O", "image"}, {"^N", "new"}, {"Esc", "more keys"}}
	case m.tab == tabCompose:
		mode, keys = "COMPOSE", [][2]string{{"i", "write"}, {"^S", "post"}, {"^T", "schedule"}, {"Tab", "queue"}, {"?", "keys"}, {"q", "quit"}}
	case m.tab == tabQueue:
		mode = "QUEUE"
		p, _ := m.selected()
		switch p.Status {
		case post.Approval:
			keys = [][2]string{{"a", "approve"}, {"n", "decline"}, {"e", "edit"}}
		case post.Scheduled:
			keys = [][2]string{{"p", "post now"}, {"t", "move"}, {"e", "edit"}, {"c", "cancel"}}
		case post.Ready, post.Failed:
			if p.State(post.LinkedIn) == post.Error {
				keys = append(keys, [2]string{"r", "retry LinkedIn"})
			}
			if p.On(post.X) && p.State(post.X) != post.Done && p.State(post.X) != post.Abandoned {
				keys = append(keys, [2]string{"o", "open on X"})
			}
			keys = append(keys, [2]string{"c", "cancel"})
		}
		keys = append(keys, [2]string{"Tab", "next tab"}, [2]string{"?", "keys"})
	case m.tab == tabDrafts:
		mode, keys = "DRAFTS", [][2]string{{"e", "edit"}, {"p", "post now"}, {"t", "schedule"}, {"d", "delete"}, {"?", "keys"}}
	default:
		mode, keys = "POSTED", [][2]string{{"v", "open"}, {"y", "copy link"}, {"o", "X again"}, {"?", "keys"}}
	}
	return m.st.Footer(mode, keys, msg, m.w)
}

// ---------------------------------------------------------------- compose

func (m *Model) composeView() []string {
	left := m.leftWidth()
	bh := m.bodyHeight()
	edH := m.editorHeight()
	text := m.ed.Value()
	p := m.editorPost()

	title := "Write"
	switch {
	case m.approving:
		title = fmt.Sprintf("Post #%d", m.cur.ID)
	case m.cur.ID != 0:
		title = fmt.Sprintf("Write · #%d %s", m.cur.ID, m.cur.Status)
	}
	if m.editing {
		title += "  ✎"
	}
	edLines := strings.Split(m.ed.View(), "\n")
	editor := strings.Split(m.st.Box(left, edH, title, edLines, m.editing, ""), "\n")
	settings := strings.Split(m.st.Box(left, 5, "Post", m.settingsLines(p), false, ""), "\n")
	col := append(editor, settings...)
	if m.w < 100 { // narrow: no previews, the counts in the settings box
		return col[:min(len(col), bh)]
	}
	rw := m.w - left - 1
	xh := m.xHeight(p, text, rw-4, bh)
	xbox := strings.Split(m.st.Box(rw, xh, "X", m.xPreview(p, text, rw-4, xh-2), false, ""), "\n")
	lbox := strings.Split(m.st.Box(rw, bh-xh, "LinkedIn", m.linkedInPreview(p, text, rw-4, bh-xh-2), false, ""), "\n")
	right := append(xbox, lbox...)
	out := make([]string, bh)
	for i := range out {
		l, r := "", ""
		if i < len(col) {
			l = col[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = ui.Fit(l, left) + " " + r
	}
	return out
}

// xHeight splits the previews' column: X gets what its post (a thread
// can be long) needs, LinkedIn the rest, each at least a third.
func (m *Model) xHeight(p post.Post, text string, w, bh int) int {
	lines := m.xPreview(p, text, w, 1000) // unclipped: content, padding, then the meter
	need := len(lines)
	for need > 2 && lines[need-2] == "" {
		lines = append(lines[:need-2], lines[need-1])
		need--
	}
	return max(bh/3, min(need+2, bh-bh/3))
}

func (m *Model) settingsLines(p post.Post) []string {
	chip := func(n string) string {
		if p.On(n) {
			return m.st.Pill("● "+post.Name(n), m.st.T.Accent)
		}
		return m.st.Chip("○ "+post.Name(n), m.st.T.Muted)
	}
	to := m.st.Muted.Render("To      ") + chip(post.X) + " " + chip(post.LinkedIn)
	if m.w < 100 && strings.TrimSpace(p.Text) != "" {
		to += m.st.Muted.Render(fmt.Sprintf("   X %d/280 · LinkedIn %d/3000", post.XLength(p.Text), post.LinkedInLength(p.Text)))
	}
	whenS := m.st.Text.Render("now")
	if p.SendAt > 0 {
		whenS = m.st.Blue.Render(when.Say(time.UnixMilli(p.SendAt), m.a.Now()))
	}
	if m.approving && m.cur.Note != "" {
		whenS += m.st.Muted.Render("   why: ") + m.st.Sub.Render(m.cur.Note)
	}
	imgs := m.st.Muted.Render("none · Ctrl+O adds one")
	if len(p.Images) > 0 {
		var names []string
		for _, i := range p.Images {
			names = append(names, filepath.Base(i))
		}
		imgs = m.st.Text.Render(strings.Join(names, ", "))
	}
	return []string{to, m.st.Muted.Render("When    ") + whenS, m.st.Muted.Render("Images  ") + imgs}
}

func (m *Model) avatar(name string, color string) string {
	r := []rune(strings.TrimSpace(name))
	initial := "?"
	if len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	return m.st.Pill(initial, color)
}

func (m *Model) names() (xName, handle, liName string) {
	liName = "You"
	if m.liErr == nil && m.li.Name != "" {
		liName = m.li.Name
	}
	xName = m.a.Config.X.Name
	if xName == "" {
		xName = liName
	}
	handle = strings.TrimPrefix(m.a.Config.X.Handle, "@")
	if handle == "" {
		handle = strings.ToLower(strings.ReplaceAll(xName, " ", ""))
	}
	return
}

// xPreview is the post as X shows it: each part of a thread under the
// last, and how full the longest is.
func (m *Model) xPreview(p post.Post, text string, w, h int) []string {
	if !p.On(post.X) {
		return []string{"", m.st.Muted.Render("Not posting to X.  Ctrl+X adds it.")}
	}
	name, handle, _ := m.names()
	head := m.avatar(name, m.st.T.Sky) + " " + m.st.Bold.Render(name) + m.st.Muted.Render(" @"+handle+" · now")
	parts := post.XParts(text)
	if len(parts) == 0 {
		return []string{head, "", m.st.Muted.Render("Your post shows here as X will show it.")}
	}
	lines := []string{head}
	longest := 0
	for i, part := range parts {
		n := post.XLength(part)
		longest = max(longest, n)
		if i > 0 {
			lines = append(lines, m.st.Muted.Render("  │"))
		}
		for _, l := range ui.Wrap(part, w-2) {
			lines = append(lines, "  "+m.st.Text.Render(l))
		}
	}
	if len(p.Images) > 0 {
		lines = append(lines, "", m.st.Peach.Render("▣ image: on your clipboard when X opens, paste it"))
	}
	label := fmt.Sprintf("%d/280", longest)
	if len(parts) > 1 {
		label = fmt.Sprintf("thread of %d · longest %d/280", len(parts), longest)
	}
	return pinBottom(lines, h, m.meter(longest, post.XLimit, w, label))
}

// linkedInPreview is the post as LinkedIn's feed shows it, folded where
// "…see more" cuts it.
func (m *Model) linkedInPreview(p post.Post, text string, w, h int) []string {
	if !p.On(post.LinkedIn) {
		return []string{"", m.st.Muted.Render("Not posting to LinkedIn.  Ctrl+L adds it.")}
	}
	_, _, name := m.names()
	head := m.avatar(name, m.st.T.Blue) + " " + m.st.Bold.Render(name) + m.st.Muted.Render(" · Just now · 🌐")
	t := post.LinkedInBody(text)
	if t == "" {
		return []string{head, "", m.st.Muted.Render("Your post shows here as LinkedIn will show it.")}
	}
	lines := []string{head, ""}
	r := []rune(t)
	shown := t
	folded := len(r) > post.LinkedInFold
	if folded {
		shown = string(r[:post.LinkedInFold])
		if i := strings.LastIndexAny(shown, " \n"); i > post.LinkedInFold*2/3 {
			shown = shown[:i] // the feed doesn't cut a word in half
		}
	}
	const more = "…see more"
	if folded {
		shown = strings.TrimRight(shown, " ") + " " + more // wrapped with the text, so it's never cut off
	}
	wrapped := ui.Wrap(shown, w)
	for i, l := range wrapped {
		if folded && i == len(wrapped)-1 && strings.HasSuffix(l, more) {
			lines = append(lines, m.st.Text.Render(strings.TrimSuffix(l, more))+m.st.Muted.Render(more))
			continue
		}
		lines = append(lines, m.st.Text.Render(l))
	}
	if n := len(p.Images); n > 0 {
		lines = append(lines, "", m.st.Blue.Render(fmt.Sprintf("▣ %d image%s", n, map[bool]string{true: "s", false: ""}[n != 1])))
	}
	n := post.LinkedInLength(t)
	label := fmt.Sprintf("%s/3,000", comma(n))
	if folded {
		label += " · folds after ~210"
	}
	return pinBottom(lines, h, m.meter(n, post.LinkedInLimit, w, label))
}

// pinBottom fits lines in h rows with last on the bottom row.
func pinBottom(lines []string, h int, last string) []string {
	if h < 2 {
		return []string{last}
	}
	if len(lines) > h-1 {
		lines = append(lines[:h-2], "…")
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	return append(lines, last)
}

// meter is a bar of how full a post is: green, yellow near the limit, red
// past it.
func (m *Model) meter(n, limit, w int, label string) string {
	bw := max(8, min(24, w-lipgloss.Width(label)-2))
	fill := min(bw, n*bw/limit)
	c := m.st.Green
	switch {
	case n > limit:
		c = m.st.Red
	case n*10 >= limit*9:
		c = m.st.Yellow
	}
	return c.Render(strings.Repeat("━", fill)) + m.st.Muted.Render(strings.Repeat("─", bw-fill)) + " " + c.Render(label)
}

func comma(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

// ---------------------------------------------------------------- lists

func (m *Model) listView() []string {
	bh := m.bodyHeight()
	l := m.lists[m.tab]
	if len(l) == 0 {
		empty := map[int]string{
			tabQueue:  "Nothing waiting or scheduled. Write a post and press Ctrl+T to schedule it.",
			tabDrafts: "No drafts. Ctrl+D in Compose keeps one.",
			tabPosted: "Nothing posted yet.",
		}[m.tab]
		box := strings.Split(m.st.Box(m.w, bh, tabNames[m.tab], []string{"", "  " + m.st.Muted.Render(empty)}, true, ""), "\n")
		return box
	}
	detailH := min(14, bh/2)
	tableH := bh - detailH
	cols := []ui.Column{{Title: "#", Width: 4}, {Title: "When", Width: 15}, {Title: "To", Width: 15}, {Title: "State", Width: 26}, {Title: "Post", Width: 0}}
	var rows [][]string
	for _, p := range l {
		rows = append(rows, []string{m.st.Muted.Render(fmt.Sprint(p.ID)), m.whenOf(p), m.st.Sub.Render(networks(p)), m.stateOf(p),
			m.st.Text.Render(strings.Join(strings.Fields(p.Text), " "))})
	}
	sel := m.sel[m.tab]
	inner := tableH - 2
	m.top[m.tab] = ui.Scroll(sel, m.top[m.tab], inner-1)
	table := m.st.Table(cols, rows, sel, m.top[m.tab], m.w-4, inner)
	title := fmt.Sprintf("%s · %d", tabNames[m.tab], len(l))
	out := strings.Split(m.st.Box(m.w, tableH, title, table, true, ""), "\n")
	return append(out, strings.Split(m.st.Box(m.w, detailH, m.detailTitle(l[sel]), m.detail(l[sel], m.w-4, detailH-2), false, ""), "\n")...)
}

func networks(p post.Post) string {
	var out []string
	for _, n := range p.Networks {
		out = append(out, post.Name(n))
	}
	return strings.Join(out, " + ")
}

func (m *Model) whenOf(p post.Post) string {
	now := m.a.Now()
	switch {
	case p.Status == post.Posted:
		return m.st.Sub.Render(ago(time.UnixMilli(p.Updated), now))
	case p.SendAt > 0 && p.Status != post.Ready:
		return m.st.Blue.Render(when.Say(time.UnixMilli(p.SendAt), now))
	}
	return m.st.Sub.Render(ago(time.UnixMilli(p.Updated), now))
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("02 Jan")
}

func (m *Model) stateOf(p post.Post) string {
	switch p.Status {
	case post.Approval:
		return m.st.Peach.Render("● needs your approval")
	case post.Scheduled:
		return m.st.Blue.Render("◷ scheduled")
	case post.Draft:
		return m.st.Muted.Render("✎ draft")
	case post.Posted:
		return m.st.Green.Render("✓ posted")
	case post.Failed:
		return m.st.Red.Render("✗ " + m.failedOn(p) + " failed")
	case post.Ready:
		if p.State(post.LinkedIn) == post.Error {
			return m.st.Red.Render("✗ LinkedIn failed")
		}
		return m.st.Yellow.Render("◐ X waits for your click")
	}
	return m.st.Muted.Render(p.Status)
}

func (m *Model) failedOn(p post.Post) string {
	var out []string
	for _, n := range p.Networks {
		if p.State(n) == post.Error {
			out = append(out, post.Name(n))
		}
	}
	return strings.Join(out, " + ")
}

func (m *Model) detailTitle(p post.Post) string {
	t := fmt.Sprintf("#%d · %s", p.ID, p.Status)
	if p.Origin == post.Agent {
		who := p.Requester
		if who == "" {
			who = "an agent"
		}
		t += " · by " + who
	}
	return t
}

func (m *Model) detail(p post.Post, w, h int) []string {
	var lines []string
	if p.Note != "" {
		lines = append(lines, m.st.Peach.Render("Why: ")+m.st.Sub.Render(p.Note), "")
	}
	text := ui.Wrap(strings.TrimSpace(p.Text), w)
	room := h - len(lines) - len(p.Networks) - 1
	if len(text) > room && room > 0 {
		text = append(text[:max(0, room-1)], "…")
	}
	for _, l := range text {
		lines = append(lines, m.st.Text.Render(l))
	}
	lines = append(lines, "")
	for _, n := range p.Networks {
		r := p.Results[n]
		var st string
		switch p.State(n) {
		case post.Done:
			st = m.st.Green.Render("✓ posted ") + m.st.Sub.Render(r.URL)
		case post.Opened:
			st = m.st.Yellow.Render("◐ open in your browser: press Post there, then o to confirm")
		case post.Error:
			st = m.st.Red.Render("✗ " + r.Error)
		case post.Abandoned:
			st = m.st.Muted.Render("skipped")
		default:
			switch {
			case n == post.X && p.Status == post.Ready && r.Part > 0:
				st = m.st.Yellow.Render(fmt.Sprintf("◐ part %d of %d next: o opens it", r.Part+1, len(post.XParts(p.Text))))
			case n == post.X && p.Status == post.Ready:
				st = m.st.Yellow.Render("◐ ready: o opens it on X")
			default:
				st = m.st.Muted.Render("not yet")
			}
		}
		lines = append(lines, m.st.Muted.Render(ui.Fit(post.Name(n), 10))+st)
	}
	return lines
}

// ---------------------------------------------------------------- popups

func (m *Model) xwaitBox() []string {
	x := m.xwait
	title := "Post it on X"
	if x.parts > 1 {
		title = fmt.Sprintf("Post part %d of %d on X", x.part+1, x.parts)
	}
	lines := []string{
		m.st.Text.Render("X is open in your browser with the post filled in."),
		m.st.Text.Render("Press ") + m.st.Bold.Render("Post") + m.st.Text.Render(" there, then come back and press Enter."),
	}
	if x.parts > 1 && x.part+1 < x.parts {
		lines = append(lines, "", m.st.Sub.Render("Paste the post's link (Share → Copy link) so the next part replies to it:"))
	} else {
		lines = append(lines, "", m.st.Sub.Render("Its link, if you want it kept (optional):"))
	}
	lines = append(lines, x.in.View(), "",
		m.st.Key("Enter", "posted")+"  "+m.st.Key("^O", "open X again")+"  "+m.st.Key("^K", "skip X")+"  "+m.st.Key("Esc", "later"))
	return m.st.Popup(title, lines, min(m.w-4, 78), m.st.T.Sky)
}

func (m *Model) helpLines() []string {
	var out []string
	where := ""
	heads := map[string]string{"write": "While writing", "compose": "Compose", "": "Everywhere", "lists": "Queue, Drafts, Posted", "x": "While X waits for your click"}
	for _, k := range Keys {
		if k.Where != where || len(out) == 0 {
			where = k.Where
			if len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, m.st.Heading(heads[where]))
		}
		out = append(out, m.st.Key(k.Keys, "")+" "+m.st.Sub.Render(k.Desc))
	}
	return out
}

// helpBox is the key sheet, in two columns when one doesn't fit.
func (m *Model) helpBox() []string {
	lines := m.helpLines()
	foot := m.st.Muted.Render("Any key closes this.")
	if len(lines)+4 <= m.h || m.w < 120 {
		return m.st.Popup("Keys", append(lines, "", foot), min(m.w-4, 92), "")
	}
	// split at a section start near the middle
	cut := len(lines) / 2
	for i := cut; i < len(lines); i++ {
		if lines[i] == "" {
			cut = i
			break
		}
	}
	w := min(m.w-4, 170)
	cw := (w - 7) / 2
	left, right := lines[:cut], lines[min(cut+1, len(lines)):]
	var out []string
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, ui.Fit(l, cw)+"   "+r)
	}
	return m.st.Popup("Keys", append(out, "", foot), w, "")
}
