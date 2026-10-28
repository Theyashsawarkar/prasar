// Package store keeps prasar's posts in one SQLite file, shared by the
// screen, the background service and the MCP server: each post is a JSON
// row, with its status and send time beside it to query by. A status
// changes only through Transition, which checks the old one, so two
// processes can't both publish the same post.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Theyashsawarkar/prasar/internal/post"
)

// ErrNotFound is a post id that doesn't exist.
var ErrNotFound = errors.New("no such post")

// Store is the open database.
type Store struct{ db *sql.DB }

// Open opens (and creates) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		status TEXT NOT NULL,
		send_at INTEGER NOT NULL DEFAULT 0,
		updated INTEGER NOT NULL,
		data TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("database %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().UnixMilli() }

// Add stores a new post and returns it with its id.
func (s *Store) Add(p post.Post) (post.Post, error) {
	t := now()
	if p.Created == 0 {
		p.Created = t
	}
	p.Updated = t
	b, _ := json.Marshal(p)
	r, err := s.db.Exec(`INSERT INTO posts (status, send_at, updated, data) VALUES (?, ?, ?, ?)`, p.Status, p.SendAt, t, string(b))
	if err != nil {
		return p, err
	}
	p.ID, _ = r.LastInsertId()
	return p, s.write(p)
}

// Get reads one post.
func (s *Store) Get(id int64) (post.Post, error) {
	var data string
	err := s.db.QueryRow(`SELECT data FROM posts WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return post.Post{}, fmt.Errorf("%w: #%d", ErrNotFound, id)
	}
	if err != nil {
		return post.Post{}, err
	}
	var p post.Post
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return p, err
	}
	p.ID = id
	return p, nil
}

func (s *Store) write(p post.Post) error {
	b, _ := json.Marshal(p)
	_, err := s.db.Exec(`UPDATE posts SET status = ?, send_at = ?, updated = ?, data = ? WHERE id = ?`,
		p.Status, p.SendAt, p.Updated, string(b), p.ID)
	return err
}

// Save writes a post back as it is, whatever its status was.
func (s *Store) Save(p post.Post) error {
	p.Updated = now()
	return s.write(p)
}

// Transition writes p only if the stored post still has one of the from
// statuses; false means another process got there first.
func (s *Store) Transition(p post.Post, from ...string) (bool, error) {
	p.Updated = now()
	b, _ := json.Marshal(p)
	q := `UPDATE posts SET status = ?, send_at = ?, updated = ?, data = ? WHERE id = ? AND status IN (?` +
		strings.Repeat(", ?", len(from)-1) + `)`
	args := []any{p.Status, p.SendAt, p.Updated, string(b), p.ID}
	for _, f := range from {
		args = append(args, f)
	}
	r, err := s.db.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}

// List is the posts with any of these statuses (all when none), newest
// first; due posts sort by send time.
func (s *Store) List(statuses ...string) ([]post.Post, error) {
	q := `SELECT id, data FROM posts`
	var args []any
	if len(statuses) > 0 {
		q += ` WHERE status IN (?` + strings.Repeat(", ?", len(statuses)-1) + `)`
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	q += ` ORDER BY CASE WHEN send_at > 0 THEN send_at ELSE updated END DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []post.Post
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var p post.Post
		if json.Unmarshal([]byte(data), &p) == nil {
			p.ID = id
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// Due is the scheduled posts whose time has come, oldest first.
func (s *Store) Due(at time.Time) ([]post.Post, error) {
	rows, err := s.db.Query(`SELECT id, data FROM posts WHERE status = ? AND send_at <= ? ORDER BY send_at, id`,
		post.Scheduled, at.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []post.Post
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var p post.Post
		if json.Unmarshal([]byte(data), &p) == nil {
			p.ID = id
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// Delete removes a post for good (drafts).
func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM posts WHERE id = ?`, id)
	return err
}

// Stamp changes whenever any post does: the screen reloads when it moves.
func (s *Store) Stamp() string {
	var n, upd sql.NullInt64
	_ = s.db.QueryRow(`SELECT COUNT(*), MAX(updated) FROM posts`).Scan(&n, &upd)
	return fmt.Sprint(n.Int64, "-", upd.Int64)
}
