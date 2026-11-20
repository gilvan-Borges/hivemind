package tui

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

type modal int

const (
	modalNone modal = iota
	modalWizard
	modalModels
	modalSessions
	modalTeams
	modalPermissions
	modalApprove
	modalBring
)

type agentRow struct {
	Name   string
	Role   string
	Title  string
	Model  string
	Status string
	Tool   string
	Tokens int
	Start  time.Time
}

type block struct {
	Kind      string
	Author    string
	Body      string
	Tool      string
	Collapsed bool
}

type fileChange struct {
	Add int
	Del int
}

type cmdItem struct {
	Name string
	Desc string
}

type tickMsg time.Time
type modelsMsg struct{ names []string }
type streamed struct {
	Event          orchestrator.Event
	Err            error
	Done           bool
	SessionID      string
	HasIntegration bool
	Files          map[string]fileChange
	ApprovalPrompt string
	ApprovalReply  chan bool
}

type model struct {
	ctx              context.Context
	workspace        string
	themeIdx         int
	width            int
	height           int
	branch           string
	project          team.File
	hasTeam          bool
	sessionID        string
	persistedSession bool
	started          time.Time
	tokens           int

	ta      textarea.Model
	vp      viewport.Model
	follow  bool
	blocks  []block
	agents  []agentRow
	files   map[string]fileChange
	history []string
	histAt  int
	running bool
	cancel  context.CancelFunc
	runCtx  context.Context
	queue   []string
	events  chan streamed
	spin    int
	quitArm bool
	quitAt  time.Time
	status  string

	modal         modal
	wizardStep    int
	wizName       string
	wizDevs       string
	wizModels     []string
	wizModel      int
	wizEffort     int
	efforts       []string
	sameModel     bool
	wizWT         bool
	wizCommit     bool
	wizPR         bool
	wizGitHub     bool
	wizJira       bool
	wizJiraKey    string
	pick          int
	pickItems     []string
	slashSel      int
	atSel         int
	atQuery       string
	pending       string
	approvalReply chan bool
}

func newModel(ctx context.Context, opt Options) model {
	ta := textarea.New()
	ta.Placeholder = "❯ Peça algo ao time…   /ajuda"
	ta.SetVirtualCursor(false)
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetWidth(40)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	km := textarea.DefaultKeyMap()
	km.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"), key.WithHelp("shift+enter", "nova linha"))
	ta.KeyMap = km
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(styles)
	ta.Prompt = ""

	vp := viewport.New(viewport.WithWidth(40), viewport.WithHeight(10))
	vp.MouseWheelEnabled = true

	m := model{
		ctx: ctx, workspace: opt.Workspace, sessionID: newSessionID(), started: time.Now(),
		ta: ta, vp: vp, follow: true, files: map[string]fileChange{},
		branch: "sem git", efforts: []string{"low", "medium", "high"},
		wizName: "padrao", wizDevs: "1", wizWT: true, wizCommit: true, wizPR: true, wizEffort: 1,
		status: "pronto",
	}
	git := gitx.Local{}
	if b, err := git.CurrentBranch(context.Background(), opt.Workspace); err == nil && b != "" && b != "HEAD" {
		m.branch = b
	}
	if f, err := team.Load(opt.Workspace); err == nil {
		if opt.TeamName != "" {
			f.Active = opt.TeamName
		}
		m.project = f
		m.hasTeam = true
		if _, err := f.ActiveTeam(""); err == nil {
			m.rebuildAgents()
		}
		m.addSystem("Time **" + f.Active + "** pronto. `/ajuda` lista os comandos.")
	} else {
		m.modal = modalWizard
		m.addSystem("Primeiro uso: vamos montar o time.")
	}
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, doTick(), loadAgyModels())
}

func doTick() tea.Cmd {
	return tea.Tick(180*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func loadAgyModels() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		names, err := listAgyModels(ctx)
		if err != nil || len(names) == 0 {
			return modelsMsg{names: []string{"agy"}}
		}
		return modelsMsg{names: names}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.spin++
		if m.quitArm && time.Since(m.quitAt) > 2*time.Second {
			m.quitArm = false
		}
		return m, doTick()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.refresh()
		return m, nil
	case modelsMsg:
		m.wizModels = msg.names
		if m.wizModel >= len(m.wizModels) {
			m.wizModel = 0
		}
		return m, nil
	case streamed:
		return m.onStream(msg)
	case tea.MouseWheelMsg:
		if m.modal == modalNone {
			m.vp, _ = m.vp.Update(msg)
			m.follow = m.vp.AtBottom()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}
}

func (m model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyName := msg.String()
	if m.modal != modalNone {
		return m.onModalKey(keyName)
	}
	switch keyName {
	case "ctrl+c":
		if m.quitArm {
			return m, tea.Quit
		}
		m.quitArm = true
		m.quitAt = time.Now()
		m.status = "Ctrl+C de novo para sair"
		return m, nil
	case "esc":
		if m.running {
			m.stopRun("cancelado")
			m.addSystem("Execução cancelada.")
			m.refresh()
			return m, nil
		}
		m.ta.Reset()
		return m, nil
	case "pgup":
		m.vp.ScrollUp(m.vp.Height() / 2)
		m.follow = false
		return m, nil
	case "pgdown":
		m.vp.ScrollDown(m.vp.Height() / 2)
		m.follow = m.vp.AtBottom()
		return m, nil
	case "ctrl+t":
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].Kind == "tool" {
				m.blocks[i].Collapsed = !m.blocks[i].Collapsed
				break
			}
		}
		m.refresh()
		return m, nil
	case "up":
		if strings.TrimSpace(m.ta.Value()) == "" && len(m.history) > 0 {
			if m.histAt > 0 {
				m.histAt--
			}
			m.ta.SetValue(m.history[m.histAt])
			return m, nil
		}
	case "tab":
		if m.slashOpen() {
			m.completeSlash()
			return m, nil
		}
		if m.atOpen() {
			m.completeAt()
			return m, nil
		}
	case "enter":
		if m.slashOpen() {
			items := m.slashMatches()
			if m.slashSel < len(items) {
				m.ta.SetValue(items[m.slashSel].Name)
			}
		}
		if m.atOpen() {
			m.completeAt()
			return m, nil
		}
		return m.submit()
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	if m.slashOpen() {
		m.slashSel = 0
	}
	m.refresh()
	return m, cmd
}

func (m model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.ta.Value())
	if text == "" || m.running {
		return m, nil
	}
	m.ta.Reset()
	m.history = append(m.history, text)
	m.histAt = len(m.history)
	if strings.HasPrefix(text, "/") {
		return m.runSlash(text)
	}
	m.blocks = append(m.blocks, block{Kind: "user", Author: "Você", Body: text})
	m.refresh()
	return m.beginRun(text)
}

func (m model) beginRun(text string) (tea.Model, tea.Cmd) {
	if !m.hasTeam {
		m.addSystem("Crie um time antes de enviar.")
		m.refresh()
		return m, nil
	}
	squad, err := m.project.ActiveTeam("")
	if err != nil {
		m.addSystem("Erro no time ativo: " + err.Error())
		m.refresh()
		return m, nil
	}
	m.running = true
	m.files = map[string]fileChange{}
	for i := range m.agents {
		m.agents[i].Status = "aguardando"
		m.agents[i].Tool = ""
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.runCtx = ctx
	m.status = "time em execução"
	m.refresh()

	reqSessionID := ""
	if m.persistedSession {
		reqSessionID = m.sessionID
	}
	projectSnapshot := m.project
	workspaceDir := m.workspace
	ch := make(chan streamed, 64)
	askHuman := func(prompt string) bool {
		if projectSnapshot.Settings.IsFullAccess() {
			return true
		}
		reply := make(chan bool, 1)
		select {
		case ch <- streamed{ApprovalPrompt: prompt, ApprovalReply: reply}:
		case <-ctx.Done():
			return false
		}
		select {
		case ok := <-reply:
			return ok
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(ch)
		nativeRoot := skillx.UserSkillsDir()
		_, _ = skillx.SeedProductSkills(nativeRoot)
		mcpHost := mcpx.Start(ctx, workspaceDir)
		defer mcpHost.Close()
		r := &orchestrator.Runner{
			Workspace:          workspaceDir,
			Project:            projectSnapshot,
			Team:               squad,
			Git:                gitx.Local{},
			Confirm:            askHuman,
			Approve:            askHuman,
			MCP:                mcpHost,
			NativeSkills:       skillx.LoadNative(nativeRoot),
			NativeSkillsRoot:   nativeRoot,
			Out:                io.Discard,
			Err:                io.Discard,
			EnableTriage:       true,
			EnableClosingChief: true,
			OnEvent: func(ev orchestrator.Event) {
				select {
				case ch <- streamed{Event: ev}:
				case <-ctx.Done():
				}
			},
		}
		r.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
			Workspace: workspaceDir,
			Approve:   askHuman,
			OnEvent:   r.OnEvent,
		})
		res, runErr := r.Run(ctx, orchestrator.Request{
			Objective: text,
			SessionID: reqSessionID,
			RunMode:   "auto",
		})
		hasInt := false
		if runErr == nil && res.IntegrationBranch != "" {
			hasInt = hasIntegrationAhead(workspaceDir)
		}
		files := collectDiffStats(workspaceDir, hasInt)
		select {
		case ch <- streamed{
			Done:           true,
			Err:            runErr,
			SessionID:      res.SessionID,
			HasIntegration: hasInt,
			Files:          files,
		}:
		case <-ctx.Done():
			ch <- streamed{Done: true, Err: ctx.Err(), SessionID: res.SessionID}
		}
	}()
	m.events = ch
	return m, listen(ch)
}

func listen(ch chan streamed) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamed{Done: true}
		}
		return ev
	}
}

func (m model) onStream(msg streamed) (tea.Model, tea.Cmd) {
	if msg.ApprovalReply != nil {
		m.pending = msg.ApprovalPrompt
		m.approvalReply = msg.ApprovalReply
		m.modal = modalApprove
		m.refresh()
		return m, nil
	}
	ev := msg.Event
	switch ev.Kind {
	case "stage":
		if ev.Stage != "" {
			m.status = ev.Stage
		}
	case "usage":
		m.tokens += int(ev.Tokens)
		if row := m.matchAgent(ev.Author, ev.Label); row != nil {
			row.Tokens += int(ev.Tokens)
		}
	case "tool_call":
		agName := m.agentKeyFor(ev.Author, ev.Label)
		m.setAgent(agName, "usando "+ev.ToolName, ev.ToolName)
		authorTitle := ev.Label
		if authorTitle == "" {
			authorTitle = m.titleOf(agName)
		}
		m.blocks = append(m.blocks, block{Kind: "tool", Author: authorTitle, Tool: ev.ToolName, Body: ev.ToolName, Collapsed: true})
	case "tool_result":
		agName := m.agentKeyFor(ev.Author, ev.Label)
		m.setAgent(agName, "pensando", "")
	case "handoff":
		lbl := ev.Label
		if lbl == "" {
			lbl = "Handoff"
		}
		m.blocks = append(m.blocks, block{Kind: "agent", Author: lbl, Body: ev.Text})
	case "model_route":
		if ev.Model != "" {
			if row := m.matchAgent(ev.Author, ev.Label); row != nil {
				row.Model = shortModel(ev.Model)
			}
		}
		if ev.Text != "" {
			m.blocks = append(m.blocks, block{Kind: "system", Author: "Jev", Body: ev.Text})
		}
	case "text":
		if !ev.Thought && ev.Text != "" {
			agName := m.agentKeyFor(ev.Author, ev.Label)
			m.setAgent(agName, "pensando", "")
			authorTitle := ev.Label
			if authorTitle == "" {
				authorTitle = m.titleOf(agName)
			}
			if ev.Partial {
				m.appendDeltaByTitle(authorTitle, ev.Text)
			} else {
				m.setOrAppendAgentBlock(authorTitle, ev.Text)
			}
		}
	}
	if msg.Done {
		if msg.SessionID != "" {
			m.sessionID = msg.SessionID
			m.persistedSession = true
		}
		if len(msg.Files) > 0 {
			m.files = msg.Files
		}
		m.running = false
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		if msg.Err != nil {
			if isCancel(msg.Err) {
				m.stopRun("cancelado")
				m.refresh()
				return m, nil
			}
			for i := range m.agents {
				if m.agents[i].Status == "pensando" || strings.HasPrefix(m.agents[i].Status, "usando") {
					m.agents[i].Status = "erro"
				}
			}
			m.status = "erro"
			m.addSystem("Erro na execução: " + msg.Err.Error())
			m.refresh()
			return m, nil
		}
		for i := range m.agents {
			if m.agents[i].Status == "pensando" || strings.HasPrefix(m.agents[i].Status, "usando") {
				m.agents[i].Status = "concluído"
				m.agents[i].Tool = ""
			}
		}
		m.status = "pronto"
		if msg.HasIntegration {
			m.modal = modalBring
		}
		m.refresh()
		return m, nil
	}
	m.refresh()
	if m.events != nil {
		return m, listen(m.events)
	}
	return m, nil
}

func (m *model) stopRun(status string) {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.running = false
	m.queue = nil
	m.status = status
	for i := range m.agents {
		if m.agents[i].Status == "pensando" || strings.HasPrefix(m.agents[i].Status, "usando") {
			m.agents[i].Status = status
		}
	}
}

func (m model) View() tea.View {
	th := m.theme()
	w, h := m.width, m.height
	if w < 20 || h < 8 {
		v := tea.NewView("HiveMind")
		v.AltScreen = true
		return v
	}
	headerH, inputH, statusH := 3, 5, 1
	midH := h - headerH - inputH - statusH
	if midH < 5 {
		midH = 5
	}
	sideW, fileW := 30, 28
	if w < 120 {
		fileW = 0
	}
	if w < 96 {
		sideW = 0
	}
	gaps := 0
	if sideW > 0 {
		gaps++
	}
	if fileW > 0 {
		gaps++
	}
	mainW := w - sideW - fileW - gaps
	if mainW < 20 {
		mainW = 20
	}
	header := m.panel(th, w, headerH, "", m.headerLine(w-4))
	var mid string
	mainPane := m.panel(th, mainW, midH, "Conversa", m.vp.View())
	if sideW > 0 && fileW > 0 {
		mid = lipgloss.JoinHorizontal(lipgloss.Top, m.panel(th, sideW, midH, "Time", m.agentPane(sideW-4, midH-3)), " ", mainPane, " ", m.panel(th, fileW, midH, "Arquivos", m.filePane(fileW-4)))
	} else if sideW > 0 {
		mid = lipgloss.JoinHorizontal(lipgloss.Top, m.panel(th, sideW, midH, "Time", m.agentPane(sideW-4, midH-3)), " ", mainPane)
	} else {
		mid = mainPane
	}
	input := m.panel(th, w, inputH, "", m.ta.View())
	status := m.statusLine(w)
	body := lipgloss.JoinVertical(lipgloss.Left, header, mid, input, status)
	if m.modal != modalNone {
		body = lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.modalView(th, w, h), lipgloss.WithWhitespaceChars(" "))
	} else {
		body = lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, body)
	}
	v := tea.NewView(body)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "HiveMind"
	if m.modal == modalNone {
		if c := m.ta.Cursor(); c != nil {
			c.Y += headerH + midH + 1
			c.X++
			v.Cursor = c
		}
	}
	return v
}

func (m model) theme() Theme { return themes()[m.themeIdx%len(themes())] }

func (m model) panel(th Theme, w, h int, title, content string) string {
	// border (2) + horizontal padding (2)
	innerW := w - 4
	innerH := h - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}
	var b strings.Builder
	if title != "" {
		b.WriteString(th.style(th.Accent).Bold(true).Render(trimWidth(title, innerW)))
		b.WriteByte('\n')
		innerH--
		if innerH < 1 {
			innerH = 1
		}
	}
	b.WriteString(fit(content, innerW, innerH))
	return th.box().Width(w).Height(h).Render(b.String())
}

func (m model) headerLine(w int) string {
	th := m.theme()
	left := th.style(th.Accent).Bold(true).Render("⬡ HiveMind")
	meta := fmt.Sprintf("  %s  ·  %s  ·  time %s", trimWidth(m.workspace, 36), m.branch, m.activeName())
	return trimWidth(left+th.style(th.Muted).Render(meta), w)
}

func (m model) agentPane(w, h int) string {
	th := m.theme()
	if len(m.agents) == 0 {
		return th.style(th.Muted).Render("sem time")
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	var b strings.Builder
	for _, a := range m.agents {
		mark := th.style(th.Muted).Render("○")
		switch {
		case a.Status == "pensando" || strings.HasPrefix(a.Status, "usando"):
			mark = th.style(th.Yellow).Render(frames[m.spin%len(frames)])
		case a.Status == "concluído":
			mark = th.style(th.Green).Render("●")
		case a.Status == "erro" || a.Status == "cancelado":
			mark = th.style(th.Red).Render("●")
		}
		b.WriteString(mark + " " + th.style(th.Text).Bold(true).Render(trimWidth(a.Title, max(1, w-4))) + "\n")
		b.WriteString("  " + th.style(th.Muted).Render(trimWidth(shortModel(a.Model), max(1, w-2))) + "\n")
		line := a.Status
		if a.Status == "pensando" || strings.HasPrefix(a.Status, "usando") {
			line += " " + time.Since(a.Start).Truncate(time.Second).String()
		}
		if a.Tokens > 0 {
			line += fmt.Sprintf(" %dtok", a.Tokens)
		}
		b.WriteString("  " + th.style(th.Yellow).Render(trimWidth(line, max(1, w-2))) + "\n")
	}
	return b.String()
}

func (m model) filePane(w int) string {
	th := m.theme()
	if len(m.files) == 0 {
		return th.style(th.Muted).Render("nenhuma edição")
	}
	names := make([]string, 0, len(m.files))
	for n := range m.files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		ch := m.files[n]
		b.WriteString(trimWidth(n, w) + "\n")
		b.WriteString(th.style(th.Green).Render(fmt.Sprintf("+%d", ch.Add)) + " " + th.style(th.Red).Render(fmt.Sprintf("-%d", ch.Del)) + "\n")
	}
	return b.String()
}

func (m model) statusLine(w int) string {
	th := m.theme()
	flags := "perm " + m.project.Settings.EffectivePermissionMode() + "  commit " + onOff(m.project.Settings.AutoCommit) + "  pr " + onOff(m.project.Settings.AutoPR)
	session := " · sessão " + m.sessionID
	if w < 120 {
		session = ""
	}
	s := fmt.Sprintf(" %s%s · %s · %d tok · %s · %s · %s",
		shortModel(m.activeModel()), session, time.Since(m.started).Truncate(time.Second), m.tokens, m.branch, flags, m.status)
	hint := "enter envia · /ajuda · esc cancela "
	gap := w - lipgloss.Width(s) - lipgloss.Width(hint)
	if gap < 1 {
		return th.style(th.Muted).Width(w).MaxHeight(1).Render(trimWidth(s, w))
	}
	return th.style(th.Muted).Width(w).MaxHeight(1).Render(s + strings.Repeat(" ", gap) + hint)
}

func (m *model) layout() {
	w, h := m.width, m.height
	headerH, inputH, statusH := 3, 5, 1
	midH := h - headerH - inputH - statusH
	sideW, fileW := 30, 28
	if w < 120 {
		fileW = 0
	}
	if w < 96 {
		sideW = 0
	}
	gaps := 0
	if sideW > 0 {
		gaps++
	}
	if fileW > 0 {
		gaps++
	}
	mainW := w - sideW - fileW - gaps
	if mainW < 10 {
		mainW = 10
	}
	m.vp.SetWidth(mainW - 4)
	m.vp.SetHeight(midH - 3)
	m.ta.SetWidth(w - 4)
	m.refresh()
}

func (m *model) refresh() {
	content := m.transcript()
	m.vp.SetContent(content)
	if m.follow {
		m.vp.GotoBottom()
	}
}

func (m model) transcript() string {
	th := m.theme()
	width := m.vp.Width()
	if width < 10 {
		width = 60
	}
	var b strings.Builder
	for _, blk := range m.blocks {
		switch blk.Kind {
		case "user":
			b.WriteString(th.style(th.Accent).Bold(true).Render("Você") + "\n")
			b.WriteString(renderMarkdown(blk.Body, width, th.Dark) + "\n\n")
		case "agent":
			b.WriteString(th.style(th.Pink).Bold(true).Render(blk.Author) + "\n")
			b.WriteString(renderMarkdown(blk.Body, width, th.Dark) + "\n\n")
		case "tool":
			mark := "▸ "
			if !blk.Collapsed {
				mark = "▾ "
			}
			b.WriteString(th.style(th.Yellow).Render(mark+blk.Tool+"  ·  "+blk.Author) + "\n")
			if !blk.Collapsed {
				b.WriteString(colorDiff(blk.Body, th) + "\n")
			}
			b.WriteByte('\n')
		default:
			b.WriteString(renderMarkdown(blk.Body, width, th.Dark) + "\n\n")
		}
	}
	if m.slashOpen() {
		b.WriteString(m.slashPopup())
	}
	if m.atOpen() {
		b.WriteString(m.atPopup())
	}
	return b.String()
}

func renderMarkdown(src string, width int, dark bool) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	style := "dark"
	if !dark {
		style = "light"
	}
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return src
	}
	out, err := r.Render(src)
	if err != nil {
		return src
	}
	return strings.TrimRight(out, "\n")
}

func colorDiff(s string, th Theme) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			b.WriteString(th.style(th.Green).Render(line) + "\n")
		case strings.HasPrefix(line, "-"):
			b.WriteString(th.style(th.Red).Render(line) + "\n")
		default:
			b.WriteString(th.style(th.Muted).Render(line) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) modalView(th Theme, w, h int) string {
	switch m.modal {
	case modalWizard:
		return m.wizardView(th, min(72, w-4))
	case modalModels, modalSessions, modalTeams, modalPermissions:
		title := map[modal]string{modalModels: "Modelo", modalSessions: "Sessões", modalTeams: "Times", modalPermissions: "Permissão e Autonomia"}[m.modal]
		return m.listModal(th, title, m.pickItems, m.pick, min(68, w-4))
	case modalApprove:
		return m.panel(th, min(70, w-4), 9, "Permissão Solicitada", m.pending+"\n\ns autorizar    n negar    esc cancela")
	case modalBring:
		return m.panel(th, min(68, w-4), 9, "Sua branch", "Trazer as mudanças da integração para "+m.branch+"?\nO checkout atual não foi alterado.\n\ns sim    n não")
	default:
		return ""
	}
}

func (m model) listModal(th Theme, title string, items []string, sel, w int) string {
	if len(items) == 0 {
		items = []string{"(vazio)"}
	}
	var b strings.Builder
	for i, it := range items {
		prefix := "  "
		if i == sel {
			prefix = "❯ "
		}
		line := prefix + it
		if i == sel {
			b.WriteString(th.style(th.Accent).Render(line) + "\n")
		} else {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString(th.style(th.Muted).Render("\nenter escolhe · esc fecha"))
	return m.panel(th, w, min(16, len(items)+6), title, b.String())
}

func fit(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = trimWidth(line, w)
	}
	return strings.Join(lines, "\n")
}

func trimWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w {
		r = r[:len(r)-1]
	}
	return string(r)
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func newSessionID() string {
	return time.Now().Format("150405")
}

func isCancel(err error) bool {
	return err == context.Canceled || strings.Contains(err.Error(), "context canceled")
}

func shortModel(s string) string {
	s = strings.TrimPrefix(s, "agy:")
	if i := strings.LastIndex(s, ":"); i > 0 {
		switch s[i+1:] {
		case "low", "medium", "high", "xhigh", "max":
			s = s[:i]
		}
	}
	if s == "" {
		return "agy"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func listAgyModels(ctx context.Context) ([]string, error) {
	models, err := modelx.ListAgyModels(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.Slug != "" {
			names = append(names, m.Slug)
		}
	}
	return names, nil
}

func (m *model) addSystem(text string) {
	m.blocks = append(m.blocks, block{Kind: "system", Body: text})
}

func (m *model) appendDeltaByTitle(title, text string) {
	if len(m.blocks) > 0 && m.blocks[len(m.blocks)-1].Kind == "agent" && m.blocks[len(m.blocks)-1].Author == title {
		m.blocks[len(m.blocks)-1].Body += text
		return
	}
	m.blocks = append(m.blocks, block{Kind: "agent", Author: title, Body: text})
}

func (m *model) setOrAppendAgentBlock(title, text string) {
	if len(m.blocks) > 0 && m.blocks[len(m.blocks)-1].Kind == "agent" && m.blocks[len(m.blocks)-1].Author == title {
		m.blocks[len(m.blocks)-1].Body = text
		return
	}
	m.blocks = append(m.blocks, block{Kind: "agent", Author: title, Body: text})
}

func (m *model) matchAgent(author, label string) *agentRow {
	key := m.agentKeyFor(author, label)
	return m.findAgent(key)
}

func (m model) agentKeyFor(author, label string) string {
	auth := strings.ToLower(author)
	lbl := strings.ToLower(label)
	for _, a := range m.agents {
		nm := strings.ToLower(a.Name)
		if auth == nm || strings.HasPrefix(auth, nm+"_") || strings.HasSuffix(auth, "_"+nm) || strings.Contains(lbl, nm) {
			return a.Name
		}
	}
	for _, a := range m.agents {
		rl := strings.ToLower(a.Role)
		if strings.Contains(auth, rl) || strings.Contains(lbl, rl) {
			return a.Name
		}
	}
	if len(m.agents) > 0 {
		return m.agents[0].Name
	}
	return author
}

func hasIntegrationAhead(workspaceDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) || !g.BranchExists(ctx, workspaceDir, gitx.IntegrationBranch) {
		return false
	}
	out, err := g.Run(ctx, workspaceDir, "rev-list", "--count", "HEAD.."+gitx.IntegrationBranch)
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n > 0
}

func collectDiffStats(workspaceDir string, againstIntegration bool) map[string]fileChange {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := gitx.Local{}
	outMap := map[string]fileChange{}
	if !g.IsRepo(ctx, workspaceDir) {
		return outMap
	}
	target := "HEAD"
	if againstIntegration {
		target = "HEAD.." + gitx.IntegrationBranch
	}
	raw, err := g.Run(ctx, workspaceDir, "diff", "--numstat", target)
	if err != nil {
		return outMap
	}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		outMap[fields[2]] = fileChange{Add: add, Del: del}
	}
	return outMap
}

func (m model) activeName() string {
	if !m.hasTeam {
		return "—"
	}
	return m.project.Active
}

func (m model) activeModel() string {
	if len(m.agents) == 0 {
		return "agy"
	}
	return m.agents[0].Model
}

func (m *model) rebuildAgents() {
	squad, err := m.project.ActiveTeam("")
	if err != nil {
		m.agents = nil
		return
	}
	m.agents = nil
	for _, a := range squad.Agents {
		m.agents = append(m.agents, agentRow{
			Name: a.Name, Role: a.Role, Title: roleTitle(a.Role, a.Name), Model: a.Model, Status: "aguardando",
		})
	}
}

func roleTitle(role, name string) string {
	switch team.NormalizeRole(role) {
	case team.RoleProjectManager:
		return "Gerente"
	case team.RoleTechLead:
		return "Líder"
	case team.RoleArchitect:
		return "Arquiteto"
	case team.RoleDeveloper:
		n := strings.TrimPrefix(name, "dev_")
		if n == name {
			n = name
		}
		return "Dev " + n
	case team.RoleQA:
		return "QA"
	default:
		return name
	}
}

func (m model) titleOf(name string) string {
	for _, a := range m.agents {
		if a.Name == name {
			return a.Title
		}
	}
	return name
}

func (m *model) setAgent(name, status, tool string) {
	for i := range m.agents {
		if m.agents[i].Name == name {
			if m.agents[i].Status != status {
				m.agents[i].Start = time.Now()
			}
			m.agents[i].Status = status
			m.agents[i].Tool = tool
		}
	}
}

func (m *model) findAgent(name string) *agentRow {
	for i := range m.agents {
		if m.agents[i].Name == name {
			return &m.agents[i]
		}
	}
	return nil
}

func (m model) slashOpen() bool {
	v := m.ta.Value()
	return strings.HasPrefix(v, "/") && !strings.Contains(v, " ") && m.modal == modalNone
}

func (m model) slashMatches() []cmdItem {
	q := strings.ToLower(strings.TrimPrefix(m.ta.Value(), "/"))
	var out []cmdItem
	for _, c := range slashCommands() {
		if q == "" || strings.Contains(strings.TrimPrefix(c.Name, "/"), q) {
			out = append(out, c)
		}
	}
	return out
}

func slashCommands() []cmdItem {
	return []cmdItem{
		{"/ajuda", "Lista os comandos e os atalhos"},
		{"/permissao", "Alterna entre governed, auto_edit e full_access"},
		{"/time", "Lista ou troca o time ativo"},
		{"/modelo", "Escolhe o modelo agy dos agentes"},
		{"/agentes", "Mostra papéis, modelos e estado"},
		{"/sessoes", "Lista sessões gravadas"},
		{"/jira", "Mostra a chave Jira configurada"},
		{"/git", "Mostra a branch e o status"},
		{"/pr", "Mostra se o pull request automático está ligado"},
		{"/limpar", "Limpa a conversa da tela"},
		{"/tema", "Alterna noite, oceano e âmbar"},
		{"/sair", "Fecha o HiveMind"},
	}
}

func (m model) slashPopup() string {
	th := m.theme()
	var b strings.Builder
	items := m.slashMatches()
	for i, c := range items {
		line := fmt.Sprintf("%-12s %s", c.Name, c.Desc)
		if i == m.slashSel {
			b.WriteString(th.style(th.Accent).Render("❯ "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (m *model) completeSlash() {
	items := m.slashMatches()
	if len(items) == 0 {
		return
	}
	if m.slashSel >= len(items) {
		m.slashSel = 0
	}
	m.ta.SetValue(items[m.slashSel].Name)
}

func (m model) runSlash(text string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return m, nil
	}
	switch fields[0] {
	case "/ajuda":
		var b strings.Builder
		b.WriteString("**Atalhos**\n\n")
		for _, c := range slashCommands() {
			b.WriteString("- `" + c.Name + "` — " + c.Desc + "\n")
		}
		b.WriteString("\nEnter envia. Shift+Enter ou Alt+Enter quebram a linha. PgUp/PgDn e a roda rolam a conversa. Ctrl+T dobra o último bloco de ferramenta. Esc cancela a execução. Ctrl+C duas vezes sai. `@arquivo` anexa um arquivo.\n")
		m.blocks = append(m.blocks, block{Kind: "agent", Author: "HiveMind", Body: b.String()})
	case "/sair":
		return m, tea.Quit
	case "/limpar":
		m.blocks = nil
		m.addSystem("Conversa limpa.")
	case "/tema":
		m.themeIdx = (m.themeIdx + 1) % len(themes())
		m.addSystem("Tema " + m.theme().Name + ".")
	case "/agentes":
		var b strings.Builder
		for _, a := range m.agents {
			fmt.Fprintf(&b, "- **%s** (`%s`) — %s — %s\n", a.Title, a.Model, a.Status, a.Role)
		}
		m.blocks = append(m.blocks, block{Kind: "agent", Author: "Time", Body: b.String()})
	case "/time":
		if len(fields) == 1 {
			m.pickItems = nil
			for _, tm := range m.project.Teams {
				mark := tm.Name
				if tm.Name == m.project.Active {
					mark = "* " + mark
				}
				m.pickItems = append(m.pickItems, mark)
			}
			m.pick = 0
			m.modal = modalTeams
		} else if m.switchTeam(fields[1]) {
			m.addSystem("Time ativo: " + fields[1])
		}
	case "/modelo":
		m.pickItems = m.wizModels
		if len(m.pickItems) == 0 {
			m.pickItems = []string{"agy"}
		}
		m.pick = 0
		m.modal = modalModels
	case "/permissao":
		if len(fields) > 1 {
			mode := team.NormalizePermissionMode(fields[1])
			m.project.Settings.PermissionMode = mode
			_ = team.Save(m.workspace, m.project)
			m.addSystem("Modo de permissão atualizado para **" + mode + "**.")
		} else {
			m.pickItems = []string{
				"governed — Governado (worktree + PR + aprovação p/ main)",
				"auto_edit — Edição e comandos locais ágeis",
				"full_access — Acesso e Permissão Total (estilo Claude Code / GPT)",
			}
			m.pick = 0
			m.modal = modalPermissions
		}
	case "/sessoes":
		m.pickItems = sessionNames(m.workspace)
		m.pick = 0
		m.modal = modalSessions
	case "/git":
		st, _ := gitx.Local{}.Status(context.Background(), m.workspace)
		if strings.TrimSpace(st) == "" {
			st = m.branch
		}
		m.addSystem(st)
	case "/pr":
		m.addSystem("Pull request automático: " + onOff(m.project.Settings.AutoPR) + ". Commit automático: " + onOff(m.project.Settings.AutoCommit) + ".")
	case "/jira":
		key := m.project.Settings.JiraProject
		if key == "" {
			key = "(nenhuma)"
		}
		m.addSystem("Jira: " + onOff(m.project.Settings.JiraEnabled) + " · projeto " + key)
	default:
		m.addSystem("Comando desconhecido. Use /ajuda.")
	}
	m.refresh()
	return m, nil
}

func (m model) atOpen() bool {
	v := m.ta.Value()
	i := strings.LastIndex(v, "@")
	if i < 0 || m.modal != modalNone {
		return false
	}
	tail := v[i+1:]
	return !strings.ContainsAny(tail, " \n\t")
}

func (m model) atPopup() string {
	files := m.matchFiles()
	var b strings.Builder
	for i, f := range files {
		if i == m.atSel {
			b.WriteString("❯ " + f + "\n")
		} else {
			b.WriteString("  " + f + "\n")
		}
		if i > 8 {
			break
		}
	}
	return b.String()
}

func (m *model) completeAt() {
	files := m.matchFiles()
	if len(files) == 0 {
		return
	}
	if m.atSel >= len(files) {
		m.atSel = 0
	}
	path := files[m.atSel]
	var snippet string
	if resolved, err := workspace.ResolveWithOptions(m.workspace, filepath.FromSlash(path), workspace.Options{AllowMemoryRead: true}); err != nil {
		snippet = err.Error()
	} else if f, openErr := os.Open(resolved); openErr != nil {
		snippet = openErr.Error()
	} else {
		data, rdErr := io.ReadAll(io.LimitReader(f, 4096))
		_ = f.Close()
		if rdErr != nil {
			snippet = rdErr.Error()
		} else {
			snippet = strx.TruncateBytes(string(data), 4000, "\n…")
		}
	}
	cur := m.ta.Value()
	if i := strings.LastIndex(cur, "@"); i >= 0 {
		cur = cur[:i]
	}
	m.ta.SetValue(strings.TrimRight(cur, " ") + "\n\n[arquivo " + path + "]\n" + snippet)
}

func (m model) matchFiles() []string {
	q := ""
	if i := strings.LastIndex(m.ta.Value(), "@"); i >= 0 {
		q = strings.ToLower(m.ta.Value()[i+1:])
	}
	var out []string
	_ = filepath.WalkDir(m.workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && (d.Name() == ".git" || d.Name() == ".hivemind" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(m.workspace, path)
		if err != nil {
			return nil
		}
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			out = append(out, filepath.ToSlash(rel))
		}
		if len(out) > 40 {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

func sessionNames(dir string) []string {
	store, err := sessionx.Open(dir)
	if err != nil {
		return []string{"(sem sessões)"}
	}
	metas, err := store.ListMeta()
	if err != nil || len(metas) == 0 {
		return []string{"(sem sessões)"}
	}
	var out []string
	for _, meta := range metas {
		out = append(out, meta.ID+"  "+trimWidth(meta.Objective, 40))
	}
	return out
}

func (m *model) switchTeam(name string) bool {
	if _, err := m.project.ActiveTeam(name); err != nil {
		m.addSystem("Time não encontrado: " + name)
		return false
	}
	m.project.Active = name
	_ = team.Save(m.workspace, m.project)
	m.rebuildAgents()
	return true
}
