// Package app is prasar's core, shared by the screen, the CLI, the MCP
// server and the background service: config, the posts' lifecycle
// (drafts, approval, the schedule) and publishing.
//
// LinkedIn posts through its free API. X has no free API any more, so X
// posts go through X's own share page: prasar opens it with the post
// filled in and you press Post (images are put on the clipboard to
// paste). Nothing here can post to X without you.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Theyashsawarkar/prasar/internal/linkedin"
	"github.com/Theyashsawarkar/prasar/internal/post"
	"github.com/Theyashsawarkar/prasar/internal/store"
)

// Version is prasar's version.
const Version = "0.1.0"

// Paths are where prasar keeps things. PRASAR_HOME puts them all under
// one folder (the demo, tests).
type Paths struct{ Config, Data, State string }

// DefaultPaths follows the XDG base directories.
func DefaultPaths() Paths {
	if h := os.Getenv("PRASAR_HOME"); h != "" {
		return Paths{filepath.Join(h, "config"), filepath.Join(h, "data"), filepath.Join(h, "state")}
	}
	home, _ := os.UserHomeDir()
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return filepath.Join(home, def)
	}
	return Paths{filepath.Join(env("XDG_CONFIG_HOME", ".config"), "prasar"),
		filepath.Join(env("XDG_DATA_HOME", ".local/share"), "prasar"),
		filepath.Join(env("XDG_STATE_HOME", ".local/state"), "prasar")}
}

// ConfigFile is config.toml.
func (p Paths) ConfigFile() string { return filepath.Join(p.Config, "config.toml") }

// Config is config.toml.
type Config struct {
	// Networks are ticked in a new post.
	Networks []string `toml:"networks"`
	LinkedIn struct {
		ClientID     string `toml:"client_id"`
		ClientSecret string `toml:"client_secret"`
		Port         int    `toml:"port"`
		Version      string `toml:"version"`
	} `toml:"linkedin"`
	X struct {
		Name   string `toml:"name"`   // shown in the preview
		Handle string `toml:"handle"` // without @, shown in the preview
	} `toml:"x"`
}

// LinkedInApp is the LinkedIn developer app from the config.
func (c Config) LinkedInApp() linkedin.App {
	return linkedin.App{ClientID: c.LinkedIn.ClientID, ClientSecret: c.LinkedIn.ClientSecret,
		Port: c.LinkedIn.Port, Version: c.LinkedIn.Version}
}

// LoadConfig reads config.toml (defaults when there's none).
func LoadConfig(p Paths) (Config, error) {
	c := Config{Networks: []string{post.X, post.LinkedIn}}
	if _, err := toml.DecodeFile(p.ConfigFile(), &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("%s: %w", p.ConfigFile(), err)
	}
	c.Networks = post.Normalize(c.Networks)
	return c, nil
}

// SaveConfig writes config.toml, private (it holds the app's secret).
func SaveConfig(p Paths, c Config) error {
	if err := os.MkdirAll(p.Config, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p.ConfigFile(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, "# prasar's settings: https://github.com/Theyashsawarkar/prasar#configuration")
	return toml.NewEncoder(f).Encode(c)
}

// App is an open prasar.
type App struct {
	Paths  Paths
	Config Config
	Store  *store.Store
	Tokens linkedin.Tokens
	Now    func() time.Time
	// Opener shows a URL in the browser; Clipboard copies a file. The
	// demo and tests replace them.
	Opener    func(url string) error
	Clipboard func(path string) error
	// Notifier sends a desktop notification; with action, it waits for a
	// click and says whether it came.
	Notifier func(title, body, action string) bool
}

// Open opens prasar's config and posts.
func Open(p Paths) (*App, error) {
	c, err := LoadConfig(p)
	if err != nil {
		return nil, err
	}
	s, err := store.Open(filepath.Join(p.Data, "prasar.db"))
	if err != nil {
		return nil, err
	}
	return &App{Paths: p, Config: c, Store: s, Tokens: linkedin.Tokens{Dir: p.Data}, Now: time.Now,
		Opener: openURL, Clipboard: copyFile, Notifier: notify}, nil
}

// Close closes the database.
func (a *App) Close() error { return a.Store.Close() }

// ---------------------------------------------------------------- LinkedIn

// LinkedIn is the signed-in session, or why there isn't one.
func (a *App) LinkedIn() (linkedin.Token, error) {
	t, err := a.Tokens.Load()
	if err != nil {
		return t, err
	}
	if t.Left(a.Now()) <= 0 {
		return t, errors.New("the LinkedIn sign-in ran out (they last 60 days): run `prasar auth linkedin`")
	}
	return t, nil
}

// SignInLinkedIn signs in through the browser and keeps the session.
func (a *App) SignInLinkedIn(ctx context.Context, say func(string)) (linkedin.Token, error) {
	t, err := linkedin.SignIn(ctx, a.Config.LinkedInApp(), say)
	if err != nil {
		return t, err
	}
	return t, a.Tokens.Save(t)
}

// ---------------------------------------------------------------- posts

// New is an empty post with the configured networks ticked.
func (a *App) New() post.Post {
	return post.Post{Networks: append([]string(nil), a.Config.Networks...), Status: post.Draft, Origin: post.You}
}

// ResolveImages makes image paths absolute and checks they exist.
func ResolveImages(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			home, _ := os.UserHomeDir()
			p = filepath.Join(home, p[2:])
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", p, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("image %s is a folder", p)
		}
		switch strings.ToLower(filepath.Ext(abs)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		default:
			return nil, fmt.Errorf("image %s: use PNG, JPEG, GIF or WebP", p)
		}
		out = append(out, abs)
	}
	return out, nil
}

// SaveDraft stores a draft (new, or the same one again).
func (a *App) SaveDraft(p post.Post) (post.Post, error) {
	imgs, err := ResolveImages(p.Images)
	if err != nil {
		return p, err
	}
	p.Images = imgs
	p.Networks = post.Normalize(p.Networks)
	p.Status = post.Draft
	if p.ID == 0 {
		if p.Origin == "" {
			p.Origin = post.You
		}
		return a.Store.Add(p)
	}
	return p, a.Store.Save(p)
}

// Submit is the post leaving your hands. Yours is scheduled when SendAt
// is ahead, else published now (interactive: X's page opens now). An
// agent's always waits for your approval first: it goes out in public
// under your name.
func (a *App) Submit(ctx context.Context, p post.Post, interactive bool) (post.Post, error) {
	imgs, err := ResolveImages(p.Images)
	if err != nil {
		return p, err
	}
	p.Images = imgs
	p.Networks = post.Normalize(p.Networks)
	if msg := post.Check(p); msg != "" {
		return p, errors.New(msg)
	}
	if p.Origin == "" {
		p.Origin = post.You
	}
	if p.SendAt > 0 && p.SendAt <= a.Now().UnixMilli() {
		p.SendAt = 0
	}
	switch {
	case p.Origin == post.Agent:
		p.Status = post.Approval
	case p.SendAt > 0:
		p.Status = post.Scheduled
	default:
		p.Status = post.Ready
	}
	if p.ID == 0 {
		p, err = a.Store.Add(p)
	} else {
		err = a.Store.Save(p)
	}
	if err != nil {
		return p, err
	}
	if p.Origin == post.Agent {
		a.logAgent("queued #%d for approval: %s", p.ID, oneLine(p.Text, 80))
		return p, nil
	}
	if p.Status == post.Ready {
		return a.Publish(ctx, p, interactive)
	}
	return p, nil
}

// Approve says yes to an agent's post (text: your edit, or nil), then
// schedules or publishes it like your own.
func (a *App) Approve(ctx context.Context, id int64, text *string, interactive bool) (post.Post, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, err
	}
	if p.Status != post.Approval {
		return p, fmt.Errorf("#%d is %s, not waiting for approval", id, p.Status)
	}
	if text != nil {
		p.Text = *text
		if msg := post.Check(p); msg != "" {
			return p, errors.New(msg)
		}
	}
	if p.SendAt > 0 && p.SendAt > a.Now().UnixMilli() {
		p.Status = post.Scheduled
	} else {
		p.SendAt = 0
		p.Status = post.Ready
	}
	ok, err := a.Store.Transition(p, post.Approval)
	if err != nil || !ok {
		return p, firstErr(err, fmt.Errorf("#%d was answered elsewhere", id))
	}
	a.logAgent("approved #%d", id)
	if p.Status == post.Ready {
		return a.Publish(ctx, p, interactive)
	}
	return p, nil
}

// Decline says no to an agent's post.
func (a *App) Decline(id int64) error {
	p, err := a.Store.Get(id)
	if err != nil {
		return err
	}
	p.Status = post.Declined
	ok, err := a.Store.Transition(p, post.Approval)
	if err == nil && !ok {
		err = fmt.Errorf("#%d isn't waiting for approval", id)
	}
	if err == nil {
		a.logAgent("declined #%d", id)
	}
	return err
}

// Cancel takes back a post that hasn't fully gone out. What's already on
// a network stays there.
func (a *App) Cancel(id int64) error {
	p, err := a.Store.Get(id)
	if err != nil {
		return err
	}
	from := p.Status
	p.Status = post.Cancelled
	if from == post.Approval {
		p.Status = post.Declined
	}
	ok, err := a.Store.Transition(p, post.Scheduled, post.Approval, post.Ready, post.Failed, post.Draft)
	if err == nil && !ok {
		err = fmt.Errorf("#%d is %s: nothing to cancel", id, from)
	}
	return err
}

// Reschedule moves a scheduled post (or a draft) to another time.
func (a *App) Reschedule(id int64, at time.Time) (post.Post, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, err
	}
	if !at.After(a.Now()) {
		return p, errors.New("that time has passed")
	}
	if p.Status == post.Draft {
		if msg := post.Check(p); msg != "" {
			return p, errors.New(msg)
		}
	}
	p.SendAt = at.UnixMilli()
	if p.Status != post.Approval {
		p.Status = post.Scheduled
	}
	ok, err := a.Store.Transition(p, post.Scheduled, post.Draft, post.Approval)
	if err == nil && !ok {
		err = fmt.Errorf("#%d is %s: too late to move", id, p.Status)
	}
	return p, err
}

// Publish sends a post that is due: LinkedIn through the API; X's share
// page opened now when interactive, else left ready for your click (with
// a notification that opens it).
func (a *App) Publish(ctx context.Context, p post.Post, interactive bool) (post.Post, error) {
	if p.On(post.LinkedIn) && p.State(post.LinkedIn) != post.Done && p.State(post.LinkedIn) != post.Abandoned {
		a.publishLinkedIn(ctx, &p)
	}
	if p.On(post.X) && p.State(post.X) == post.Pending {
		if interactive {
			if err := a.openX(&p); err != nil {
				p.Set(post.X, post.Result{State: post.Error, Error: err.Error()})
			}
		} else {
			a.notifyX(p)
		}
	}
	a.settle(&p)
	if err := a.Store.Save(p); err != nil {
		return p, err
	}
	if r := p.Results[post.LinkedIn]; p.On(post.LinkedIn) && r.State == post.Error {
		return p, errors.New(r.Error)
	}
	return p, nil
}

func (a *App) publishLinkedIn(ctx context.Context, p *post.Post) {
	tok, err := a.LinkedIn()
	if err == nil {
		c := &linkedin.Client{Token: tok, Version: a.Config.LinkedIn.Version}
		var link string
		link, err = c.Post(ctx, post.LinkedInText(p.Text), p.Images)
		if err == nil {
			p.Set(post.LinkedIn, post.Result{State: post.Done, URL: link, At: a.Now().UnixMilli()})
			return
		}
	}
	p.Set(post.LinkedIn, post.Result{State: post.Error, Error: err.Error(), At: a.Now().UnixMilli()})
}

// settle works out the post's status from its networks.
func (a *App) settle(p *post.Post) {
	done, failed := p.Settled()
	switch {
	case done && failed:
		p.Status = post.Failed
	case done:
		p.Status = post.Posted
	case failed:
		p.Status = post.Failed
	default:
		p.Status = post.Ready
	}
}

// ---------------------------------------------------------------- X

// XParts is the post as X will get it: one part, or a thread.
func XParts(p post.Post) []string { return post.XParts(p.Text) }

// OpenX opens the next part of a post on X's share page.
func (a *App) OpenX(id int64) (post.Post, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, err
	}
	if !p.On(post.X) {
		return p, fmt.Errorf("#%d isn't for X", id)
	}
	if !p.Open() {
		return p, fmt.Errorf("#%d is %s", id, p.Status)
	}
	if p.Status == post.Scheduled || p.Status == post.Approval || p.Status == post.Draft {
		return p, fmt.Errorf("#%d is %s: it isn't due yet", id, p.Status)
	}
	if err := a.openX(&p); err != nil {
		return p, err
	}
	a.settle(&p)
	return p, a.Store.Save(p)
}

func (a *App) openX(p *post.Post) error {
	parts := XParts(*p)
	r := p.Results[post.X]
	if r.Part >= len(parts) {
		return errors.New("every part is already posted")
	}
	reply := ""
	if r.Part > 0 && len(r.Links) >= r.Part {
		reply = post.XStatusID(r.Links[r.Part-1])
	}
	if r.Part == 0 && len(p.Images) > 0 && a.Clipboard != nil {
		_ = a.Clipboard(p.Images[0]) // best effort: the page can't take images
	}
	if err := a.Opener(post.XIntent(parts[r.Part], reply)); err != nil {
		return fmt.Errorf("couldn't open the browser: %w", err)
	}
	r.State = post.Opened
	r.At = a.Now().UnixMilli()
	p.Set(post.X, r)
	return nil
}

// PostedX says you pressed Post on X for the part that's open: link is
// its address when you have it (the next part then opens as a reply).
// Returns whether more parts are left.
func (a *App) PostedX(id int64, link string) (post.Post, bool, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, false, err
	}
	if p.State(post.X) != post.Opened {
		return p, false, fmt.Errorf("#%d has no X page open", id)
	}
	r := p.Results[post.X]
	r.Links = append(r.Links, strings.TrimSpace(link))
	r.Part++
	more := r.Part < len(XParts(p))
	if more {
		r.State = post.Pending
	} else {
		r.State = post.Done
		r.URL = firstNonEmpty(r.Links...)
	}
	r.At = a.Now().UnixMilli()
	p.Set(post.X, r)
	a.settle(&p)
	return p, more, a.Store.Save(p)
}

// SkipX gives up on X for this post (LinkedIn is unaffected).
func (a *App) SkipX(id int64) (post.Post, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, err
	}
	r := p.Results[post.X]
	r.State = post.Abandoned
	p.Set(post.X, r)
	a.settle(&p)
	return p, a.Store.Save(p)
}

// CopyImage puts a post's image n on the clipboard, to paste on X.
func (a *App) CopyImage(p post.Post, n int) error {
	if n < 0 || n >= len(p.Images) {
		return errors.New("no such image")
	}
	return a.Clipboard(p.Images[n])
}

// Retry tries a failed LinkedIn post again.
func (a *App) Retry(ctx context.Context, id int64) (post.Post, error) {
	p, err := a.Store.Get(id)
	if err != nil {
		return p, err
	}
	if p.State(post.LinkedIn) != post.Error {
		return p, fmt.Errorf("#%d has nothing to retry", id)
	}
	p.Set(post.LinkedIn, post.Result{State: post.Pending})
	return a.Publish(ctx, p, false)
}

// ---------------------------------------------------------------- the schedule

// DeliverDue publishes every scheduled post whose time has come. A post
// is claimed (scheduled → ready) before anything is sent, so the screen,
// the MCP server and the service never publish one twice.
func (a *App) DeliverDue(ctx context.Context) []post.Post {
	due, err := a.Store.Due(a.Now())
	if err != nil {
		return nil
	}
	var out []post.Post
	for _, p := range due {
		claim := p
		claim.Status = post.Ready
		if ok, err := a.Store.Transition(claim, post.Scheduled); err != nil || !ok {
			continue
		}
		done, err := a.Publish(ctx, claim, false)
		if err != nil {
			a.Notifier("prasar: a scheduled post failed", oneLine(done.Text, 60)+"\n"+err.Error(), "")
		} else if done.Status == post.Posted {
			a.Notifier("prasar: posted", oneLine(done.Text, 80), "")
		}
		out = append(out, done)
	}
	return out
}

// notifyX says an X post is due, and opens it on a click.
func (a *App) notifyX(p post.Post) {
	go func() {
		if a.Notifier("prasar: your X post is ready", oneLine(p.Text, 90)+"\nOpen it and press Post.", "Open in browser") {
			_, _ = a.OpenX(p.ID)
		}
	}()
}

// AskApproval puts an agent's post in front of you: a tmux popup when
// there's a tmux to show it on, and a notification either way.
func (a *App) AskApproval(p post.Post) {
	a.Notifier("prasar: approve a post?", (firstNonEmpty(p.Requester, "An agent"))+" wants to post: "+oneLine(p.Text, 80)+
		"\nOpen prasar (or run prasar approve) to answer.", "")
	if os.Getenv("TMUX") == "" {
		return
	}
	args := []string{"display-popup", "-E", "-w", "86%", "-h", "84%", "-T", " prasar: approve post ",
		fmt.Sprintf("%s approve %d", shellQuote(Executable()), p.ID)}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args[:1], append([]string{"-t", pane}, args[1:]...)...)
	}
	cmd := exec.Command("tmux", args...)
	_ = cmd.Start()
	go func() { _ = cmd.Wait() }()
}

// WaitFor waits until post id leaves the given statuses.
func (a *App) WaitFor(ctx context.Context, id int64, statuses ...string) (post.Post, error) {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		p, err := a.Store.Get(id)
		if err != nil {
			return p, err
		}
		in := false
		for _, s := range statuses {
			in = in || p.Status == s
		}
		if !in {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-t.C:
		}
	}
}

// ---------------------------------------------------------------- the desktop

// Executable is this prasar binary.
func Executable() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "prasar"
}

func openURL(u string) error {
	if o := os.Getenv("PRASAR_OPENER"); o != "" {
		return exec.Command(o, u).Start()
	}
	return exec.Command("xdg-open", u).Start()
}

// copyFile puts an image on the clipboard (wl-copy, else xclip).
func copyFile(path string) error {
	mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
		".webp": "image/webp"}[strings.ToLower(filepath.Ext(path))]
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var cmd *exec.Cmd
	switch {
	case os.Getenv("WAYLAND_DISPLAY") != "" && have("wl-copy"):
		cmd = exec.Command("wl-copy", "--type", mime)
	case have("xclip"):
		cmd = exec.Command("xclip", "-selection", "clipboard", "-t", mime, "-i")
	default:
		return errors.New("no clipboard tool: install wl-clipboard (Wayland) or xclip (X11)")
	}
	cmd.Stdin = f
	return cmd.Run()
}

func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

// notify sends a desktop notification; with an action, waits (up to an
// hour) for a click on it.
func notify(title, body, action string) bool {
	if !have("notify-send") {
		return false
	}
	args := []string{"-a", "prasar", "-i", "mail-send", title, body}
	if action == "" {
		_ = exec.Command("notify-send", args...).Start()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	out, err := exec.CommandContext(ctx, "notify-send", append([]string{"--action=open=" + action, "--wait"}, args...)...).Output()
	return err == nil && strings.TrimSpace(string(out)) == "open"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (a *App) logAgent(format string, args ...any) {
	_ = os.MkdirAll(a.Paths.State, 0o700)
	f, err := os.OpenFile(filepath.Join(a.Paths.State, "agent.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", a.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// OneLine is s on one line, cut to n characters.
func OneLine(s string, n int) string { return oneLine(s, n) }

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
