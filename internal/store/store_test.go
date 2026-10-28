package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Theyashsawarkar/prasar/internal/post"
)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAddGetList(t *testing.T) {
	s := open(t)
	p, err := s.Add(post.Post{Text: "hello", Networks: []string{post.X}, Status: post.Draft, Origin: post.You})
	if err != nil || p.ID == 0 {
		t.Fatal(p, err)
	}
	got, err := s.Get(p.ID)
	if err != nil || got.Text != "hello" || got.ID != p.ID {
		t.Fatal(got, err)
	}
	if _, err := s.Get(999); err == nil {
		t.Fatal("missing post found")
	}
	drafts, _ := s.List(post.Draft)
	if len(drafts) != 1 {
		t.Fatal(drafts)
	}
	if l, _ := s.List(post.Posted); len(l) != 0 {
		t.Fatal(l)
	}
}

func TestTransitionOnlyFromTheRightStatus(t *testing.T) {
	s := open(t)
	p, _ := s.Add(post.Post{Text: "x", Status: post.Scheduled, SendAt: 1})
	p.Status = post.Posted
	ok, err := s.Transition(p, post.Scheduled)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	p.Status = post.Cancelled
	if ok, _ := s.Transition(p, post.Scheduled, post.Approval); ok {
		t.Fatal("moved a posted post")
	}
	got, _ := s.Get(p.ID)
	if got.Status != post.Posted {
		t.Fatal(got.Status)
	}
}

func TestDue(t *testing.T) {
	s := open(t)
	past := time.Now().Add(-time.Minute).UnixMilli()
	future := time.Now().Add(time.Hour).UnixMilli()
	a, _ := s.Add(post.Post{Text: "due", Status: post.Scheduled, SendAt: past})
	s.Add(post.Post{Text: "later", Status: post.Scheduled, SendAt: future})
	s.Add(post.Post{Text: "draft", Status: post.Draft, SendAt: past})
	due, _ := s.Due(time.Now())
	if len(due) != 1 || due[0].ID != a.ID {
		t.Fatal(due)
	}
}

func TestStampMoves(t *testing.T) {
	s := open(t)
	before := s.Stamp()
	p, _ := s.Add(post.Post{Text: "x", Status: post.Draft})
	if s.Stamp() == before {
		t.Fatal("stamp didn't move on add")
	}
	mid := s.Stamp()
	time.Sleep(2 * time.Millisecond)
	p.Text = "y"
	s.Save(p)
	if s.Stamp() == mid {
		t.Fatal("stamp didn't move on save")
	}
}
