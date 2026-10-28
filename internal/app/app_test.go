package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Theyashsawarkar/prasar/internal/fake"
	"github.com/Theyashsawarkar/prasar/internal/linkedin"
	"github.com/Theyashsawarkar/prasar/internal/post"
)

type desk struct {
	mu      sync.Mutex
	opened  []string
	copied  []string
	notes   []string
	clickOn bool
}

func (d *desk) open(u string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opened = append(d.opened, u)
	return nil
}
func (d *desk) copy(p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.copied = append(d.copied, p)
	return nil
}
func (d *desk) note(t, b, action string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.notes = append(d.notes, t)
	return action != "" && d.clickOn
}
func (d *desk) last() string { d.mu.Lock(); defer d.mu.Unlock(); return d.opened[len(d.opened)-1] }

func newApp(t *testing.T, signedIn bool) (*App, *fake.LinkedIn, *desk) {
	t.Setenv("PRASAR_NO_KEYRING", "1")
	f := fake.New()
	t.Cleanup(f.Close)
	t.Setenv("PRASAR_LINKEDIN_URL", f.URL)
	t.Setenv("TMUX", "")
	h := t.TempDir()
	p := Paths{filepath.Join(h, "c"), filepath.Join(h, "d"), filepath.Join(h, "s")}
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := &desk{}
	a.Opener, a.Clipboard, a.Notifier = d.open, d.copy, d.note
	if signedIn {
		a.Tokens.Save(linkedin.Token{Access: "fake-token", Sub: "fake-member", Name: "Asha Demo", Expires: time.Now().Add(50 * 24 * time.Hour).Unix()})
	}
	return a, f, d
}

func TestPostNowToBoth(t *testing.T) {
	a, f, d := newApp(t, true)
	img := filepath.Join(t.TempDir(), "shot.png")
	os.WriteFile(img, []byte("png"), 0o644)
	p := a.New()
	p.Text = "Shipped prasar today #golang"
	p.Images = []string{img}
	got, err := a.Submit(context.Background(), p, true)
	if err != nil {
		t.Fatal(err)
	}
	// LinkedIn went through the API, escaped, with the hashtag
	posts := f.Posts()
	if len(posts) != 1 || posts[0].Commentary != `Shipped prasar today {hashtag|\#|golang}` || len(posts[0].Images) != 1 {
		t.Fatal(posts)
	}
	if got.State(post.LinkedIn) != post.Done || !strings.Contains(got.Results[post.LinkedIn].URL, posts[0].URN) {
		t.Fatal(got.Results)
	}
	// X: the share page is open and the image is on the clipboard; not posted until you say so
	if got.State(post.X) != post.Opened || got.Status != post.Ready {
		t.Fatal(got.Status, got.Results)
	}
	if !strings.HasPrefix(d.last(), "https://x.com/intent/post?text=Shipped+prasar+today+%23golang") || d.copied[0] != img {
		t.Fatal(d.opened, d.copied)
	}
	done, more, err := a.PostedX(got.ID, "https://x.com/asha/status/111")
	if err != nil || more || done.Status != post.Posted || done.Results[post.X].URL != "https://x.com/asha/status/111" {
		t.Fatal(done, more, err)
	}
}

func TestXThreadChainsReplies(t *testing.T) {
	a, _, d := newApp(t, true)
	p := a.New()
	p.Networks = []string{post.X}
	p.Text = "one\n---\ntwo\n---\nthree"
	got, err := a.Submit(context.Background(), p, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.last(), "text=one+1%2F3") {
		t.Fatal(d.last())
	}
	got, more, _ := a.PostedX(got.ID, "https://x.com/a/status/10")
	if !more || got.Status != post.Ready || got.State(post.X) != post.Pending {
		t.Fatal(got)
	}
	got, _ = a.OpenX(got.ID)
	if !strings.Contains(d.last(), "in_reply_to=10") || !strings.Contains(d.last(), "two+2%2F3") {
		t.Fatal(d.last())
	}
	got, more, _ = a.PostedX(got.ID, "") // no link: the next part can't be a reply
	got, _ = a.OpenX(got.ID)
	if strings.Contains(d.last(), "in_reply_to") {
		t.Fatal("replied without a link:", d.last())
	}
	got, more, _ = a.PostedX(got.ID, "https://x.com/a/status/30")
	if more || got.Status != post.Posted {
		t.Fatal(got)
	}
}

func TestAgentPostsWaitForApproval(t *testing.T) {
	a, f, _ := newApp(t, true)
	p := a.New()
	p.Text = "From the agent"
	p.Networks = []string{post.LinkedIn}
	p.Origin = post.Agent
	got, err := a.Submit(context.Background(), p, false)
	if err != nil || got.Status != post.Approval {
		t.Fatal(got.Status, err)
	}
	if len(f.Posts()) != 0 {
		t.Fatal("posted before approval")
	}
	edited := "From the agent, edited by me"
	got, err = a.Approve(context.Background(), got.ID, &edited, false)
	if err != nil || got.Status != post.Posted || f.Posts()[0].Commentary != "From the agent, edited by me" {
		t.Fatal(got, err)
	}
	if _, err := a.Approve(context.Background(), got.ID, nil, false); err == nil {
		t.Fatal("approved twice")
	}

	p2 := a.New()
	p2.Text, p2.Origin = "no thanks", post.Agent
	got2, _ := a.Submit(context.Background(), p2, false)
	if err := a.Decline(got2.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := a.Store.Get(got2.ID); g.Status != post.Declined {
		t.Fatal(g.Status)
	}
	if len(f.Posts()) != 1 {
		t.Fatal("declined post went out")
	}
}

func TestScheduleDeliversOnce(t *testing.T) {
	a, f, d := newApp(t, true)
	d.clickOn = true
	now := time.Now()
	a.Now = func() time.Time { return now }
	p := a.New()
	p.Text = "Later"
	p.SendAt = now.Add(time.Hour).UnixMilli()
	got, err := a.Submit(context.Background(), p, true)
	if err != nil || got.Status != post.Scheduled || len(d.opened) != 0 {
		t.Fatal(got.Status, err, d.opened)
	}
	if out := a.DeliverDue(context.Background()); len(out) != 0 {
		t.Fatal("delivered early")
	}
	now = now.Add(2 * time.Hour)
	out := a.DeliverDue(context.Background())
	if len(out) != 1 || len(f.Posts()) != 1 {
		t.Fatal(out, f.Posts())
	}
	if again := a.DeliverDue(context.Background()); len(again) != 0 || len(f.Posts()) != 1 {
		t.Fatal("delivered twice")
	}
	// X waits for your click: the notification's button opens the page
	deadline := time.Now().Add(3 * time.Second)
	for {
		g, _ := a.Store.Get(got.ID)
		if g.State(post.X) == post.Opened {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the notification's click didn't open X:", g.Results)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLinkedInFailures(t *testing.T) {
	a, f, _ := newApp(t, false)
	p := a.New()
	p.Text = "hello"
	p.Networks = []string{post.LinkedIn}
	got, err := a.Submit(context.Background(), p, false)
	if err == nil || got.Status != post.Failed || !strings.Contains(got.Results[post.LinkedIn].Error, "prasar auth linkedin") {
		t.Fatal(got, err)
	}
	a.Tokens.Save(linkedin.Token{Access: "fake-token", Sub: "fake-member", Expires: time.Now().Add(time.Hour).Unix()})
	got, err = a.Retry(context.Background(), got.ID)
	if err != nil || got.Status != post.Posted || len(f.Posts()) != 1 {
		t.Fatal(got, err)
	}
}

func TestCancelRescheduleAndChecks(t *testing.T) {
	a, _, _ := newApp(t, true)
	if _, err := a.Submit(context.Background(), post.Post{Text: " ", Networks: []string{post.X}}, false); err == nil {
		t.Fatal("empty post submitted")
	}
	if _, err := a.Submit(context.Background(), post.Post{Text: "x", Networks: []string{post.X}, Images: []string{"/nope.png"}}, false); err == nil {
		t.Fatal("missing image accepted")
	}
	d, _ := a.SaveDraft(post.Post{Text: "draft", Networks: []string{post.X}})
	got, err := a.Reschedule(d.ID, time.Now().Add(time.Hour))
	if err != nil || got.Status != post.Scheduled {
		t.Fatal(got, err)
	}
	if err := a.Cancel(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.Cancel(d.ID); err == nil {
		t.Fatal("cancelled twice")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	p := Paths{Config: t.TempDir()}
	c, _ := LoadConfig(p)
	if strings.Join(c.Networks, ",") != "x,linkedin" {
		t.Fatal(c.Networks)
	}
	c.LinkedIn.ClientID, c.LinkedIn.ClientSecret = "id", "s3cret"
	c.Networks = []string{"linkedin"}
	if err := SaveConfig(p, c); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p.ConfigFile())
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	c2, err := LoadConfig(p)
	if err != nil || c2.LinkedIn.ClientSecret != "s3cret" || strings.Join(c2.Networks, ",") != "linkedin" {
		t.Fatal(c2, err)
	}
}
