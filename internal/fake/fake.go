// Package fake is an in-memory LinkedIn for the tests and `prasar demo`:
// sign-in (it answers at once, no browser), who you are, image uploads
// and posts, at the paths the real one uses. Point prasar at it with
// PRASAR_LINKEDIN_URL.
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
)

// Post is one post LinkedIn received.
type Post struct {
	URN        string
	Author     string
	Commentary string
	Images     []string // image urns
	Version    string   // the LinkedIn-Version header
}

// LinkedIn is the fake.
type LinkedIn struct {
	*httptest.Server
	mu       sync.Mutex
	posts    []Post
	uploaded map[string]int // image urn -> bytes received
	n        int
	// FailPosts makes every post fail with this status (0: they work).
	FailPosts int
}

// New starts a fake LinkedIn.
func New() *LinkedIn {
	l := &LinkedIn{uploaded: map[string]int{}}
	l.Server = httptest.NewServer(http.HandlerFunc(l.serve))
	return l
}

// Posts is what was posted so far.
func (l *LinkedIn) Posts() []Post {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Post(nil), l.posts...)
}

// Uploaded is how many bytes arrived for an image urn.
func (l *LinkedIn) Uploaded(urn string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.uploaded[urn]
}

func (l *LinkedIn) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	authed := r.Header.Get("Authorization") == "Bearer fake-token"
	switch {
	case r.URL.Path == "/oauth/v2/authorization":
		q := r.URL.Query()
		back, _ := url.Parse(q.Get("redirect_uri"))
		back.RawQuery = url.Values{"code": {"fake-code"}, "state": {q.Get("state")}}.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	case r.URL.Path == "/oauth/v2/accessToken" && r.Method == "POST":
		_ = r.ParseForm()
		if r.Form.Get("code") != "fake-code" || r.Form.Get("client_secret") == "" {
			http.Error(w, `{"error_description":"bad code or client"}`, 400)
			return
		}
		writeJSON(w, map[string]any{"access_token": "fake-token", "expires_in": 5184000})
	case r.URL.Path == "/v2/userinfo":
		if !authed {
			http.Error(w, `{"message":"bad token"}`, 401)
			return
		}
		writeJSON(w, map[string]any{"sub": "fake-member", "name": "Asha Demo", "picture": ""})
	case r.URL.Path == "/rest/images" && r.URL.Query().Get("action") == "initializeUpload":
		if !authed {
			http.Error(w, `{"message":"bad token"}`, 401)
			return
		}
		l.n++
		urn := fmt.Sprintf("urn:li:image:fake%d", l.n)
		writeJSON(w, map[string]any{"value": map[string]any{
			"uploadUrl": l.URL + "/upload/" + strings.TrimPrefix(urn, "urn:li:image:"), "image": urn}})
	case strings.HasPrefix(r.URL.Path, "/upload/") && r.Method == "PUT":
		b, _ := io.ReadAll(r.Body)
		l.uploaded["urn:li:image:"+strings.TrimPrefix(r.URL.Path, "/upload/")] = len(b)
		w.WriteHeader(201)
	case r.URL.Path == "/rest/posts" && r.Method == "POST":
		if !authed {
			http.Error(w, `{"message":"bad token"}`, 401)
			return
		}
		if l.FailPosts != 0 {
			http.Error(w, `{"message":"the fake was told to fail"}`, l.FailPosts)
			return
		}
		var body struct {
			Author     string `json:"author"`
			Commentary string `json:"commentary"`
			Content    struct {
				Media struct {
					ID string `json:"id"`
				} `json:"media"`
				MultiImage struct {
					Images []struct {
						ID string `json:"id"`
					} `json:"images"`
				} `json:"multiImage"`
			} `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Author == "" {
			http.Error(w, `{"message":"bad post"}`, 422)
			return
		}
		l.n++
		p := Post{URN: fmt.Sprintf("urn:li:share:%d", 7000+l.n), Author: body.Author, Commentary: body.Commentary,
			Version: r.Header.Get("LinkedIn-Version")}
		if body.Content.Media.ID != "" {
			p.Images = append(p.Images, body.Content.Media.ID)
		}
		for _, im := range body.Content.MultiImage.Images {
			p.Images = append(p.Images, im.ID)
		}
		l.posts = append(l.posts, p)
		w.Header().Set("x-restli-id", p.URN)
		w.WriteHeader(201)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
