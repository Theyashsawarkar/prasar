// prasar: write once, post to X and LinkedIn. LinkedIn through its free
// API, X through its own share page (you press Post). A screen, a CLI, an
// MCP server for agents and a small background service for the schedule.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/Theyashsawarkar/prasar/internal/app"
	"github.com/Theyashsawarkar/prasar/internal/fake"
	"github.com/Theyashsawarkar/prasar/internal/linkedin"
	"github.com/Theyashsawarkar/prasar/internal/mcpserver"
	"github.com/Theyashsawarkar/prasar/internal/post"
	"github.com/Theyashsawarkar/prasar/internal/tui"
	"github.com/Theyashsawarkar/prasar/internal/when"
)

const usage = `prasar: write once, post to X and LinkedIn.

  prasar                         the screen (? in it lists every key)
  prasar post TEXT [options]     post from the shell (TEXT "-" reads stdin)
      --x / --linkedin             only there (default: both, or config's networks)
      --at WHEN                    schedule it: "tomorrow 9am", "fri 6pm", "in 2h"
      --image PATH                 attach an image (repeat for more)
      --draft                      keep it as a draft instead
  prasar check TEXT              how it fits: X's 280 (and its thread), LinkedIn's 3000
  prasar list [STATUS]           your posts: draft, scheduled, approval, ready, posted, failed
  prasar approve [ID]            answer an agent's post (the oldest waiting, without ID)
  prasar auth linkedin [--setup] sign in to LinkedIn (--setup asks for your app's id and secret)
  prasar auth status | logout
  prasar service install|remove|status   publish scheduled posts in the background
  prasar daemon                  the same, in the foreground
  prasar mcp [--requester NAME]  MCP server for AI agents (stdio)
  prasar doctor                  check the setup
  prasar demo                    try it with a fake LinkedIn (nothing real is posted)
  prasar --popup KEY             run inside a tmux popup: KEY closes it too
  prasar --version

X is posted with one click: prasar opens X's share page with the post
filled in, you press Post (X's API isn't free). Setup and every option:
https://github.com/Theyashsawarkar/prasar
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "":
		err = screen(args)
	case "post":
		err = postCmd(args)
	case "check":
		fmt.Println(mcpserver.Describe(textArg(args)))
	case "list", "ls":
		err = withApp(func(a *app.App) error { return list(a, args) })
	case "approve":
		err = withApp(func(a *app.App) error { return approve(a, args) })
	case "auth":
		err = withApp(func(a *app.App) error { return auth(a, args) })
	case "service":
		err = service(args)
	case "daemon":
		err = withApp(daemon)
	case "mcp":
		err = mcpCmd(args)
	case "doctor":
		err = withApp(doctor)
	case "demo":
		err = demo()
	case "keybindings": // scripts/keybindings, for Vayu's keybinding search
		fmt.Print(tui.KeybindingsFile())
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "prasar: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "prasar:", err)
		os.Exit(1)
	}
}

func withApp(f func(*app.App) error) error {
	a, err := app.Open(app.DefaultPaths())
	if err != nil {
		return err
	}
	defer a.Close()
	return f(a)
}

func screen(args []string) error {
	fs := flag.NewFlagSet("prasar", flag.ExitOnError)
	popup := fs.String("popup", "", "")
	version := fs.Bool("version", false, "")
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")
	fs.Usage = func() { fmt.Print(usage) }
	_ = fs.Parse(args)
	switch {
	case *version:
		fmt.Println("prasar", app.Version)
		return nil
	case *help:
		fmt.Print(usage)
		return nil
	}
	return withApp(func(a *app.App) error { return run(a, tui.Options{PopupKey: *popup}) })
}

func run(a *app.App, opt tui.Options) error {
	_, err := tea.NewProgram(tui.New(a, opt), tea.WithAltScreen()).Run()
	return err
}

func textArg(args []string) string {
	t := strings.Join(args, " ")
	if t == "-" || t == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		b, _ := io.ReadAll(os.Stdin)
		t = string(b)
	}
	return strings.TrimSpace(t)
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func postCmd(args []string) error {
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	onX := fs.Bool("x", false, "")
	onLI := fs.Bool("linkedin", false, "")
	at := fs.String("at", "", "")
	draft := fs.Bool("draft", false, "")
	var images multi
	fs.Var(&images, "image", "")
	fs.Usage = func() { fmt.Print(usage) }
	// flags may come before or after the text
	var words []string
	for len(args) > 0 {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			words = append(words, args[0])
			args = args[1:]
		}
	}
	text := textArg(words)
	return withApp(func(a *app.App) error {
		p := a.New()
		p.Text, p.Images = text, images
		if *onX || *onLI {
			p.Networks = nil
			if *onX {
				p.Networks = append(p.Networks, post.X)
			}
			if *onLI {
				p.Networks = append(p.Networks, post.LinkedIn)
			}
		}
		if *draft {
			got, err := a.SaveDraft(p)
			if err == nil {
				fmt.Printf("Saved draft #%d.\n", got.ID)
			}
			return err
		}
		if *at != "" {
			t, err := when.Parse(*at, a.Now())
			if err != nil {
				return err
			}
			p.SendAt = t.UnixMilli()
		}
		got, err := a.Submit(context.Background(), p, true)
		if got.ID == 0 {
			return err
		}
		if got.Status == post.Scheduled {
			fmt.Printf("Scheduled #%d for %s.\n", got.ID, when.Say(time.UnixMilli(got.SendAt), a.Now()))
			if !serviceActive() {
				fmt.Println("Note: the background service isn't running, so it goes out only while prasar is open: prasar service install")
			}
			return nil
		}
		if r := got.Results[post.LinkedIn]; got.On(post.LinkedIn) {
			if r.State == post.Done {
				fmt.Println("LinkedIn: posted", r.URL)
			} else {
				fmt.Println("LinkedIn: failed:", r.Error)
			}
		}
		if got.On(post.X) {
			return confirmX(a, got)
		}
		return nil
	})
}

// confirmX walks you through X's share page, part by part.
func confirmX(a *app.App, p post.Post) error {
	in := bufio.NewReader(os.Stdin)
	for p.State(post.X) == post.Opened {
		parts := len(post.XParts(p.Text))
		part := p.Results[post.X].Part
		label := "X"
		if parts > 1 {
			label = fmt.Sprintf("X (part %d of %d)", part+1, parts)
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Printf("%s: opened in your browser; press Post there. Confirm it with: prasar (Queue, o)\n", label)
			return nil
		}
		fmt.Printf("%s: opened in your browser with the post filled in. Press Post there,\nthen paste its link here (optional) and press Enter (s skips X): ", label)
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "s" {
			_, err := a.SkipX(p.ID)
			return err
		}
		got, more, err := a.PostedX(p.ID, line)
		if err != nil {
			return err
		}
		p = got
		if more {
			if p, err = a.OpenX(p.ID); err != nil {
				return err
			}
		}
	}
	fmt.Println("X: posted.")
	return nil
}

func list(a *app.App, args []string) error {
	ps, err := a.Store.List(args...)
	if err != nil {
		return err
	}
	for _, p := range ps {
		at := ""
		if p.SendAt > 0 && p.Status == post.Scheduled {
			at = " " + when.Say(time.UnixMilli(p.SendAt), a.Now())
		}
		var to []string
		for _, n := range p.Networks {
			to = append(to, post.Name(n)+":"+p.State(n))
		}
		fmt.Printf("#%-4d %-9s%s [%s] %s\n", p.ID, p.Status, at, strings.Join(to, " "), app.OneLine(p.Text, 70))
	}
	return nil
}

func approve(a *app.App, args []string) error {
	var id int64
	if len(args) > 0 {
		n, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
		if err != nil {
			return fmt.Errorf("approve takes a post id, like 12")
		}
		id = n
	} else {
		waiting, _ := a.Store.List(post.Approval)
		if len(waiting) == 0 {
			fmt.Println("Nothing is waiting for your approval.")
			return nil
		}
		id = waiting[len(waiting)-1].ID
	}
	return run(a, tui.Options{Approve: id})
}

func auth(a *app.App, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		fmt.Println(mcpserver.Accounts(a))
		return nil
	case "logout":
		a.Tokens.Delete()
		fmt.Println("Signed out of LinkedIn.")
		return nil
	case "linkedin":
	default:
		return fmt.Errorf("auth linkedin, auth status or auth logout")
	}
	setup := len(args) > 1 && args[1] == "--setup"
	if setup || a.Config.LinkedIn.ClientID == "" || a.Config.LinkedIn.ClientSecret == "" {
		if err := setupLinkedIn(a); err != nil {
			return err
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, cancel2 := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel2()
	fmt.Println("Opening LinkedIn in your browser to sign in…")
	t, err := a.SignInLinkedIn(ctx, func(u string) { fmt.Println("If it didn't open:", u) })
	if err != nil {
		return err
	}
	fmt.Printf("Signed in as %s. It lasts %d days (LinkedIn's limit); prasar says when it runs low.\n", t.Name, int(t.Left(a.Now()).Hours()/24))
	return nil
}

func setupLinkedIn(a *app.App) error {
	redirect := a.Config.LinkedInApp().Redirect()
	fmt.Printf(`prasar posts to LinkedIn through an app of your own (free, about 2 minutes):

  1. https://www.linkedin.com/developers/apps/new : any name, your LinkedIn
     page as the company (LinkedIn needs one; a page you create works), a logo.
  2. Products tab: add "Share on LinkedIn" and "Sign In with LinkedIn using
     OpenID Connect" (both self-serve, approved at once).
  3. Auth tab: under "Authorized redirect URLs" add
         %s
     and copy the Client ID and Primary Client Secret from the same tab.

`, redirect)
	in := bufio.NewReader(os.Stdin)
	fmt.Print("Client ID: ")
	id, _ := in.ReadString('\n')
	fmt.Print("Client Secret (hidden): ")
	var secret string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		secret = string(b)
	} else {
		secret, _ = in.ReadString('\n')
	}
	id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
	if id == "" || secret == "" {
		return errors.New("both the Client ID and the Client Secret are needed")
	}
	a.Config.LinkedIn.ClientID, a.Config.LinkedIn.ClientSecret = id, secret
	if err := app.SaveConfig(a.Paths, a.Config); err != nil {
		return err
	}
	fmt.Println("Saved to", a.Paths.ConfigFile(), "(private to you).")
	return nil
}

func daemon(a *app.App) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintln(os.Stderr, "prasar: publishing scheduled posts as they fall due")
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		for _, p := range a.DeliverDue(ctx) {
			fmt.Fprintf(os.Stderr, "prasar: #%d due: %s\n", p.ID, p.Status)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func unitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", "prasar.service")
}

func serviceActive() bool {
	return exec.Command("systemctl", "--user", "is-active", "--quiet", "prasar.service").Run() == nil
}

func service(args []string) error {
	if len(args) == 0 {
		return errors.New("service install, remove or status")
	}
	sc := func(args ...string) error {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	switch args[0] {
	case "install":
		unit := fmt.Sprintf(`[Unit]
Description=prasar: publishes your scheduled posts
Documentation=https://github.com/Theyashsawarkar/prasar
StartLimitIntervalSec=0

[Service]
ExecStart=%s daemon
Restart=on-failure
RestartSec=2
RestartSteps=5
RestartMaxDelaySec=60

[Install]
WantedBy=default.target
`, app.Executable())
		if err := os.MkdirAll(filepath.Dir(unitPath()), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(unitPath(), []byte(unit), 0o644); err != nil {
			return err
		}
		if err := sc("daemon-reload"); err != nil {
			return err
		}
		if err := sc("enable", "--now", "prasar.service"); err != nil {
			return err
		}
		fmt.Printf("Installed %s and started it.\nLogs: journalctl --user -u prasar -f\n", unitPath())
		return nil
	case "remove", "uninstall":
		_ = sc("disable", "--now", "prasar.service")
		if err := os.Remove(unitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = sc("daemon-reload")
		fmt.Println("Removed the background service.")
		return nil
	case "status":
		return sc("status", "--no-pager", "prasar.service")
	}
	return fmt.Errorf("unknown service command %q", args[0])
}

func mcpCmd(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	requester := fs.String("requester", "an AI agent", "")
	_ = fs.Parse(args)
	return withApp(func(a *app.App) error {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return mcpserver.Run(ctx, a, *requester)
	})
}

func doctor(a *app.App) error {
	ok, bad := "✓", "✗"
	line := func(good bool, what, detail string) {
		mark := ok
		if !good {
			mark = bad
		}
		fmt.Printf("%s %-22s %s\n", mark, what, detail)
	}
	have := func(b string) bool { _, err := exec.LookPath(b); return err == nil }
	line(true, "config", a.Paths.ConfigFile())
	line(a.Config.LinkedIn.ClientID != "", "LinkedIn app", map[bool]string{true: "set", false: "not set: prasar auth linkedin --setup"}[a.Config.LinkedIn.ClientID != ""])
	if t, err := a.LinkedIn(); err != nil {
		line(false, "LinkedIn sign-in", err.Error())
	} else {
		days := int(t.Left(a.Now()).Hours() / 24)
		line(days >= 7, "LinkedIn sign-in", fmt.Sprintf("%s, %d days left", t.Name, days))
	}
	line(have("xdg-open"), "browser (xdg-open)", "opens X's share page")
	line(have("wl-copy") || have("xclip"), "clipboard", "wl-copy or xclip: images for X, copying links")
	line(have("notify-send"), "notifications", "notify-send: scheduled X posts, agents' posts")
	line(serviceActive(), "background service", map[bool]string{true: "running", false: "not running: prasar service install"}[serviceActive()])
	if have("secret-tool") {
		line(true, "keyring", "the LinkedIn sign-in is kept in your keyring")
	} else {
		line(true, "keyring", "no secret-tool: the LinkedIn sign-in is in a private file")
	}
	return nil
}

// demo is prasar with a fake LinkedIn, in a throwaway folder: nothing
// real is posted, and X's page isn't opened.
func demo() error {
	dir, err := os.MkdirTemp("", "prasar-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f := fake.New()
	defer f.Close()
	os.Setenv("PRASAR_HOME", dir)
	os.Setenv("PRASAR_LINKEDIN_URL", f.URL)
	os.Setenv("PRASAR_NO_KEYRING", "1")
	a, err := app.Open(app.DefaultPaths())
	if err != nil {
		return err
	}
	defer a.Close()
	a.Opener = func(string) error { return nil }
	a.Clipboard = func(string) error { return nil }
	a.Notifier = func(string, string, string) bool { return false }
	a.Config.X.Handle = "asha_builds"
	_ = a.Tokens.Save(linkedin.Token{Access: "fake-token", Sub: "fake-member", Name: "Asha Demo",
		Expires: time.Now().Add(52 * 24 * time.Hour).Unix()})
	seed(a)
	return run(a, tui.Options{})
}

func seed(a *app.App) {
	ctx := context.Background()
	now := time.Now()
	d := a.New()
	d.Text = "Half-written thought about terminal tools: the best ones disappear into your hands. #cli"
	_, _ = a.SaveDraft(d)

	s := a.New()
	s.Text = "This week I moved my whole posting flow into the terminal. One editor, both networks, a live preview of each. Here's what changed. #buildinpublic"
	s.SendAt = now.Add(26 * time.Hour).UnixMilli()
	_, _ = a.Submit(ctx, s, false)

	ag := a.New()
	ag.Text = "Shipped: prasar 0.1, a terminal app that posts to X and LinkedIn from one editor. LinkedIn goes out through its API, X opens with the post filled in. Free, open source. #golang"
	ag.Origin, ag.Requester, ag.Note = post.Agent, "Claude Code", "You asked for a launch post for the release"
	_, _ = a.Submit(ctx, ag, false)

	done := a.New()
	done.Networks = []string{post.LinkedIn}
	done.Text = "Grateful for the team that made this quarter's launch happen."
	_, _ = a.Submit(ctx, done, false)
}
