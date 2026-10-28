package tui

import (
	"fmt"
	"strings"
)

// Key is one key of the screen: what you press, what it does, and where
// (empty: everywhere).
type Key struct {
	Keys, Desc, Where string
}

// Keys is every key, in the order the ? sheet lists them. The README's key
// tables and scripts/keybindings (Vayu's keybinding search) come from it:
// after a change run `go test ./internal/tui -update`.
var Keys = []Key{
	{"Ctrl+S", "Post it now (LinkedIn goes out, X opens in your browser; asks first)", "write"},
	{"Ctrl+T", "Schedule it (tomorrow 9am, fri 6pm, in 2h...)", "write"},
	{"Ctrl+D", "Save it as a draft", "write"},
	{"Ctrl+X", "Toggle posting to X", "write"},
	{"Ctrl+L", "Toggle posting to LinkedIn", "write"},
	{"Ctrl+O", "Add an image (a path; Ctrl+O on an empty line removes them)", "write"},
	{"Ctrl+N", "Start a new post (the current one stays as it was saved)", "write"},
	{"Esc", "Leave the editor (the other keys work then)", "write"},
	{"i / Enter", "Edit the post", "compose"},
	{"Tab / Shift+Tab", "Switch to the next / previous tab", ""},
	{"1 – 4", "Switch to Compose, Queue, Drafts or Posted", ""},
	{"L", "Open LinkedIn's sign-in again (it lasts 60 days)", ""},
	{"?", "Show every key", ""},
	{"q", "Quit", ""},
	{"j / k", "Move to the next / previous post", "lists"},
	{"Enter / e", "Edit the post in Compose", "lists"},
	{"p", "Post it now (asks first)", "lists"},
	{"t", "Schedule it, or move it to another time", "lists"},
	{"o", "Open it on X's page to press Post (and confirm it after)", "lists"},
	{"a", "Approve an agent's post", "lists"},
	{"n", "Decline an agent's post", "lists"},
	{"r", "Send a post LinkedIn refused again", "lists"},
	{"c", "Cancel a scheduled or waiting post", "lists"},
	{"d", "Delete the draft (asks first)", "lists"},
	{"y", "Copy the post's link", "lists"},
	{"v", "Open the post's link in the browser", "lists"},
	{"Enter", "Confirm it's posted on X (paste its link first for a thread's next part)", "x"},
	{"Ctrl+O", "Open X's page again", "x"},
	{"Ctrl+K", "Cancel X for this post (LinkedIn is unaffected)", "x"},
	{"Esc", "Leave it for later (it waits in Queue)", "x"},
}

// KeybindingsFile is scripts/keybindings, for Vayu's keybinding search.
func KeybindingsFile() string {
	var b strings.Builder
	b.WriteString("# Keys inside the prasar screen, for Vayu's keybinding search\n")
	b.WriteString("# (keybind-search.py reads ~/.tmux/plugins/*/scripts). Generated from\n")
	b.WriteString("# internal/tui/keys.go by `go test ./internal/tui -update`; don't edit.\n")
	// marker is kept apart from the rest of the line: the binary holds this
	// format string, and the keybinding search scans plugins' bin/ folders.
	marker := strings.Join([]string{"#", "keybind"}, " ")
	for _, k := range Keys {
		where := map[string]string{"write": " (while writing)", "compose": " (Compose tab)", "lists": " (Queue, Drafts, Posted)", "x": " (X popup)"}[k.Where]
		fmt.Fprintf(&b, "%s: Tmux/prasar | %s | %s%s\n", marker, k.Keys, k.Desc, where)
	}
	return b.String()
}
