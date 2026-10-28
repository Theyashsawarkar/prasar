package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/prasar/internal/post"
)

func TestDescribe(t *testing.T) {
	short := Describe("hello world")
	if !strings.Contains(short, "X: 11/280 characters, one post.") || !strings.Contains(short, "LinkedIn: 11/3000") {
		t.Fatal(short)
	}
	long := Describe(strings.Repeat("Words make sentences here. ", 30))
	if !strings.Contains(long, "X: a thread of") || !strings.Contains(long, "…see more") {
		t.Fatal(long)
	}
}

func TestOutcome(t *testing.T) {
	p := post.Post{ID: 3, Status: post.Ready, Networks: []string{post.X, post.LinkedIn}}
	p.Set(post.LinkedIn, post.Result{State: post.Done, URL: "https://www.linkedin.com/feed/update/urn:li:share:1/"})
	p.Set(post.X, post.Result{State: post.Opened})
	got := Outcome(p, time.Now())
	if !strings.Contains(got, "LinkedIn: posted https://www.linkedin.com/feed/update/urn:li:share:1/") ||
		!strings.Contains(got, "not confirmed yet") {
		t.Fatal(got)
	}
	p.Status, p.SendAt = post.Scheduled, time.Now().Add(26*time.Hour).UnixMilli()
	if !strings.Contains(Outcome(p, time.Now()), "Not posted yet") {
		t.Fatal(Outcome(p, time.Now()))
	}
}
