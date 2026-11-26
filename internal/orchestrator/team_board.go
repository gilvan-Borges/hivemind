package orchestrator

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/vaultx"
)

// TeamNote is a structured contract or coordination signal published between developers,
// QA, and the Chief sharing the same conversation worktree.
type TeamNote struct {
	Author  string
	To      string
	Topic   string
	Content string
}

// TeamDirective is a high-priority live steering instruction issued during the
// Roundtable by the Chief or User to redirect an active agent in real time.
type TeamDirective struct {
	From      string
	To        string
	Content   string
	delivered map[string]bool
}

// TeamBoard holds live in-memory team awareness for a single run:
//   - which file/directory slices each developer owns
//   - which files each developer actually modified (recorded automatically)
//   - recent tool actions per agent so the Chief can inspect what a Dev has been doing
//   - live Roundtable directives from the Chief or User
//   - concise contract/interface notes published via share_team_note
type TeamBoard struct {
	mu         sync.RWMutex
	order      []string
	owners     map[string][]string
	touched    map[string][]string
	trail      map[string][]string
	notes      []TeamNote
	directives []TeamDirective
	qaFailures []string
	vaultRoot  string
	vaultBr    string
	vaultObj   string
}

func newTeamBoard() *TeamBoard {
	return &TeamBoard{
		owners:  make(map[string][]string),
		touched: make(map[string][]string),
		trail:   make(map[string][]string),
	}
}

func (b *TeamBoard) RecordActivity(agentName, action string) {
	if b == nil {
		return
	}
	ag := strings.TrimSpace(agentName)
	act := strings.TrimSpace(action)
	if ag == "" || act == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.trail[ag]
	if len(list) > 0 && list[len(list)-1] == act {
		return
	}
	list = append(list, act)
	if len(list) > 12 {
		list = append([]string(nil), list[len(list)-12:]...)
	}
	b.trail[ag] = list
}

func (b *TeamBoard) RecentActivity(agentName string) []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	list := b.trail[strings.TrimSpace(agentName)]
	if len(list) == 0 {
		return nil
	}
	out := make([]string, len(list))
	copy(out, list)
	return out
}

func (b *TeamBoard) SetVaultPersistence(root, branch, objective string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.vaultRoot = strings.TrimSpace(root)
	b.vaultBr = strings.TrimSpace(branch)
	b.vaultObj = strings.TrimSpace(objective)
	snap, vRoot := b.snapshotLocked()
	b.mu.Unlock()
	if vRoot != "" {
		_ = vaultx.SyncSquadBoard(vRoot, snap)
	}
}

func (b *TeamBoard) AddQAFailure(note string) {
	if b == nil {
		return
	}
	clean := strings.TrimSpace(note)
	if clean == "" {
		return
	}
	b.mu.Lock()
	b.qaFailures = append(b.qaFailures, clean)
	if len(b.qaFailures) > 10 {
		b.qaFailures = append([]string(nil), b.qaFailures[len(b.qaFailures)-10:]...)
	}
	snap, vRoot := b.snapshotLocked()
	b.mu.Unlock()
	if vRoot != "" {
		_ = vaultx.SyncSquadBoard(vRoot, snap)
	}
}

func (b *TeamBoard) snapshotLocked() (vaultx.SquadBoardSnapshot, string) {
	if b.vaultRoot == "" {
		return vaultx.SquadBoardSnapshot{}, ""
	}
	owners := make(map[string][]string, len(b.owners))
	for k, v := range b.owners {
		owners[k] = append([]string(nil), v...)
	}
	touched := make(map[string][]string, len(b.touched))
	for k, v := range b.touched {
		touched[k] = append([]string(nil), v...)
	}
	notes := make([]vaultx.SquadNote, 0, len(b.notes))
	for _, n := range b.notes {
		notes = append(notes, vaultx.SquadNote{Author: n.Author, To: n.To, Topic: n.Topic, Content: n.Content})
	}
	dirs := make([]vaultx.SquadDirective, 0, len(b.directives))
	for _, d := range b.directives {
		dirs = append(dirs, vaultx.SquadDirective{From: d.From, To: d.To, Content: d.Content})
	}
	return vaultx.SquadBoardSnapshot{
		Objective:  b.vaultObj,
		Branch:     b.vaultBr,
		Owners:     owners,
		Touched:    touched,
		Notes:      notes,
		Directives: dirs,
		QAFailures: append([]string(nil), b.qaFailures...),
	}, b.vaultRoot
}

func (b *TeamBoard) PublishDirective(from, to, content string) {
	if b == nil {
		return
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if from == "" {
		from = "chefe"
	}
	if to == "" {
		to = "todos"
	}
	b.mu.Lock()
	b.directives = append(b.directives, TeamDirective{
		From:      from,
		To:        to,
		Content:   content,
		delivered: make(map[string]bool),
	})
	snap, vRoot := b.snapshotLocked()
	b.mu.Unlock()
	if vRoot != "" {
		_ = vaultx.SyncSquadBoard(vRoot, snap)
	}
}

func directiveMatchesAgent(target, agentName string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	a := strings.ToLower(strings.TrimSpace(agentName))
	if t == "" || t == "todos" || t == "all" || t == "*" || t == a {
		return true
	}
	if t == "dev" && strings.HasPrefix(a, "dev") {
		return true
	}
	if t == "qa" && strings.HasPrefix(a, "qa") {
		return true
	}
	return false
}

func (b *TeamBoard) PendingDirectivesFor(agentName string, markDelivered bool) []TeamDirective {
	if b == nil {
		return nil
	}
	ag := strings.ToLower(strings.TrimSpace(agentName))
	if ag == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []TeamDirective
	for i := range b.directives {
		d := &b.directives[i]
		if !directiveMatchesAgent(d.To, ag) || d.delivered[ag] {
			continue
		}
		if markDelivered {
			d.delivered[ag] = true
		}
		out = append(out, TeamDirective{
			From:    d.From,
			To:      d.To,
			Content: d.Content,
		})
	}
	return out
}

// UnreadNotificationFor returns a structured <agent_message_board_notification> block
// containing unread live directives addressed to agentName (or "todos") and marks them
// delivered so each message is surfaced once without bloating subsequent turns.
func (b *TeamBoard) UnreadNotificationFor(agentName string) string {
	pending := b.PendingDirectivesFor(agentName, true)
	if len(pending) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("<agent_message_board_notification>\n")
	for _, d := range pending {
		fmt.Fprintf(&out, "- [%s → %s]: %s\n", d.From, d.To, d.Content)
	}
	out.WriteString("</agent_message_board_notification>")
	return out.String()
}

func (b *TeamBoard) RegisterOwner(agentName string, paths []string) {
	if b == nil {
		return
	}
	norm := NormalizeOwnedPaths(paths)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, seen := b.owners[agentName]; !seen {
		b.order = append(b.order, agentName)
	}
	b.owners[agentName] = norm
}

func (b *TeamBoard) RecordTouch(agentName, relSlash string) {
	if b == nil {
		return
	}
	rel := normalizeRelPattern(relSlash)
	if rel == "" {
		return
	}
	b.mu.Lock()
	for _, existing := range b.touched[agentName] {
		if existing == rel {
			b.mu.Unlock()
			return
		}
	}
	b.touched[agentName] = append(b.touched[agentName], rel)
	snap, vRoot := b.snapshotLocked()
	b.mu.Unlock()
	if vRoot != "" {
		_ = vaultx.SyncSquadBoard(vRoot, snap)
	}
}

func (b *TeamBoard) PublishNote(author, topic, content string, toOpt ...string) {
	if b == nil {
		return
	}
	author = strings.TrimSpace(author)
	topic = strings.TrimSpace(topic)
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if topic == "" {
		topic = "contrato"
	}
	to := "todos"
	if len(toOpt) > 0 && strings.TrimSpace(toOpt[0]) != "" {
		to = strings.TrimSpace(toOpt[0])
	}
	b.mu.Lock()
	updated := false
	// Update existing note with same author+topic so repeated publishes stay compact.
	for i := range b.notes {
		if b.notes[i].Author == author && strings.EqualFold(b.notes[i].Topic, topic) {
			b.notes[i].To = to
			b.notes[i].Content = content
			updated = true
			break
		}
	}
	if !updated {
		b.notes = append(b.notes, TeamNote{
			Author:  author,
			To:      to,
			Topic:   topic,
			Content: content,
		})
	}
	snap, vRoot := b.snapshotLocked()
	b.mu.Unlock()
	if vRoot != "" {
		_ = vaultx.SyncSquadBoard(vRoot, snap)
	}
}

// SummaryFor renders a compact shared-awareness block injected into sibling devs
// and QA without dumping raw chat transcripts.
func (b *TeamBoard) SummaryFor(currentAgent string) string {
	if b == nil {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.order) == 0 && len(b.touched) == 0 && len(b.notes) == 0 && len(b.directives) == 0 {
		return ""
	}

	var out strings.Builder
	if len(b.order) > 0 {
		var lines []string
		for _, ag := range b.order {
			paths := b.owners[ag]
			touched := b.touched[ag]
			if len(paths) == 0 && len(touched) == 0 {
				continue
			}
			roleTag := ag
			if ag == currentAgent {
				roleTag += " (você)"
			}
			part := "- " + roleTag
			if len(paths) > 0 {
				part += ": dono de `" + strings.Join(paths, "`, `") + "`"
			}
			if len(touched) > 0 {
				part += " · editou: `" + strings.Join(touched, "`, `") + "`"
			}
			lines = append(lines, part)
		}
		if len(lines) > 0 {
			out.WriteString("[Memória Compartilhada do Time — Worktree Unificada]\n")
			out.WriteString(strings.Join(lines, "\n"))
		}
	} else if len(b.touched) > 0 {
		agents := make([]string, 0, len(b.touched))
		for ag := range b.touched {
			agents = append(agents, ag)
		}
		sort.Strings(agents)
		out.WriteString("[Arquivos modificados pelo time nesta conversa]\n")
		for _, ag := range agents {
			fmt.Fprintf(&out, "- %s: `%s`\n", ag, strings.Join(b.touched[ag], "`, `"))
		}
	}

	if len(b.directives) > 0 {
		var dirLines []string
		for _, d := range b.directives {
			if currentAgent == "" || directiveMatchesAgent(d.To, currentAgent) {
				dirLines = append(dirLines, fmt.Sprintf("- [%s → %s]: %s", d.From, d.To, d.Content))
			}
		}
		if len(dirLines) > 0 {
			if out.Len() > 0 {
				out.WriteString("\n")
			}
			out.WriteString("[🧭 Mesa Redonda ao Vivo — Direcionamentos do Chefe & Time]\n")
			out.WriteString(strings.Join(dirLines, "\n"))
		}
	}

	if len(b.notes) > 0 {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString("[Contratos & Notas Publicadas entre Devs]\n")
		for _, n := range b.notes {
			toTag := ""
			if strings.TrimSpace(n.To) != "" && !strings.EqualFold(n.To, "todos") {
				toTag = " → " + n.To
			}
			fmt.Fprintf(&out, "- [%s%s · %s]: %s\n", n.Author, toTag, n.Topic, n.Content)
		}
	}
	return strings.TrimSpace(out.String())
}

func normalizeRelPattern(raw string) string {
	s := strings.TrimSpace(filepath.ToSlash(raw))
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimLeft(s, "/")
	s = path.Clean(s)
	if s == "." || s == "" || s == ".." || strings.HasPrefix(s, "../") {
		return ""
	}
	// Preserve trailing wildcard simplification e.g. "ide/frontend/**" -> "ide/frontend".
	for strings.HasSuffix(s, "/**") || strings.HasSuffix(s, "/*") {
		if strings.HasSuffix(s, "/**") {
			s = strings.TrimSuffix(s, "/**")
		} else {
			s = strings.TrimSuffix(s, "/*")
		}
	}
	return s
}

// NormalizeOwnedPaths cleans and deduplicates a slice of relative path prefixes/globs.
func NormalizeOwnedPaths(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		p := normalizeRelPattern(r)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// DisjointOwnedPaths reports whether every developer (0..devCount-1) has at least
// one explicit owned path and no two developers have overlapping paths.
// Paths in separate services/subtrees return true so devs can safely share one worktree
// with deterministic tool-layer write guards.
func DisjointOwnedPaths(slices [][]string, devCount int) bool {
	if devCount <= 1 {
		return true
	}
	if len(slices) < devCount {
		return false
	}
	normPerDev := make([][]string, devCount)
	for i := 0; i < devCount; i++ {
		norm := NormalizeOwnedPaths(slices[i])
		if len(norm) == 0 {
			return false
		}
		normPerDev[i] = norm
	}
	for i := 0; i < devCount; i++ {
		for j := i + 1; j < devCount; j++ {
			if pathsOverlap(normPerDev[i], normPerDev[j]) {
				return false
			}
		}
	}
	return true
}

func pathsOverlap(aList, bList []string) bool {
	for _, a := range aList {
		for _, b := range bList {
			al := strings.ToLower(a)
			bl := strings.ToLower(b)
			if al == bl ||
				strings.HasPrefix(al, bl+"/") ||
				strings.HasPrefix(bl, al+"/") {
				return true
			}
		}
	}
	return false
}

// MatchesOwnedPaths reports whether relPath falls under one of owned paths (exact file,
// directory prefix, or path.Match glob).
func MatchesOwnedPaths(relPath string, owned []string) bool {
	clean := normalizeRelPattern(relPath)
	if clean == "" {
		return false
	}
	cleanLower := strings.ToLower(clean)
	for _, raw := range owned {
		p := normalizeRelPattern(raw)
		if p == "" {
			continue
		}
		pLower := strings.ToLower(p)
		if cleanLower == pLower || strings.HasPrefix(cleanLower, pLower+"/") {
			return true
		}
		if strings.ContainsAny(pLower, "*?[") {
			if ok, _ := path.Match(pLower, cleanLower); ok {
				return true
			}
		}
	}
	return false
}

func resolveToolRelPath(root, argPath string) string {
	p := strings.TrimSpace(argPath)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) && strings.TrimSpace(root) != "" {
		if rel, err := filepath.Rel(root, p); err == nil {
			p = rel
		}
	}
	return normalizeRelPattern(p)
}

// newOwnedPathsGuardCallback deterministically prevents a developer from writing outside
// its assigned owned_paths when multiple developers share one conversation worktree.
func newOwnedPathsGuardCallback(root, agentName string, owned []string, board *TeamBoard) llmagent.BeforeToolCallback {
	cleanOwned := NormalizeOwnedPaths(owned)
	if len(cleanOwned) == 0 {
		return nil
	}
	return func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		if !devWriteTools[name] {
			return nil, nil
		}
		rawPath, _ := args["path"].(string)
		rel := resolveToolRelPath(root, rawPath)
		if MatchesOwnedPaths(rel, cleanOwned) {
			return nil, nil
		}
		ownerHint := ""
		if board != nil {
			board.mu.RLock()
			for peer, peerPaths := range board.owners {
				if peer == agentName || len(peerPaths) == 0 {
					continue
				}
				if MatchesOwnedPaths(rel, peerPaths) {
					ownerHint = fmt.Sprintf(" Esse caminho pertence a %s.", peer)
					break
				}
			}
			board.mu.RUnlock()
		}
		return map[string]any{
			"blocked": true,
			"error": fmt.Sprintf(
				"posse de arquivo bloqueada: %q está fora do seu owned_paths (%s).%s Restrinja edições ao seu escopo ou publique um contrato via share_team_note.",
				rawPath,
				strings.Join(cleanOwned, ", "),
				ownerHint,
			),
		}, nil
	}
}

func newTeamBoardTouchCallback(root, agentName string, board *TeamBoard) llmagent.AfterToolCallback {
	if board == nil {
		return nil
	}
	return func(_ agent.Context, tl tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		if err != nil || tl == nil {
			return nil, nil
		}
		if blocked, _ := result["blocked"].(bool); blocked {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		if !devWriteTools[name] {
			return nil, nil
		}
		rawPath, _ := args["path"].(string)
		if rel := resolveToolRelPath(root, rawPath); rel != "" {
			board.RecordTouch(agentName, rel)
		}
		return nil, nil
	}
}

func newShareTeamNoteTool(agentName string, board *TeamBoard, onEventOpt ...func(Event)) (tool.Tool, error) {
	var onEvent func(Event)
	if len(onEventOpt) > 0 {
		onEvent = onEventOpt[0]
	}
	type shareArgs struct {
		To      string `json:"to,omitempty" jsonschema:"Destinatário opcional na Mesa Redonda (ex.: todos, chefe, qa, dev_1, dev_2). Padrão: todos."`
		Topic   string `json:"topic" jsonschema:"Assunto curto (ex.: contrato-api, schema, tipos, aviso-build, duvida)."`
		Content string `json:"content" jsonschema:"Mensagem ou nota concisa com assinatura, rota, tipo, contrato ou alinhamento para o time em tempo real."`
	}
	type shareRes struct {
		OK bool `json:"ok"`
	}
	return functiontool.New(functiontool.Config{
		Name:        "share_team_note",
		Description: "Envia uma mensagem ou contrato de interface ao vivo na Mesa Redonda para os outros Devs, QA e Chefe na mesma worktree.",
	}, func(_ agent.Context, args shareArgs) (shareRes, error) {
		if strings.TrimSpace(args.Content) == "" {
			return shareRes{OK: false}, fmt.Errorf("informe content na nota")
		}
		target := strings.TrimSpace(args.To)
		if target == "" {
			target = "todos"
		}
		board.PublishNote(agentName, args.Topic, args.Content, target)
		if onEvent != nil {
			topicLabel := strings.TrimSpace(args.Topic)
			if topicLabel == "" {
				topicLabel = "alinhamento"
			}
			onEvent(Event{
				Kind:   "handoff",
				Stage:  inferStage(agentName, agentName),
				Author: agentName,
				Label:  fmt.Sprintf("Mesa Redonda · %s → %s (%s)", agentName, target, topicLabel),
				Text:   strings.TrimSpace(args.Content),
			})
		}
		return shareRes{OK: true}, nil
	})
}

func alignOwnedPaths(raw [][]string, n int) [][]string {
	if len(raw) == 0 || n <= 0 {
		return nil
	}
	out := make([][]string, n)
	for i := 0; i < n && i < len(raw); i++ {
		out[i] = NormalizeOwnedPaths(raw[i])
	}
	return out
}

func marshalOwnedPaths(paths [][]string) string {
	if len(paths) == 0 {
		return ""
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalOwnedPaths(raw string) [][]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out [][]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}
