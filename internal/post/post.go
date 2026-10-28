// Package post is what prasar posts: one text for X and LinkedIn, its
// images, when it goes out, and what happened on each network. It also
// knows each network's rules: X's 280 weighted characters (and how to
// split a longer text into a thread), LinkedIn's 3000 characters and the
// escaping its "little text" format needs.
package post

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Networks.
const (
	X        = "x"
	LinkedIn = "linkedin"
)

// Networks lists them in the order they're shown.
var Networks = []string{X, LinkedIn}

// Name is a network's name for people.
func Name(n string) string {
	switch n {
	case X:
		return "X"
	case LinkedIn:
		return "LinkedIn"
	}
	return n
}

// A post's status.
const (
	Draft     = "draft"     // saved, not sent anywhere
	Approval  = "approval"  // an agent wrote it: waits for your yes
	Scheduled = "scheduled" // goes out at SendAt
	Ready     = "ready"     // due, and X waits for your click in the browser
	Posted    = "posted"    // done everywhere it was meant to go
	Failed    = "failed"    // a network refused it (see Results)
	Cancelled = "cancelled" // you took it back
	Declined  = "declined"  // you said no to an agent's post
)

// A network's state for one post.
const (
	Pending   = "pending" // not tried yet
	Opened    = "opened"  // X: the share page was opened, waiting for you to press Post
	Done      = "posted"  // on the network
	Error     = "failed"  // the network refused it
	Abandoned = "skipped" // you chose not to post it there after all
)

// Origins.
const (
	You   = "you"
	Agent = "agent"
)

// Result is what happened on one network.
type Result struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
	At    int64  `json:"at,omitempty"`
	// X: the thread part to open next (0-based), and the posted parts' links
	Part  int      `json:"part,omitempty"`
	Links []string `json:"links,omitempty"`
}

// Post is one post, for one or more networks.
type Post struct {
	ID        int64             `json:"id"`
	Text      string            `json:"text"`
	Networks  []string          `json:"networks"`
	Images    []string          `json:"images,omitempty"` // absolute paths
	Status    string            `json:"status"`
	SendAt    int64             `json:"send_at,omitempty"` // unix ms; 0 = now
	Origin    string            `json:"origin"`
	Requester string            `json:"requester,omitempty"` // which agent asked
	Note      string            `json:"note,omitempty"`      // the agent's reason, shown when you approve
	Results   map[string]Result `json:"results,omitempty"`
	Created   int64             `json:"created"`
	Updated   int64             `json:"updated"`
}

// On says whether the post goes to network n.
func (p Post) On(n string) bool {
	for _, x := range p.Networks {
		if x == n {
			return true
		}
	}
	return false
}

// State is network n's state ("pending" when nothing happened yet).
func (p Post) State(n string) string {
	if r, ok := p.Results[n]; ok && r.State != "" {
		return r.State
	}
	return Pending
}

// Set records network n's result.
func (p *Post) Set(n string, r Result) {
	if p.Results == nil {
		p.Results = map[string]Result{}
	}
	p.Results[n] = r
}

// Settled says whether every network is finished with (posted, skipped
// or failed), and whether any failed.
func (p Post) Settled() (done, failed bool) {
	done = true
	for _, n := range p.Networks {
		switch p.State(n) {
		case Done, Abandoned:
		case Error:
			failed = true
		default:
			done = false
		}
	}
	return done, failed
}

// Open says whether the post can still change (not posted, cancelled,
// declined).
func (p Post) Open() bool {
	switch p.Status {
	case Posted, Cancelled, Declined:
		return false
	}
	return true
}

// Normalize keeps known networks, once each, in the usual order.
func Normalize(networks []string) []string {
	var out []string
	for _, n := range Networks {
		for _, x := range networks {
			if strings.EqualFold(strings.TrimSpace(x), n) || (n == X && strings.EqualFold(strings.TrimSpace(x), "twitter")) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- X

// XLimit is X's length limit, in weighted characters.
const XLimit = 280

// xURL is how X counts a link: always 23, however long it is.
const xURL = 23

var reURL = regexp.MustCompile(`https?://[^\s]+|\b[a-zA-Z0-9-]+\.(?:com|org|net|io|dev|in|co|ai|app|me|xyz|gg|tv)(?:/[^\s]*)?`)

// XLength is how long X counts a text: most characters 1, CJK and emoji
// 2, every link 23 (twitter-text's v3 weighting).
func XLength(s string) int {
	s = strings.TrimSpace(s)
	n := 0
	last := 0
	for _, m := range reURL.FindAllStringIndex(s, -1) {
		n += weighted(s[last:m[0]]) + xURL
		last = m[1]
	}
	return n + weighted(s[last:])
}

func weighted(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r <= 4351, r >= 8192 && r <= 8205, r >= 8208 && r <= 8223, r >= 8242 && r <= 8247:
			n++
		case unicode.Is(unicode.Mn, r) || r == 0xFE0F || r == 0x200D:
			// combining marks, the emoji variation selector and zero-width
			// joiners ride on the character before them
		default:
			n += 2
		}
	}
	return n
}

// ThreadBreak is a line that starts a new post in an X thread.
const ThreadBreak = "---"

// XParts is the text as X posts: the parts you split with a "---" line,
// and any part still over 280 split at sentence or word ends, numbered
// "1/3" when there's more than one.
func XParts(text string) []string {
	var chunks []string
	for _, c := range splitBreaks(text) {
		if XLength(c) <= XLimit {
			chunks = append(chunks, c)
			continue
		}
		chunks = append(chunks, splitLong(c, XLimit-6)...) // room for " 10/12"
	}
	if len(chunks) <= 1 {
		return chunks
	}
	n := len(chunks)
	for i := range chunks {
		chunks[i] = chunks[i] + " " + itoa(i+1) + "/" + itoa(n)
	}
	return chunks
}

func splitBreaks(text string) []string {
	var out []string
	var cur []string
	flush := func() {
		if c := strings.TrimSpace(strings.Join(cur, "\n")); c != "" {
			out = append(out, c)
		}
		cur = nil
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == ThreadBreak {
			flush()
			continue
		}
		cur = append(cur, l)
	}
	flush()
	return out
}

// splitLong cuts text into pieces of at most limit, preferring the end of
// a paragraph, then a sentence, then a word.
func splitLong(text string, limit int) []string {
	var out []string
	for XLength(text) > limit {
		cut := bestCut(text, limit)
		out = append(out, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

func bestCut(text string, limit int) int {
	fits := 0 // the longest prefix (in bytes) within the limit
	for i := range text {
		if i > 0 && XLength(text[:i]) > limit {
			break
		}
		fits = i
	}
	if XLength(text) <= limit {
		fits = len(text)
	}
	head := text[:fits]
	for _, sep := range []string{"\n\n", ". ", "! ", "? ", "\n", "; ", ", ", " "} {
		if i := strings.LastIndex(head, sep); i > fits/3 {
			return i + len(sep)
		}
	}
	if fits == 0 {
		return len(text)
	}
	return fits
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// XIntent is X's share page with the text filled in: the free way to
// post, one click on Post. replyTo is a post id to answer (a thread's
// next part).
func XIntent(text, replyTo string) string {
	q := url.Values{"text": {text}}
	if replyTo != "" {
		q.Set("in_reply_to", replyTo)
	}
	return "https://x.com/intent/post?" + q.Encode()
}

var reStatus = regexp.MustCompile(`(?:x|twitter)\.com/[^/\s]+/status(?:es)?/(\d+)`)

// XStatusID is the post id in an X link ("" when it isn't one).
func XStatusID(link string) string {
	if m := reStatus.FindStringSubmatch(link); m != nil {
		return m[1]
	}
	return ""
}

// ---------------------------------------------------------------- LinkedIn

// LinkedInLimit is the most characters a LinkedIn post may have.
const LinkedInLimit = 3000

// LinkedInFold is about where LinkedIn's feed cuts a post with "…see more".
const LinkedInFold = 210

// LinkedInBody is the text as LinkedIn gets it: one post, so the "---"
// lines that split an X thread become paragraph breaks.
func LinkedInBody(s string) string {
	return strings.Join(splitBreaks(s), "\n\n")
}

// LinkedInLength counts characters the way LinkedIn does (runes).
func LinkedInLength(s string) int { return len([]rune(LinkedInBody(s))) }

var reHashtag = regexp.MustCompile(`(^|[\s(])#([\p{L}\p{N}_]+)`)

// LinkedInText turns plain text into LinkedIn's "little text" format:
// its reserved characters escaped, and #words made into real hashtags.
func LinkedInText(s string) string {
	s = LinkedInBody(s)
	type tag struct{ start, end int }
	var tags []tag
	for _, m := range reHashtag.FindAllStringSubmatchIndex(s, -1) {
		tags = append(tags, tag{m[4] - 1, m[5]}) // from the # to the word's end
	}
	var b strings.Builder
	last := 0
	for _, t := range tags {
		b.WriteString(escapeLittle(s[last:t.start]))
		b.WriteString(`{hashtag|\#|` + escapeLittle(s[t.start+1:t.end]) + `}`)
		last = t.end
	}
	b.WriteString(escapeLittle(s[last:]))
	return b.String()
}

func escapeLittle(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\|{}@[]()<>#*_~`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Check says what's wrong with a post for its networks ("" when nothing).
func Check(p Post) string {
	if strings.TrimSpace(p.Text) == "" {
		return "the post is empty"
	}
	if len(p.Networks) == 0 {
		return "pick where to post it: X, LinkedIn or both"
	}
	if p.On(LinkedIn) && LinkedInLength(p.Text) > LinkedInLimit {
		return "too long for LinkedIn (" + itoa(LinkedInLength(p.Text)) + "/3000)"
	}
	if p.On(LinkedIn) && len(p.Images) > 20 {
		return "LinkedIn takes at most 20 images"
	}
	if p.On(X) && len(p.Images) > 4 {
		return "X takes at most 4 images"
	}
	return ""
}
