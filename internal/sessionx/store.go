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
)

const (
	// AppName is the ADK application id stored with every session.
	AppName = "hivemind"
	// UserID is the local operator. The CLI is single-user.
	UserID = "local"
)

// Meta is the summary shown by `hivemind session list`.
type Meta struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updatedAt"`
	Objective string    `json:"objective,omitempty"`
	JiraKey   string    `json:"jiraKey,omitempty"`
}

// Store implements session.Service on top of one JSON file per session.
type Store struct {
	dir   string
	mu    sync.Mutex
	live  map[string]*fileSession
	metas map[string]Meta
}

// Open creates the session directory if it does not exist.
func Open(workspace string) (*Store, error) {
	dir := filepath.Join(workspace, ".hivemind", "sessions")
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
	id := req.SessionID
	if id == "" {
		id = newID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(id)); err == nil {
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
	if err := fs.writeLocked(); err != nil {
		return nil, err
	}
	s.live[id] = fs
	s.metas[id] = fs.metaNoLock()
	s.writeIndexLocked()
	return &session.CreateResponse{Session: fs}, nil
}

// Get implements session.Service.
func (s *Store) Get(_ context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("app_name, user_id e session_id são obrigatórios")
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
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.live, req.SessionID)
	delete(s.metas, req.SessionID)
	_ = os.Remove(s.path(req.SessionID))
	s.writeIndexLocked()
	return nil
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
	fs.events = append(fs.events, event)
	fs.updatedAt = event.Timestamp
	meta := fs.metaNoLock()
	fs.mu.Unlock()
	return fs.persist(meta)
}

// ListMeta returns session summaries, newest last.
func (s *Store) ListMeta() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readIndexLocked()
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

// writeIndexLocked rewrites index.json. Caller holds s.mu.
// It must not lock a fileSession.
func (s *Store) writeIndexLocked() {
	existing, _ := s.readIndexLocked()
	byID := map[string]Meta{}
	for _, m := range existing {
		if _, err := os.Stat(s.path(m.ID)); err == nil {
			byID[m.ID] = m
		}
	}
	for id, m := range s.metas {
		if _, err := os.Stat(s.path(id)); err == nil {
			byID[id] = m
		}
	}
	entries := make([]Meta, 0, len(byID))
	for _, m := range byID {
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
	cp := append([]*session.Event(nil), s.events...)
	return eventList(cp)
}

func (s *fileSession) metaNoLock() Meta {
	return Meta{ID: s.id, UpdatedAt: s.updatedAt, Objective: s.objective, JiraKey: s.jiraKey}
}

func (s *fileSession) persist(meta Meta) error {
	s.mu.RLock()
	rec := s.snapshot()
	s.mu.RUnlock()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(s.store.path(s.id), data); err != nil {
		return err
	}
	s.store.mu.Lock()
	if s.store.metas == nil {
		s.store.metas = map[string]Meta{}
	}
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Compile-time check.
var _ session.Service = (*Store)(nil)
