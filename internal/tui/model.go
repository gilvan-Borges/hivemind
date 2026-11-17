package tui

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type modal int

const (
	modalNone modal = iota
	modalWizard
	modalModels
	modalSessions
	modalTeams
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
	Agent string
	Event modelx.StreamEvent
	Err   error
	Done  bool
}

type model struct {
	ctx       context.Context
	workspace string
	themeIdx  int
	width     int
	height    int
	branch    string
	project   team.File
	hasTeam   bool
	sessionID string
	started   time.Time
	tokens    int

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

	modal      modal
	wizardStep int
	wizName    string
	wizDevs    string
	wizModels  []string
	wizModel   int
	wizEffort  int
	efforts    []string
	sameModel  bool
	wizWT      bool
	wizCommit  bool
	wizPR      bool
	wizGitHub  bool
	wizJira    bool
	wizJiraKey string
	pick       int
	pickItems  []string
	slashSel   int
	atSel      int
	atQuery    string
	pending    string
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
		wizName: "padrao", wizDevs: "1", wizWT: true, wizEffort: 1,
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
	cmds := []tea.Cmd{textarea.Blink, doTick()}
	if m.modal == modalWizard {
		cmds = append(cmds, loadAgyModels())
	}
	return tea.Batch(cmds...)
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
	m.running = true
	m.files = map[string]fileChange{}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.runCtx = ctx
	if isSmallTalk(text) {
		m.queue = []string{m.agentByRole(team.RoleProjectManager)}
		m.status = "triagem"
	} else {
		m.queue = m.pipeline()
		m.status = "time em execução"
	}
	return m.kick(text)
}

func (m model) kick(userText string) (model, tea.Cmd) {
	if len(m.queue) == 0 {
		m.running = false
		m.status = "pronto"
		if !isSmallTalk(userText) {
			m.modal = modalBring
		}
		m.refresh()
		return m, nil
	}
	name := m.queue[0]
	m.queue = m.queue[1:]
	m.setAgent(name, "pensando", "")
	m.blocks = append(m.blocks, block{Kind: "agent", Author: m.titleOf(name), Body: ""})
	m.refresh()
	ch := make(chan streamed, 32)
	prompt := m.agentPrompt(name, userText)
	go func() {
		defer close(ch)
		ag, err := m.agyFor(name)
		if err != nil {
			ch <- streamed{Agent: name, Err: err, Done: true}
			return
		}
		err = ag.Stream(m.runCtx, prompt, func(ev modelx.StreamEvent) {
			select {
			case ch <- streamed{Agent: name, Event: ev}:
			case <-m.runCtx.Done():
			}
		})
		ch <- streamed{Agent: name, Err: err, Done: true}
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
	if msg.Event.Type == "delta" {
		m.appendDelta(msg.Agent, msg.Event.Text)
		m.setAgent(msg.Agent, "pensando", "")
	}
	if msg.Event.Type == "tool" {
		m.setAgent(msg.Agent, "usando ferramenta "+msg.Event.ToolName, msg.Event.ToolName)
		body := strings.TrimSpace(msg.Event.ToolArgs + "\n" + msg.Event.ToolOut)
		m.blocks = append(m.blocks, block{Kind: "tool", Author: m.titleOf(msg.Agent), Tool: msg.Event.ToolName, Body: body})
		if isEditTool(msg.Event.ToolName) {
			m.noteFile(msg.Event.ToolArgs, msg.Event.ToolOut)
		}
		if needsApproval(msg.Event.ToolName) && !m.project.Settings.AutoPR && !m.project.Settings.AutoCommit {
			m.pending = msg.Event.ToolName
			m.modal = modalApprove
			m.refresh()
			return m, nil
		}
	}
	if msg.Event.Type == "result" {
		m.tokens += msg.Event.Tokens
		if row := m.findAgent(msg.Agent); row != nil {
			row.Tokens += msg.Event.Tokens
		}
		if msg.Event.Text != "" {
			m.appendDelta(msg.Agent, "")
			if last := m.lastAgentBlock(msg.Agent); last != nil && strings.TrimSpace(last.Body) == "" {
				last.Body = msg.Event.Text
			}
		}
	}
	if msg.Done {
		status := "concluído"
		if msg.Err != nil {
			if isCancel(msg.Err) {
				status = "cancelado"
				m.running = false
				m.queue = nil
				m.status = "cancelado"
				m.refresh()
				return m, nil
			}
			status = "erro"
			m.addSystem("Erro em " + m.titleOf(msg.Agent) + ": " + msg.Err.Error())
		}
		m.setAgent(msg.Agent, status, "")
		if m.running && len(m.queue) > 0 && m.runCtx != nil {
			return m.kick(m.lastUser())
		}
		m.running = false
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.status = "pronto"
		if status != "erro" && !isSmallTalk(m.lastUser()) && len(m.queue) == 0 {
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
	flags := "commit " + onOff(m.project.Settings.AutoCommit) + "  pr " + onOff(m.project.Settings.AutoPR)
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
	case modalModels, modalSessions, modalTeams:
		title := map[modal]string{modalModels: "Modelo", modalSessions: "Sessões", modalTeams: "Times"}[m.modal]
		return m.listModal(th, title, m.pickItems, m.pick, min(64, w-4))
	case modalApprove:
		return m.panel(th, min(64, w-4), 8, "Autorizar", "A ferramenta "+m.pending+" pede confirmação.\n\ns sim    n não    esc cancela")
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

func isEditTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "write") || strings.Contains(n, "edit") || strings.Contains(n, "diff")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func needsApproval(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "push") || strings.Contains(n, "pull_request") || strings.Contains(n, "create_pr")
}

func isSmallTalk(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.Trim(t, "!.?, ")
	switch t {
	case "ola", "olá", "oi", "oie", "hello", "hi", "hey", "bom dia", "boa tarde", "boa noite", "obrigado", "obrigada", "valeu", "tudo bem", "tudo bom":
		return true
	}
	if strings.HasSuffix(t, "?") && !hasChangeVerb(t) && len([]rune(t)) < 80 {
		return true
	}
	return false
}

func hasChangeVerb(s string) bool {
	for _, w := range []string{"adicione", "adicionar", "crie", "criar", "corrija", "implement", "função", "funcao", "teste", "arquivo", "refactor", "mude", "altere"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func listAgyModels(ctx context.Context) ([]string, error) {
	bin, err := modelx.LookAgy()
	if err != nil {
		return nil, err
	}
	cmd := execCommand(ctx, bin, "models")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		names = append(names, fields[0])
	}
	return names, nil
}

func (m *model) addSystem(text string) {
	m.blocks = append(m.blocks, block{Kind: "system", Body: text})
}

func (m *model) appendDelta(agent, text string) {
	blk := m.lastAgentBlock(agent)
	if blk == nil {
		m.blocks = append(m.blocks, block{Kind: "agent", Author: m.titleOf(agent), Body: text})
		return
	}
	blk.Body += text
}

func (m *model) lastAgentBlock(agent string) *block {
	title := m.titleOf(agent)
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "agent" && m.blocks[i].Author == title {
			return &m.blocks[i]
		}
	}
	return nil
}

func (m *model) lastUser() string {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "user" {
			return m.blocks[i].Body
		}
	}
	return ""
}

func (m *model) noteFile(args, output string) {
	path := ""
	for _, key := range []string{`"path":"`, `"path": "`} {
		if i := strings.Index(args, key); i >= 0 {
			rest := args[i+len(key):]
			if j := strings.IndexAny(rest, `"`); j >= 0 {
				path = rest[:j]
			}
		}
	}
	if path == "" {
		return
	}
	add, del := 0, 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			add++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			del++
		}
	}
	if add == 0 && del == 0 {
		add = 1
	}
	cur := m.files[path]
	cur.Add += add
	cur.Del += del
	m.files[path] = cur
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

func (m model) agentByRole(role string) string {
	for _, a := range m.agents {
		if team.NormalizeRole(a.Role) == role {
			return a.Name
		}
	}
	if len(m.agents) > 0 {
		return m.agents[0].Name
	}
	return "pm"
}

func (m model) pipeline() []string {
	var out []string
	for _, role := range []string{team.RoleProjectManager, team.RoleTechLead, team.RoleArchitect, team.RoleDeveloper, team.RoleQA} {
		for _, a := range m.agents {
			if team.NormalizeRole(a.Role) == role {
				out = append(out, a.Name)
			}
		}
	}
	return out
}

func (m model) agyFor(name string) (*modelx.Agy, error) {
	modelName := "agy"
	for _, a := range m.agents {
		if a.Name == name {
			modelName = a.Model
		}
	}
	return modelx.NewAgy("", modelName)
}

func (m model) agentPrompt(name, userText string) string {
	return "Papel: " + m.titleOf(name) + ".\nResponda em português.\nNão use ferramentas internas do Antigravity; descreva a ação no texto e, se for editar arquivo, o HiveMind mostra o bloco.\n\nPedido do usuário:\n" + userText
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
	data, err := os.ReadFile(filepath.Join(m.workspace, path))
	snippet := string(data)
	if err != nil {
		snippet = err.Error()
	}
	if len(snippet) > 4000 {
		snippet = snippet[:4000] + "\n…"
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
