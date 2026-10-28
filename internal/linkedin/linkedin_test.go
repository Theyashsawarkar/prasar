package linkedin

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Theyashsawarkar/prasar/internal/fake"
)

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// The browser follows LinkedIn's redirect back to prasar's callback.
func browser(t *testing.T) {
	Open = func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { Open = func(string) error { return nil } })
}

func setup(t *testing.T) (*fake.LinkedIn, App) {
	f := fake.New()
	t.Cleanup(f.Close)
	t.Setenv("PRASAR_LINKEDIN_URL", f.URL)
	browser(t)
	return f, App{ClientID: "id", ClientSecret: "secret", Port: freePort(t)}
}

func TestSignInAndPost(t *testing.T) {
	f, app := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var said string
	tok, err := SignIn(ctx, app, func(u string) { said = u })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "scope=openid+profile+w_member_social") || !strings.Contains(said, "redirect_uri=http%3A%2F%2Flocalhost") {
		t.Fatal("consent URL:", said)
	}
	if tok.Sub != "fake-member" || tok.Name != "Asha Demo" || tok.Left(time.Now()) < 59*24*time.Hour {
		t.Fatal(tok)
	}

	img := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(img, []byte("not really a png"), 0o644)
	c := &Client{Token: tok}
	link, err := c.Post(ctx, `Hello \(world\)`, []string{img, img})
	if err != nil {
		t.Fatal(err)
	}
	posts := f.Posts()
	if len(posts) != 1 || posts[0].Author != "urn:li:person:fake-member" || posts[0].Commentary != `Hello \(world\)` {
		t.Fatal(posts)
	}
	if len(posts[0].Images) != 2 || f.Uploaded(posts[0].Images[0]) != 16 {
		t.Fatal("images:", posts[0].Images)
	}
	if posts[0].Version != DefaultVersion {
		t.Fatal("version header", posts[0].Version)
	}
	if link != "https://www.linkedin.com/feed/update/"+posts[0].URN+"/" {
		t.Fatal(link)
	}
}

func TestErrorsSaySomethingUseful(t *testing.T) {
	f, _ := setup(t)
	ctx := context.Background()
	c := &Client{Token: Token{Access: "fake-token", Sub: "fake-member", Expires: time.Now().Add(time.Hour).Unix()}}
	f.FailPosts = 426
	if _, err := c.Post(ctx, "x", nil); err == nil || !strings.Contains(err.Error(), "linkedin.version") {
		t.Fatal(err)
	}
	f.FailPosts = 0
	bad := &Client{Token: Token{Access: "wrong", Sub: "fake-member", Expires: time.Now().Add(time.Hour).Unix()}}
	if _, err := bad.Post(ctx, "x", nil); err == nil || !strings.Contains(err.Error(), "prasar auth linkedin") {
		t.Fatal(err)
	}
	expired := &Client{Token: Token{Access: "fake-token", Sub: "fake-member", Expires: time.Now().Add(-time.Hour).Unix()}}
	if _, err := expired.Post(ctx, "x", nil); err == nil || !strings.Contains(err.Error(), "60 days") {
		t.Fatal(err)
	}
	if _, err := (&Client{}).Post(ctx, "x", nil); err != ErrNotSignedIn {
		t.Fatal(err)
	}
}

func TestTokensInAFile(t *testing.T) {
	t.Setenv("PRASAR_NO_KEYRING", "1")
	ts := Tokens{Dir: t.TempDir()}
	if _, err := ts.Load(); err != ErrNotSignedIn {
		t.Fatal(err)
	}
	ts.Save(Token{Access: "a", Sub: "s", Name: "n", Expires: 5})
	got, err := ts.Load()
	if err != nil || got.Name != "n" {
		t.Fatal(got, err)
	}
	st, _ := os.Stat(ts.file())
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	ts.Delete()
	if _, err := ts.Load(); err != ErrNotSignedIn {
		t.Fatal(err)
	}
}
