// Package sessionx persists ADK sessions as JSON files under .hivemind/sessions
// so a later process can resume the same conversation. ADK ships in-memory,
// database and Vertex session services; this file backend keeps the static
// binary free of a database server.
package sessionx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

const (
	// AppName is the ADK application id stored with every session.
	AppName = "hivemind"
	// UserID is the local operator. The CLI is single-user.
	UserID = "local"
)

// Meta is the summary shown by `hivemind session list` and IDE session telemetry.
type Meta struct {
	ID               string    `json:"id"`
	UpdatedAt        time.Time `json:"updatedAt"`
	Objective        string    `json:"objective,omitempty"`
	JiraKey          string    `json:"jiraKey,omitempty"`
	InputTokens      int64     `json:"inputTokens,omitempty"`
	OutputTokens     int64     `json:"outputTokens,omitempty"`
	TotalTokens      int64     `json:"totalTokens,omitempty"`
	CachedReadTokens int64     `json:"cachedReadTokens,omitempty"`
	// Kind is "hive" when the Agent Hive owns the thread. Empty means a team run.
	Kind string `json:"kind,omitempty"`
}

// Store implements session.Service on top of one JSON file per session.
type Store struct {
	dir         string
	mu          sync.Mutex
	live        map[string]*fileSession
	metas       map[string]Meta
	indexLoaded bool
}

// Open creates the external session directory for workspaceRoot if it does not exist,
// migrating any legacy <workspaceRoot>/.hivemind/sessions files automatically.
func Open(workspaceRoot string) (*Store, error) {
	dir := workspace.SessionsDir(workspaceRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, live: map[string]*fileSession{}, metas: map[string]Meta{}}, nil
}

// Create implements session.Service.
func (s *Store) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name e user_id são obrigatórios")
	}
	id := strings.TrimSpace(req.SessionID)
	if id == "" {
		id = newID()
	}
	if !validSessionID(id) {
		return nil, fmt.Errorf("session_id inválido: %q", id)
	}
	s.mu.Lock()
	if _, ok := s.live[id]; ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("sessão %s já existe", id)
	}
	if _, err := os.Stat(s.path(id)); err == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("sessão %s já existe", id)
	}
	fs := &fileSession{
		id:        id,
		appName:   req.AppName,
		userID:    req.UserID,
		state:     maps.Clone(req.State),
		updatedAt: time.Now().UTC(),
		store:     s,
	}
	if fs.state == nil {
		fs.state = map[string]any{}
	}
	fs.objective, _ = fs.state["objective"].(string)
	fs.jiraKey, _ = fs.state["jira_key"].(string)
	s.live[id] = fs
	meta := fs.metaNoLock()
	s.mu.Unlock()

	if err := fs.persist(meta); err != nil {
		s.mu.Lock()
		delete(s.live, id)
		s.mu.Unlock()
		return nil, err
	}
	return &session.CreateResponse{Session: fs}, nil
}

// Get implements session.Service.
func (s *Store) Get(_ context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("app_name, user_id e session_id são obrigatórios")
	}
	if !validSessionID(req.SessionID) {
		return nil, fmt.Errorf("session_id inválido: %q", req.SessionID)
	}
	s.mu.Lock()
	fs, ok := s.live[req.SessionID]
	if !ok {
		loaded, err := s.load(req.SessionID)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		fs = loaded
		s.live[req.SessionID] = fs
	} else {
		fs.mu.Lock()
		sanitizeSessionEventCallIDs(fs.events)
		fs.mu.Unlock()
	}
	s.mu.Unlock()
	if fs.appName != req.AppName || fs.userID != req.UserID {
		return nil, fmt.Errorf("%w: %s", session.ErrNotFound, req.SessionID)
	}
	return &session.GetResponse{Session: &filteredSession{fileSession: fs, after: req.After, recent: req.NumRecentEvents}}, nil
}

// List implements session.Service. Events are omitted here; Get loads them.
func (s *Store) List(context.Context, *session.ListRequest) (*session.ListResponse, error) {
	metas, err := s.ListMeta()
	if err != nil {
		return nil, err
	}
	out := make([]session.Session, 0, len(metas))
	for _, m := range metas {
		out = append(out, &fileSession{
			id:        m.ID,
			appName:   AppName,
			userID:    UserID,
			updatedAt: m.UpdatedAt,
			state:     map[string]any{},
		})
	}
	return &session.ListResponse{Sessions: out}, nil
}

// Delete implements session.Service.
func (s *Store) Delete(_ context.Context, req *session.DeleteRequest) error {
	if req == nil || !validSessionID(req.SessionID) {
		return fmt.Errorf("session_id inválido")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.ensureIndexLocked()
	delete(s.live, req.SessionID)
	delete(s.metas, req.SessionID)
	_ = os.Remove(s.path(req.SessionID))
	s.writeIndexLocked()
	return nil
}

func validSessionID(id string) bool {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" || trimmed != id || strings.EqualFold(trimmed, "index") {
		return false
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, `/\:*?"<>|`) {
		return false
	}
	return true
}

// AppendEvent implements session.Service.
func (s *Store) AppendEvent(ctx context.Context, cur session.Session, event *session.Event) error {
	if cur == nil || event == nil {
		return fmt.Errorf("sessão e evento são obrigatórios")
	}
	if event.Partial {
		return nil
	}
	fs, ok := unwrap(cur)
	if !ok {
		return fmt.Errorf("tipo de sessão inesperado: %T", cur)
	}
	if event.ID == "" {
		event.ID = newID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	fs.mu.Lock()
	if fs.state == nil {
		fs.state = map[string]any{}
	}
	for k, v := range event.Actions.StateDelta {
		fs.state[k] = v
	}
	fs.events = append(fs.events, cloneEvent(event))
	fs.updatedAt = event.Timestamp
	meta := fs.metaNoLock()
	fs.mu.Unlock()
	return fs.persist(meta)
}

// ListMeta returns session summaries, newest last.
func (s *Store) ListMeta() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureIndexLocked(); err != nil {
		return nil, err
	}
	entries := make([]Meta, 0, len(s.metas))
	for _, m := range s.metas {
		entries = append(entries, m)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.Before(entries[j].UpdatedAt)
	})
	return entries, nil
}

// SetObjective updates the fields shown in the session list and the state keys
// the agents read.
func SetObjective(sess session.Session, objective, jiraKey string) error {
	fs, ok := unwrap(sess)
	if !ok {
		return fmt.Errorf("sessão não persistente")
	}
	fs.mu.Lock()
	if fs.state == nil {
		fs.state = map[string]any{}
	}
	fs.state["objective"] = objective
	fs.state["jira_key"] = jiraKey
	fs.objective = objective
	fs.jiraKey = jiraKey
	fs.updatedAt = time.Now().UTC()
	meta := fs.metaNoLock()
	fs.mu.Unlock()
	return fs.persist(meta)
}

// UpdateState updates multiple state keys on sess and persists the snapshot to disk.
func UpdateState(sess session.Session, updates map[string]any) error {
	fs, ok := unwrap(sess)
	if !ok {
		for k, v := range updates {
			_ = sess.State().Set(k, v)
		}
		return nil
	}
	fs.mu.Lock()
	if fs.state == nil {
		fs.state = map[string]any{}
	}
	for k, v := range updates {
		fs.state[k] = v
	}
	fs.updatedAt = time.Now().UTC()
	meta := fs.metaNoLock()
	fs.mu.Unlock()
	return fs.persist(meta)
}

// CompactOldToolOutputs trims bulky FunctionResponse payloads (such as read_file,
// git_diff, and run_shell outputs) from older session turns while keeping the most
// recent keepRecentEvents events intact. This prevents prompt token explosion across
// multi-turn conversations.
func CompactOldToolOutputs(sess session.Session, keepRecentEvents int) error {
	fs, ok := unwrap(sess)
	if !ok {
		return nil
	}
	if keepRecentEvents <= 0 {
		keepRecentEvents = 8
	}
	const maxOldToolOutputBytes = 420
	const maxOldToolArgBytes = 240
	fs.mu.Lock()
	cutoff := len(fs.events) - keepRecentEvents
	if cutoff <= 0 {
		fs.mu.Unlock()
		return nil
	}
	changed := false
	for i := 0; i < cutoff; i++ {
		orig := fs.events[i]
		if orig == nil || orig.LLMResponse.Content == nil {
			continue
		}
		ev := cloneEvent(orig)
		eventChanged := false
		for _, part := range ev.LLMResponse.Content.Parts {
			if part == nil {
				continue
			}
			// A write_file/write_doc call carries the entire file body in its
			// arguments. Replaying that content in every later prompt is one of
			// the largest avoidable costs of a multi-agent run; the file itself
			// is already in the worktree.
			if part.FunctionCall != nil && len(part.FunctionCall.Args) > 0 {
				switch part.FunctionCall.Name {
				case "write_file", "write_doc":
					if content, ok := part.FunctionCall.Args["content"].(string); ok && len(content) > maxOldToolArgBytes {
						part.FunctionCall.Args["content"] = fmt.Sprintf("…(conteúdo de %d bytes compactado; arquivo já gravado na worktree)", len(content))
						eventChanged = true
					}
				case "edit_file":
					for _, field := range []string{"old_text", "new_text"} {
						if txt, ok := part.FunctionCall.Args[field].(string); ok && len(txt) > maxOldToolArgBytes {
							part.FunctionCall.Args[field] = fmt.Sprintf("…(trecho de %d bytes compactado; edição já aplicada na worktree)", len(txt))
							eventChanged = true
						}
					}
				}
			}
			if part.FunctionResponse == nil || part.FunctionResponse.Response == nil {
				continue
			}
			for k, val := range part.FunctionResponse.Response {
				strVal, isStr := val.(string)
				if !isStr || len(strVal) <= maxOldToolOutputBytes {
					continue
				}
				part.FunctionResponse.Response[k] = strx.TruncateBytes(
					strVal,
					maxOldToolOutputBytes,
					"\n…(saída de turno anterior compactada para economia de tokens)",
				)
				eventChanged = true
			}
		}
		if eventChanged {
			fs.events[i] = ev
			changed = true
		}
	}
	if !changed {
		fs.mu.Unlock()
		return nil
	}
	meta := fs.metaNoLock()
	fs.mu.Unlock()
	return fs.persist(meta)
}

func unwrap(sess session.Session) (*fileSession, bool) {
	switch s := sess.(type) {
	case *fileSession:
		return s, true
	case *filteredSession:
		return s.fileSession, true
	default:
		return nil, false
	}
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index.json") }

func (s *Store) load(id string) (*fileSession, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", session.ErrNotFound, id)
		}
		return nil, err
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("ler sessão %s: %w", id, err)
	}
	sanitizeSessionEventCallIDs(rec.Events)
	fs := &fileSession{
		id:        rec.ID,
		appName:   rec.AppName,
		userID:    rec.UserID,
		state:     rec.State,
		events:    rec.Events,
		updatedAt: rec.UpdatedAt,
		objective: rec.Objective,
		jiraKey:   rec.JiraKey,
		store:     s,
	}
	if fs.state == nil {
		fs.state = map[string]any{}
	}
	return fs, nil
}

// sanitizeSessionEventCallIDs repairs legacy persisted sessions that contain
// duplicate FunctionCall IDs across turns (e.g. "agy-1", "call_01", "call_02")
// or within parallel batches, as well as orphaned FunctionResponses.
// Duplicate or orphaned IDs cause OpenAI/OpenRouter adapters to fail with
// `received function response for unknown or already completed call id` and cause ADK's
// history rearrangement to swap/duplicate tool responses across turns.
func sanitizeSessionEventCallIDs(events []*session.Event) {
	if len(events) == 0 {
		return
	}
	type pendingCall struct {
		origID string
		newID  string
		name   string
		used   bool
	}
	usedCallIDs := make(map[string]bool)
	var pending []pendingCall

	for ei, ev := range events {
		if ev == nil || ev.LLMResponse.Content == nil {
			continue
		}
		hasCalls := false
		for _, p := range ev.LLMResponse.Content.Parts {
			if p != nil && p.FunctionCall != nil {
				hasCalls = true
				break
			}
		}
		if hasCalls {
			// Starting a new model tool-call turn resets any stale unmatched calls from earlier turns.
			pending = nil
		}

		var keptParts []*genai.Part
		partsChanged := false
		for pi, part := range ev.LLMResponse.Content.Parts {
			if part == nil {
				continue
			}
			if fc := part.FunctionCall; fc != nil {
				origID := strings.TrimSpace(fc.ID)
				newID := origID
				if newID == "" || usedCallIDs[newID] {
					newID = fmt.Sprintf("hm-call-%d-%d", ei, pi)
				}
				usedCallIDs[newID] = true
				fc.ID = newID
				pending = append(pending, pendingCall{
					origID: origID,
					newID:  newID,
					name:   strings.TrimSpace(fc.Name),
				})
				keptParts = append(keptParts, part)
				continue
			}
			if fr := part.FunctionResponse; fr != nil {
				origID := strings.TrimSpace(fr.ID)
				frName := strings.TrimSpace(fr.Name)
				matchedIdx := -1
				// 1. Match by exact original ID + tool Name first.
				for i := range pending {
					if !pending[i].used && pending[i].origID == origID && (frName == "" || strings.EqualFold(pending[i].name, frName)) {
						matchedIdx = i
						break
					}
				}
				// 2. Match by exact original ID alone.
				if matchedIdx < 0 && origID != "" {
					for i := range pending {
						if !pending[i].used && pending[i].origID == origID {
							matchedIdx = i
							break
						}
					}
				}
				// 3. Match by tool Name if original IDs collided in a parallel batch.
				if matchedIdx < 0 && frName != "" {
					for i := range pending {
						if !pending[i].used && strings.EqualFold(pending[i].name, frName) {
							matchedIdx = i
							break
						}
					}
				}
				if matchedIdx >= 0 {
					pending[matchedIdx].used = true
					fr.ID = pending[matchedIdx].newID
					keptParts = append(keptParts, part)
				} else {
					// Orphaned FunctionResponse with no pending FunctionCall: drop it so OpenAI/OpenRouter
					// CallTracker never fails with "unknown or already completed call id".
					partsChanged = true
				}
				continue
			}
			keptParts = append(keptParts, part)
		}
		if partsChanged {
			if len(keptParts) == 0 {
				keptParts = []*genai.Part{{Text: "ok"}}
			}
			ev.LLMResponse.Content.Parts = keptParts
		}
	}
}

func (s *Store) readIndexLocked() ([]Meta, error) {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx struct {
		Sessions []Meta `json:"sessions"`
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	sort.Slice(idx.Sessions, func(i, j int) bool {
		return idx.Sessions[i].UpdatedAt.Before(idx.Sessions[j].UpdatedAt)
	})
	return idx.Sessions, nil
}

func (s *Store) ensureIndexLocked() error {
	if s.indexLoaded {
		return nil
	}
	if s.metas == nil {
		s.metas = map[string]Meta{}
	}
	existing, err := s.readIndexLocked()
	if err != nil {
		return err
	}
	for _, m := range existing {
		if _, statErr := os.Stat(s.path(m.ID)); statErr == nil {
			if _, already := s.metas[m.ID]; !already {
				s.metas[m.ID] = m
			}
		}
	}
	s.indexLoaded = true
	return nil
}

// writeIndexLocked rewrites index.json. Caller holds s.mu.
// It must not lock a fileSession.
func (s *Store) writeIndexLocked() {
	_ = s.ensureIndexLocked()
	entries := make([]Meta, 0, len(s.metas))
	for _, m := range s.metas {
		entries = append(entries, m)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].UpdatedAt.Before(entries[j].UpdatedAt) })
	data, err := json.MarshalIndent(struct {
		Sessions []Meta `json:"sessions"`
	}{Sessions: entries}, "", "  ")
	if err != nil {
		return
	}
	_ = atomicWrite(s.indexPath(), data)
}

type record struct {
	ID        string           `json:"id"`
	AppName   string           `json:"appName"`
	UserID    string           `json:"userId"`
	UpdatedAt time.Time        `json:"updatedAt"`
	Objective string           `json:"objective,omitempty"`
	JiraKey   string           `json:"jiraKey,omitempty"`
	State     map[string]any   `json:"state"`
	Events    []*session.Event `json:"events"`
}

type fileSession struct {
	mu        sync.RWMutex
	writeMu   sync.Mutex
	id        string
	appName   string
	userID    string
	state     map[string]any
	events    []*session.Event
	updatedAt time.Time
	objective string
	jiraKey   string
	store     *Store
}

func (s *fileSession) ID() string      { return s.id }
func (s *fileSession) AppName() string { return s.appName }
func (s *fileSession) UserID() string  { return s.userID }
func (s *fileSession) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}
func (s *fileSession) State() session.State { return &memState{mu: &s.mu, m: s.state} }
func (s *fileSession) Events() session.Events {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]*session.Event, 0, len(s.events))
	for _, ev := range s.events {
		cp = append(cp, cloneEvent(ev))
	}
	return eventList(cp)
}

func cloneEvent(ev *session.Event) *session.Event {
	if ev == nil {
		return nil
	}
	cp := *ev
	if ev.Actions.StateDelta != nil {
		cp.Actions.StateDelta = maps.Clone(ev.Actions.StateDelta)
	}
	if ev.LLMResponse.Content != nil {
		c := *ev.LLMResponse.Content
		if len(ev.LLMResponse.Content.Parts) > 0 {
			c.Parts = make([]*genai.Part, len(ev.LLMResponse.Content.Parts))
			for i, p := range ev.LLMResponse.Content.Parts {
				if p == nil {
					continue
				}
				pc := *p
				if p.FunctionCall != nil {
					fc := *p.FunctionCall
					fc.Args = maps.Clone(p.FunctionCall.Args)
					pc.FunctionCall = &fc
				}
				if p.FunctionResponse != nil {
					fr := *p.FunctionResponse
					fr.Response = maps.Clone(p.FunctionResponse.Response)
					pc.FunctionResponse = &fr
				}
				c.Parts[i] = &pc
			}
		}
		cp.LLMResponse.Content = &c
	}
	return &cp
}

func (s *fileSession) metaNoLock() Meta {
	var inTok, outTok, totTok, cachedTok int64
	for _, ev := range s.events {
		if ev == nil || ev.Partial || ev.LLMResponse.UsageMetadata == nil {
			continue
		}
		u := ev.LLMResponse.UsageMetadata
		inTok += int64(u.PromptTokenCount)
		outTok += int64(u.CandidatesTokenCount)
		cachedTok += int64(u.CachedContentTokenCount)
		t := int64(u.TotalTokenCount)
		if t == 0 && (u.PromptTokenCount > 0 || u.CandidatesTokenCount > 0) {
			t = int64(u.PromptTokenCount) + int64(u.CandidatesTokenCount)
		}
		totTok += t
	}
	kind, _ := s.state["conversationKind"].(string)
	return Meta{
		ID:               s.id,
		UpdatedAt:        s.updatedAt,
		Objective:        s.objective,
		JiraKey:          s.jiraKey,
		InputTokens:      inTok,
		OutputTokens:     outTok,
		TotalTokens:      totTok,
		CachedReadTokens: cachedTok,
		Kind:             kind,
	}
}

func (s *fileSession) persist(_ Meta) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	rec := s.snapshot()
	meta := s.metaNoLock()
	s.mu.RUnlock()

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(s.store.path(s.id), data); err != nil {
		return err
	}
	s.store.mu.Lock()
	_ = s.store.ensureIndexLocked()
	s.store.metas[s.id] = meta
	s.store.writeIndexLocked()
	s.store.mu.Unlock()
	return nil
}

func (s *fileSession) writeLocked() error {
	rec := s.snapshot()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.store.path(s.id), data)
}

func (s *fileSession) snapshot() record {
	state := map[string]any{}
	for k, v := range s.state {
		if strings.HasPrefix(k, session.KeyPrefixTemp) {
			continue
		}
		state[k] = v
	}
	events := append([]*session.Event(nil), s.events...)
	return record{
		ID: s.id, AppName: s.appName, UserID: s.userID, UpdatedAt: s.updatedAt,
		Objective: s.objective, JiraKey: s.jiraKey, State: state, Events: events,
	}
}

// filteredSession applies GetRequest filters without copying the backing store.
type filteredSession struct {
	*fileSession
	after  time.Time
	recent int
}

func (f *filteredSession) Events() session.Events {
	all := f.fileSession.Events()
	list := make([]*session.Event, 0, all.Len())
	for i := 0; i < all.Len(); i++ {
		ev := all.At(i)
		if ev == nil {
			continue
		}
		if !f.after.IsZero() && ev.Timestamp.Before(f.after) {
			continue
		}
		list = append(list, ev)
	}
	if f.recent > 0 && len(list) > f.recent {
		list = list[len(list)-f.recent:]
	}
	return eventList(list)
}

type eventList []*session.Event

func (e eventList) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, ev := range e {
			if !yield(ev) {
				return
			}
		}
	}
}
func (e eventList) Len() int { return len(e) }
func (e eventList) At(i int) *session.Event {
	if i < 0 || i >= len(e) {
		return nil
	}
	return e[i]
}

type memState struct {
	mu *sync.RWMutex
	m  map[string]any
}

func (s *memState) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s *memState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		return fmt.Errorf("estado nulo")
	}
	s.m[key] = value
	return nil
}

func (s *memState) All() iter.Seq2[string, any] {
	s.mu.RLock()
	cp := maps.Clone(s.m)
	s.mu.RUnlock()
	return func(yield func(string, any) bool) {
		for k, v := range cp {
			if !yield(k, v) {
				return
			}
		}
	}
}

func newID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Chmod(0o600)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*10) * time.Millisecond)
		}
		if err := os.Rename(tmp, path); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	_ = os.Remove(tmp)
	return lastErr
}

// Compile-time check.
var _ session.Service = (*Store)(nil)
