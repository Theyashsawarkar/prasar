// Package linkedin posts to your LinkedIn profile through LinkedIn's free,
// self-serve API: an app of your own with the "Share on LinkedIn" and
// "Sign In with LinkedIn using OpenID Connect" products, signed in once in
// the browser. The token LinkedIn gives such apps lasts 60 days and can't
// be refreshed: prasar says when it's running out, and `prasar auth
// linkedin` signs in again.
package linkedin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scopes are what prasar asks for: who you are (to post as you) and
// posting.
const Scopes = "openid profile w_member_social"

// DefaultVersion is the LinkedIn API version prasar speaks (YYYYMM).
// LinkedIn retires a version after about a year: `linkedin.version` in
// config.toml picks another without a new prasar.
const DefaultVersion = "202607"

// DefaultPort is the local port the browser comes back to after you sign
// in: the app's redirect URL must be http://localhost:<port>/callback.
const DefaultPort = 8723

// Bases are where LinkedIn lives; PRASAR_LINKEDIN_URL points both at a
// fake one (tests, `prasar demo`).
func Bases() (oauth, api string) {
	if u := strings.TrimRight(os.Getenv("PRASAR_LINKEDIN_URL"), "/"); u != "" {
		return u, u
	}
	return "https://www.linkedin.com", "https://api.linkedin.com"
}

// App is your LinkedIn developer app.
type App struct {
	ClientID, ClientSecret string
	Port                   int
	Version                string
}

// Redirect is the URL the app must list under "Authorized redirect URLs".
func (a App) Redirect() string {
	port := a.Port
	if port == 0 {
		port = DefaultPort
	}
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

// Token is a signed-in session.
type Token struct {
	Access  string `json:"access_token"`
	Expires int64  `json:"expires"` // unix seconds
	Sub     string `json:"sub"`     // the member id posts are written as
	Name    string `json:"name"`
	Picture string `json:"picture,omitempty"`
}

// Left is how long the token still works.
func (t Token) Left(now time.Time) time.Duration {
	return time.Unix(t.Expires, 0).Sub(now)
}

// ErrNotSignedIn means no LinkedIn sign-in yet, or it ran out.
var ErrNotSignedIn = errors.New("LinkedIn isn't signed in: run `prasar auth linkedin`")

// SignIn opens LinkedIn's consent page in the browser and waits for it to
// come back to the local redirect, then trades the code for a token and
// reads who you are. say() gets the URL in case no browser opens.
func SignIn(ctx context.Context, a App, say func(string)) (Token, error) {
	if a.ClientID == "" || a.ClientSecret == "" {
		return Token{}, errors.New("no LinkedIn app yet: set linkedin.client_id and client_secret (prasar auth linkedin --setup)")
	}
	redirect := a.Redirect()
	u, _ := url.Parse(redirect)
	ln, err := net.Listen("tcp", "127.0.0.1:"+u.Port())
	if err != nil {
		return Token{}, fmt.Errorf("can't listen on port %s for LinkedIn's answer: %w", u.Port(), err)
	}
	defer ln.Close()
	oauthBase, _ := Bases()
	state := randString()
	auth := oauthBase + "/oauth/v2/authorization?" + url.Values{
		"response_type": {"code"}, "client_id": {a.ClientID}, "redirect_uri": {redirect},
		"state": {state}, "scope": {Scopes},
	}.Encode()

	type result struct {
		code string
		err  error
	}
	got := make(chan result, 1)
	var once sync.Once
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		var res result
		switch {
		case q.Get("error") != "":
			res.err = fmt.Errorf("LinkedIn said: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("state") != state:
			res.err = errors.New("the sign-in answer didn't match this request; try again")
		default:
			res.code = q.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		msg := "prasar is signed in to LinkedIn. You can close this tab."
		if res.err != nil {
			msg = "prasar: " + res.err.Error()
		}
		fmt.Fprintf(w, "<p style='font:16px sans-serif;margin:3em'>%s</p>", html.EscapeString(msg))
		once.Do(func() { got <- res })
	})}
	go srv.Serve(ln)
	defer srv.Close()

	say(auth)
	_ = Open(auth)

	var res result
	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case res = <-got:
	}
	if res.err != nil {
		return Token{}, res.err
	}
	return exchange(ctx, a, res.code)
}

func exchange(ctx context.Context, a App, code string) (Token, error) {
	oauthBase, _ := Bases()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a.Redirect()},
		"client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, _ := http.NewRequestWithContext(ctx, "POST", oauthBase+"/oauth/v2/accessToken", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := do(req, &out, nil); err != nil {
		return Token{}, fmt.Errorf("signing in: %w", err)
	}
	t := Token{Access: out.AccessToken, Expires: time.Now().Unix() + out.ExpiresIn}
	me, err := (&Client{Token: t, Version: a.Version}).Me(ctx)
	if err != nil {
		return Token{}, err
	}
	t.Sub, t.Name, t.Picture = me.Sub, me.Name, me.Picture
	return t, nil
}

// Open shows a URL in the browser (the tests and the demo replace it).
var Open = func(u string) error { return exec.Command("xdg-open", u).Start() }

func randString() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ---------------------------------------------------------------- the API

// Client talks to LinkedIn as the signed-in member.
type Client struct {
	Token   Token
	Version string
}

func (c *Client) version() string {
	if c.Version != "" {
		return c.Version
	}
	return DefaultVersion
}

func (c *Client) request(ctx context.Context, method, path string, body any) *http.Request {
	_, api := Bases()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, api+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.Token.Access)
	req.Header.Set("LinkedIn-Version", c.version())
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// Me is the signed-in member.
type Me struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// Me reads who's signed in.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	req := c.request(ctx, "GET", "/v2/userinfo", nil)
	err := do(req, &me, nil)
	if err == nil && me.Sub == "" {
		err = errors.New("LinkedIn didn't say who you are (is \"Sign In with LinkedIn using OpenID Connect\" added to the app?)")
	}
	return me, err
}

func (c *Client) author() string { return "urn:li:person:" + c.Token.Sub }

// Post publishes text (plain: escaped here) with images on your profile
// and returns the post's link.
func (c *Client) Post(ctx context.Context, commentary string, images []string) (string, error) {
	if c.Token.Access == "" || c.Token.Sub == "" {
		return "", ErrNotSignedIn
	}
	if c.Token.Left(time.Now()) <= 0 {
		return "", errors.New("the LinkedIn sign-in ran out (they last 60 days): run `prasar auth linkedin`")
	}
	body := map[string]any{
		"author":     c.author(),
		"commentary": commentary,
		"visibility": "PUBLIC",
		"distribution": map[string]any{
			"feedDistribution": "MAIN_FEED", "targetEntities": []any{}, "thirdPartyDistributionChannels": []any{},
		},
		"lifecycleState":            "PUBLISHED",
		"isReshareDisabledByAuthor": false,
	}
	var ids []string
	for _, path := range images {
		id, err := c.upload(ctx, path)
		if err != nil {
			return "", fmt.Errorf("image %s: %w", filepath.Base(path), err)
		}
		ids = append(ids, id)
	}
	switch len(ids) {
	case 0:
	case 1:
		body["content"] = map[string]any{"media": map[string]any{"id": ids[0]}}
	default:
		var imgs []any
		for _, id := range ids {
			imgs = append(imgs, map[string]any{"id": id})
		}
		body["content"] = map[string]any{"multiImage": map[string]any{"images": imgs}}
	}
	var hdr http.Header
	if err := do(c.request(ctx, "POST", "/rest/posts", body), nil, &hdr); err != nil {
		return "", err
	}
	urn := hdr.Get("x-restli-id")
	if urn == "" {
		urn = hdr.Get("X-LinkedIn-Id")
	}
	if urn == "" {
		return "https://www.linkedin.com/in/me/recent-activity/all/", nil
	}
	return "https://www.linkedin.com/feed/update/" + urn + "/", nil
}

// upload sends one image and returns its urn.
func (c *Client) upload(ctx context.Context, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var init struct {
		Value struct {
			UploadURL string `json:"uploadUrl"`
			Image     string `json:"image"`
		} `json:"value"`
	}
	req := c.request(ctx, "POST", "/rest/images?action=initializeUpload",
		map[string]any{"initializeUploadRequest": map[string]any{"owner": c.author()}})
	if err := do(req, &init, nil); err != nil {
		return "", err
	}
	if init.Value.UploadURL == "" {
		return "", errors.New("LinkedIn gave no upload address")
	}
	put, _ := http.NewRequestWithContext(ctx, "PUT", init.Value.UploadURL, bytes.NewReader(data))
	put.Header.Set("Authorization", "Bearer "+c.Token.Access)
	put.Header.Set("Content-Type", "application/octet-stream")
	if err := do(put, nil, nil); err != nil {
		return "", err
	}
	return init.Value.Image, nil
}

// do sends a request, decodes a JSON answer into out (when given), keeps
// the response headers in hdr (when given), and turns LinkedIn's errors
// into a sentence.
func do(req *http.Request, out any, hdr *http.Header) error {
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("can't reach LinkedIn: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if hdr != nil {
		*hdr = resp.Header
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Desc    string `json:"error_description"`
		}
		_ = json.Unmarshal(b, &e)
		msg := strings.TrimSpace(e.Message + " " + e.Desc)
		if msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		switch resp.StatusCode {
		case 401:
			return fmt.Errorf("LinkedIn refused the sign-in (%s): run `prasar auth linkedin`", msg)
		case 403:
			return fmt.Errorf("LinkedIn says the app may not do this (%s): does it have the \"Share on LinkedIn\" product?", msg)
		case 426:
			return fmt.Errorf("LinkedIn retired this API version (%s): set linkedin.version in config.toml to a recent YYYYMM", msg)
		case 429:
			return errors.New("LinkedIn's daily limit is reached (about 150 posts a day): try again tomorrow")
		}
		return fmt.Errorf("LinkedIn: %d %s", resp.StatusCode, msg)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("LinkedIn's answer: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- tokens

// Tokens keeps the session in the Secret Service keyring through
// secret-tool, or, without a keyring, in a 0600 file.
type Tokens struct{ Dir string }

func keyring() bool {
	_, err := exec.LookPath("secret-tool")
	return err == nil && os.Getenv("PRASAR_NO_KEYRING") == ""
}

func (ts Tokens) file() string { return filepath.Join(ts.Dir, "linkedin.token") }

// Save keeps a token.
func (ts Tokens) Save(t Token) error {
	b, _ := json.Marshal(t)
	if keyring() {
		cmd := exec.Command("secret-tool", "store", "--label=prasar: LinkedIn", "service", "prasar", "account", "linkedin")
		cmd.Stdin = bytes.NewReader(b)
		if err := cmd.Run(); err == nil {
			_ = os.Remove(ts.file())
			return nil
		}
	}
	if err := os.MkdirAll(ts.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(ts.file(), b, 0o600)
}

// Load reads the token (ErrNotSignedIn when there's none).
func (ts Tokens) Load() (Token, error) {
	var b []byte
	if keyring() {
		if out, err := exec.Command("secret-tool", "lookup", "service", "prasar", "account", "linkedin").Output(); err == nil && len(out) > 0 {
			b = out
		}
	}
	if b == nil {
		f, err := os.ReadFile(ts.file())
		if err != nil {
			return Token{}, ErrNotSignedIn
		}
		b = f
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil || t.Access == "" {
		return Token{}, ErrNotSignedIn
	}
	return t, nil
}

// Delete signs out.
func (ts Tokens) Delete() {
	if keyring() {
		_ = exec.Command("secret-tool", "clear", "service", "prasar", "account", "linkedin").Run()
	}
	_ = os.Remove(ts.file())
}
