package sessionx

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"path/filepath"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

type readCursorFile struct {
	Seen map[string]int `json:"seen"`
}

func (s *Store) annotateConversations(ctx context.Context, rows []SessionSummary) {
	if s == nil || len(rows) == 0 {
		return
	}
	cursors := s.loadCursors()
	dirty := false
	for i := range rows {
		got, err := s.Get(ctx, &session.GetRequest{
			AppName:   AppName,
			UserID:    UserID,
			SessionID: rows[i].ID,
		})
		if err != nil || got.Session == nil {
			continue
		}
		preview, n := chatTail(got.Session)
		rows[i].Preview = preview
		seen, ok := cursors[rows[i].ID]
		if !ok {
			cursors[rows[i].ID] = n
			dirty = true
			continue
		}
		if n > seen {
			rows[i].Unread = n - seen
		}
	}
	if dirty {
		_ = s.saveCursors(cursors)
	}
}

func (s *Store) markRead(sessionID string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cursors := s.loadCursorsLocked()
	cursors[sessionID] = n
	return s.saveCursorsLocked(cursors)
}

func (s *Store) loadCursors() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCursorsLocked()
}

func (s *Store) loadCursorsLocked() map[string]int {
	data, err := os.ReadFile(s.readsPath())
	if err != nil {
		return map[string]int{}
	}
	var file readCursorFile
	if json.Unmarshal(data, &file) != nil || file.Seen == nil {
		return map[string]int{}
	}
	return file.Seen
}

func (s *Store) saveCursors(cursors map[string]int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCursorsLocked(cursors)
}

func (s *Store) saveCursorsLocked(cursors map[string]int) error {
	raw, err := json.MarshalIndent(readCursorFile{Seen: cursors}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.readsPath(), append(raw, '\n'))
}

func (s *Store) readsPath() string {
	return filepath.Join(s.dir, "reads.json")
}

func chatTail(sess session.Session) (preview string, n int) {
	if sess == nil {
		return "", 0
	}
	evs := sess.Events()
	for i := 0; i < evs.Len(); i++ {
		ev := evs.At(i)
		if ev == nil || ev.Partial || ev.LLMResponse.Content == nil {
			continue
		}
		for _, part := range ev.LLMResponse.Content.Parts {
			if part == nil || part.Thought || strings.TrimSpace(part.Text) == "" {
				continue
			}
			n++
			preview = strings.TrimSpace(part.Text)
		}
	}
	return strx.TruncateRunes(preview, 140, "…"), n
}
