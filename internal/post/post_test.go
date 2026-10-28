package post

import (
	"strings"
	"testing"
)

func TestXLength(t *testing.T) {
	for in, want := range map[string]int{
		"hello":                               5,
		"hello https://example.com/a/b/c/d/e": 6 + 23,
		"नमस्ते":                              6, // Devanagari is in the weight-1 range; the virama/vowel signs are marks
		"日本":                                  4,
		"ship it 🚀":                           10,
		"see github.com/x/y":                  4 + 23,
	} {
		if got := XLength(in); got != want {
			t.Errorf("XLength(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestXPartsShort(t *testing.T) {
	if got := XParts("just one"); len(got) != 1 || got[0] != "just one" {
		t.Fatalf("got %q", got)
	}
}

func TestXPartsBreaks(t *testing.T) {
	got := XParts("first\n---\nsecond\n  ---  \nthird")
	want := []string{"first 1/3", "second 2/3", "third 3/3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestXPartsLong(t *testing.T) {
	s := strings.Repeat("This sentence is here to fill space. ", 20) // ~740 chars
	parts := XParts(s)
	if len(parts) < 3 {
		t.Fatalf("expected a thread, got %d parts", len(parts))
	}
	for i, p := range parts {
		if XLength(p) > XLimit {
			t.Errorf("part %d is %d long", i, XLength(p))
		}
		if !strings.HasSuffix(p, "/"+itoa(len(parts))) {
			t.Errorf("part %d isn't numbered: %q", i, p)
		}
		if i < len(parts)-1 && !strings.Contains(p, "space. ") && !strings.HasSuffix(strings.Fields(p)[len(strings.Fields(p))-2], ".") {
			t.Errorf("part %d doesn't end on a sentence: %q", i, p)
		}
	}
}

func TestXIntent(t *testing.T) {
	got := XIntent("hi & bye #go", "")
	if got != "https://x.com/intent/post?text=hi+%26+bye+%23go" {
		t.Fatal(got)
	}
	if got := XIntent("2/2", "123"); !strings.Contains(got, "in_reply_to=123") {
		t.Fatal(got)
	}
}

func TestXStatusID(t *testing.T) {
	if XStatusID("https://x.com/yash/status/1850000000000000001?s=20") != "1850000000000000001" {
		t.Fatal("x.com link")
	}
	if XStatusID("https://twitter.com/yash/status/42") != "42" {
		t.Fatal("twitter.com link")
	}
	if XStatusID("https://example.com") != "" {
		t.Fatal("not a status")
	}
}

func TestLinkedInText(t *testing.T) {
	for in, want := range map[string]string{
		"plain words":             "plain words",
		"Shipping #golang today":  `Shipping {hashtag|\#|golang} today`,
		"a (b) [c] <d> *e* _f_ ~": `a \(b\) \[c\] \<d\> \*e\* \_f\_ \~`,
		"mail me@x.com":           `mail me\@x.com`,
		"C# isn't a tag":          `C\# isn't a tag`,
		"#first word":             `{hashtag|\#|first} word`,
	} {
		if got := LinkedInText(in); got != want {
			t.Errorf("LinkedInText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkedInBodyDropsThreadBreaks(t *testing.T) {
	if got := LinkedInBody("one\n---\ntwo"); got != "one\n\ntwo" {
		t.Fatalf("%q", got)
	}
	if LinkedInLength("one\n---\ntwo") != 8 {
		t.Fatal(LinkedInLength("one\n---\ntwo"))
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{"LinkedIn", "twitter", "x", "mastodon"})
	if strings.Join(got, ",") != "x,linkedin" {
		t.Fatal(got)
	}
}

func TestCheckAndSettled(t *testing.T) {
	p := Post{Text: "hi", Networks: []string{X, LinkedIn}}
	if Check(p) != "" {
		t.Fatal(Check(p))
	}
	if Check(Post{Text: " ", Networks: []string{X}}) == "" {
		t.Fatal("empty post passed")
	}
	if Check(Post{Text: strings.Repeat("a", 3001), Networks: []string{LinkedIn}}) == "" {
		t.Fatal("too long for LinkedIn passed")
	}
	p.Set(X, Result{State: Done})
	if done, _ := p.Settled(); done {
		t.Fatal("settled with LinkedIn pending")
	}
	p.Set(LinkedIn, Result{State: Error})
	if done, failed := p.Settled(); !done || !failed {
		t.Fatal("should be settled and failed")
	}
}
