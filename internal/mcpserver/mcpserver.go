// Package mcpserver lets AI agents write posts for you over MCP: check a
// text against each network's rules, save drafts, and ask to publish.
// Every publish waits for your approval in a popup: a post goes out in
// public under your name.
package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Theyashsawarkar/prasar/internal/app"
	"github.com/Theyashsawarkar/prasar/internal/post"
	"github.com/Theyashsawarkar/prasar/internal/when"
)

const instructions = `prasar posts to the user's X and LinkedIn.

- Write posts in the user's voice, then check them with post_check (X counts 280 weighted characters; a longer text becomes a thread, or split it yourself with a line holding only "---"; LinkedIn takes 3000 and folds after about 210 with "…see more").
- post_draft saves a draft the user can finish in prasar. post_publish asks to publish (now, or later with send_at): the user always approves it in a popup first and may edit it. Never claim something was posted unless the result says so.
- LinkedIn posts go out through its API. X has no free API: after approval prasar opens X's share page with the post filled in and the user presses Post themselves, so an X post is "waiting for the user's click" until they confirm it.
- Images are local paths (PNG, JPEG, GIF, WebP), only ones the user asked for.`

type server struct{ a *app.App }

// Run serves the tools on stdin/stdout until ctx ends, delivering
// scheduled posts meanwhile.
func Run(ctx context.Context, a *app.App, requester string) error {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.DeliverDue(ctx)
			}
		}
	}()
	return New(a, requester).Run(ctx, &mcp.StdioTransport{})
}

// New builds the server.
func New(a *app.App, requester string) *mcp.Server {
	s := &server{a: a}
	srv := mcp.NewServer(&mcp.Implementation{Name: "prasar", Version: app.Version}, &mcp.ServerOptions{Instructions: instructions})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(srv, &mcp.Tool{Name: "post_check", Annotations: ro,
		Description: "Check a post against X's and LinkedIn's rules before drafting or publishing: its lengths, how X would split it into a thread, and what LinkedIn shows before \"…see more\"."}, s.check)
	mcp.AddTool(srv, &mcp.Tool{Name: "post_draft",
		Description: "Save a post as a draft in prasar, without publishing. The user finishes and posts it from prasar. Returns its id."}, s.draft)
	mcp.AddTool(srv, &mcp.Tool{Name: "post_publish",
		Description: "Ask to publish a post on X and/or LinkedIn, now or later (send_at: \"tomorrow 9am\", \"fri 6pm\", \"in 2h\"). The user always approves it in a popup first and may edit it; the call waits for the answer (up to 10 minutes) and says what happened. X needs the user's own click on X's page."}, func(ctx context.Context, r *mcp.CallToolRequest, in publishIn) (*mcp.CallToolResult, any, error) {
		return s.publish(ctx, r, in, requester)
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "post_list", Annotations: ro,
		Description: "List the user's posts in prasar: drafts, scheduled, waiting (for approval or the user's click on X), posted (with links) and failed. Newest first."}, s.list)
	mcp.AddTool(srv, &mcp.Tool{Name: "post_accounts", Annotations: ro,
		Description: "Which networks prasar can post to now: LinkedIn's sign-in (and when it runs out), X's one-click posting."}, s.accounts)
	return srv
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func fail(err error) (*mcp.CallToolResult, any, error) {
	r := text(err.Error())
	r.IsError = true
	return r, nil, nil
}

type checkIn struct {
	Text string `json:"text" jsonschema:"the post's text"`
}

func (s *server) check(_ context.Context, _ *mcp.CallToolRequest, in checkIn) (*mcp.CallToolResult, any, error) {
	return text(Describe(in.Text)), nil, nil
}

// Describe is a text measured against both networks.
func Describe(t string) string {
	var b strings.Builder
	parts := post.XParts(t)
	if len(parts) <= 1 {
		fmt.Fprintf(&b, "X: %d/%d characters, one post.\n", post.XLength(t), post.XLimit)
	} else {
		fmt.Fprintf(&b, "X: a thread of %d posts:\n", len(parts))
		for i, p := range parts {
			fmt.Fprintf(&b, "  %d. (%d/%d) %s\n", i+1, post.XLength(p), post.XLimit, app.OneLine(p, 100))
		}
	}
	n := post.LinkedInLength(t)
	fmt.Fprintf(&b, "LinkedIn: %d/%d characters", n, post.LinkedInLimit)
	if n > post.LinkedInLimit {
		b.WriteString(" (TOO LONG)")
	}
	if n > post.LinkedInFold {
		r := []rune(post.LinkedInBody(t))
		fmt.Fprintf(&b, ". Before \"…see more\": %q", string(r[:post.LinkedInFold]))
	}
	b.WriteString(".")
	return b.String()
}

type draftIn struct {
	Text     string   `json:"text" jsonschema:"the post"`
	Networks []string `json:"networks,omitempty" jsonschema:"x and/or linkedin; default the user's usual ones"`
	Images   []string `json:"images,omitempty" jsonschema:"local image paths the user asked for"`
}

func (s *server) draft(_ context.Context, _ *mcp.CallToolRequest, in draftIn) (*mcp.CallToolResult, any, error) {
	p := s.a.New()
	p.Text, p.Images, p.Origin = in.Text, in.Images, post.Agent
	if len(in.Networks) > 0 {
		p.Networks = in.Networks
	}
	p, err := s.a.SaveDraft(p)
	if err != nil {
		return fail(err)
	}
	return text(fmt.Sprintf("Saved as draft #%d for %s. The user can finish and post it in prasar (Drafts).", p.ID, names(p.Networks))), nil, nil
}

type publishIn struct {
	Text     string   `json:"text" jsonschema:"the post"`
	Networks []string `json:"networks,omitempty" jsonschema:"x and/or linkedin; default the user's usual ones"`
	Images   []string `json:"images,omitempty" jsonschema:"local image paths the user asked for"`
	SendAt   string   `json:"send_at,omitempty" jsonschema:"when, e.g. tomorrow 9am, fri 6pm, in 2h; empty: right after approval"`
	Note     string   `json:"note,omitempty" jsonschema:"one line for the user on why this post, shown when they approve"`
}

func (s *server) publish(ctx context.Context, _ *mcp.CallToolRequest, in publishIn, requester string) (*mcp.CallToolResult, any, error) {
	p := s.a.New()
	p.Text, p.Images, p.Origin, p.Note, p.Requester = in.Text, in.Images, post.Agent, in.Note, requester
	if len(in.Networks) > 0 {
		p.Networks = in.Networks
	}
	if in.SendAt != "" {
		at, err := when.Parse(in.SendAt, s.a.Now())
		if err != nil {
			return fail(err)
		}
		p.SendAt = at.UnixMilli()
	}
	p, err := s.a.Submit(ctx, p, false)
	if err != nil {
		return fail(err)
	}
	s.a.AskApproval(p)
	wctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	p, err = s.a.WaitFor(wctx, p.ID, post.Approval)
	cancel()
	if err != nil {
		return text(fmt.Sprintf("Still waiting for the user's approval (post #%d); NOT posted. They can answer in prasar or with `prasar approve %d`.", p.ID, p.ID)), nil, nil
	}
	if p.Status == post.Declined || p.Status == post.Cancelled {
		return text(fmt.Sprintf("The user declined post #%d. Nothing was posted.", p.ID)), nil, nil
	}
	// the approval popup publishes: give it time to hear back from LinkedIn
	deadline := time.Now().Add(90 * time.Second)
	for p.Status == post.Ready && p.On(post.LinkedIn) && p.State(post.LinkedIn) == post.Pending && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return text(Outcome(p, s.a.Now())), nil, nil
		case <-time.After(300 * time.Millisecond):
		}
		if q, err := s.a.Store.Get(p.ID); err == nil {
			p = q
		}
	}
	if p.Text != in.Text {
		return text(Outcome(p, s.a.Now()) + "\n\nThe user edited it before approving:\n" + p.Text), nil, nil
	}
	return text(Outcome(p, s.a.Now())), nil, nil
}

// Outcome says where a post stands, network by network.
func Outcome(p post.Post, now time.Time) string {
	var lines []string
	switch p.Status {
	case post.Scheduled:
		return fmt.Sprintf("Approved and scheduled for %s (post #%d). Not posted yet.", when.Say(time.UnixMilli(p.SendAt), now), p.ID)
	case post.Approval:
		return fmt.Sprintf("Post #%d is waiting for the user's approval.", p.ID)
	case post.Draft:
		return fmt.Sprintf("Post #%d is a draft.", p.ID)
	case post.Declined, post.Cancelled:
		return fmt.Sprintf("Post #%d was %s. Nothing more will be posted.", p.ID, p.Status)
	}
	for _, n := range p.Networks {
		r := p.Results[n]
		switch p.State(n) {
		case post.Done:
			lines = append(lines, fmt.Sprintf("%s: posted %s", post.Name(n), r.URL))
		case post.Opened:
			lines = append(lines, post.Name(n)+": X's page is open in the user's browser; waiting for them to press Post (not confirmed yet)")
		case post.Pending:
			if n == post.X {
				lines = append(lines, "X: ready, waiting for the user to open it and press Post (not posted yet)")
			} else {
				lines = append(lines, post.Name(n)+": not posted yet")
			}
		case post.Error:
			lines = append(lines, fmt.Sprintf("%s: FAILED: %s", post.Name(n), r.Error))
		case post.Abandoned:
			lines = append(lines, post.Name(n)+": skipped by the user")
		}
	}
	return fmt.Sprintf("Post #%d (%s):\n%s", p.ID, p.Status, strings.Join(lines, "\n"))
}

type listIn struct {
	Status string `json:"status,omitempty" jsonschema:"draft, scheduled, approval, ready, posted, failed; empty: all"`
	Limit  int    `json:"limit,omitempty" jsonschema:"at most this many (default 20)"`
}

func (s *server) list(_ context.Context, _ *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
	var st []string
	if in.Status != "" {
		st = []string{in.Status}
	}
	ps, err := s.a.Store.List(st...)
	if err != nil {
		return fail(err)
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	var b strings.Builder
	for i, p := range ps {
		if i >= in.Limit {
			break
		}
		at := ""
		if p.SendAt > 0 {
			at = " at " + when.Say(time.UnixMilli(p.SendAt), s.a.Now())
		}
		fmt.Fprintf(&b, "#%d %s%s [%s] %s\n", p.ID, p.Status, at, names(p.Networks), app.OneLine(p.Text, 90))
		for _, n := range p.Networks {
			if r := p.Results[n]; r.URL != "" {
				fmt.Fprintf(&b, "    %s: %s\n", post.Name(n), r.URL)
			}
		}
	}
	if b.Len() == 0 {
		return text("No posts."), nil, nil
	}
	return text(b.String()), nil, nil
}

func (s *server) accounts(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return text(Accounts(s.a)), nil, nil
}

// Accounts says what prasar can post to now.
func Accounts(a *app.App) string {
	li := "LinkedIn: "
	if t, err := a.LinkedIn(); err != nil {
		li += "not ready: " + err.Error()
	} else {
		li += fmt.Sprintf("signed in as %s, for %d more days", t.Name, int(t.Left(a.Now()).Hours()/24))
	}
	return li + "\nX: one click: prasar opens X's share page with the post filled in, the user presses Post (no API, free)."
}

func names(ns []string) string {
	var out []string
	for _, n := range ns {
		out = append(out, post.Name(n))
	}
	return strings.Join(out, " + ")
}
