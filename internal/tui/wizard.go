package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func execCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, bin, args...)
}

func (m model) onModalKey(k string) (tea.Model, tea.Cmd) {
	switch m.modal {
	case modalWizard:
		return m.onWizard(k)
	case modalApprove:
		if k == "s" || k == "S" {
			m.addSystem("Autorizado: " + m.pending + ". O checkout não foi trocado.")
			m.modal = modalNone
		}
		if k == "n" || k == "N" || k == "esc" {
			m.addSystem("Recusado: " + m.pending + ".")
			m.modal = modalNone
		}
	case modalBring:
		if k == "s" || k == "S" {
			_, err := gitx.Local{}.Run(context.Background(), m.workspace, "merge", "--no-edit", "hivemind/integracao")
			if err != nil {
				m.addSystem("Não foi possível trazer as mudanças: " + err.Error())
			} else {
				m.addSystem("Mudanças trazidas para " + m.branch + ". Nenhum checkout silencioso.")
			}
			m.modal = modalNone
		}
		if k == "n" || k == "N" || k == "esc" {
			m.addSystem("Checkout mantido em " + m.branch + ".")
			m.modal = modalNone
		}
	case modalTeams:
		m.movePick(k)
		if k == "enter" && len(m.pickItems) > 0 {
			name := strings.TrimSpace(strings.TrimPrefix(m.pickItems[m.pick], "*"))
			if m.switchTeam(name) {
				m.addSystem("Time ativo: " + name)
			}
			m.modal = modalNone
		}
	case modalModels:
		m.movePick(k)
		if k == "s" || k == "S" || k == "enter" {
			if len(m.pickItems) > 0 {
				m.applyModel(m.pickItems[m.pick])
			}
			m.modal = modalNone
		}
	case modalSessions:
		m.movePick(k)
		if k == "enter" {
			m.modal = modalNone
		}
	}
	if k == "esc" && m.modal != modalWizard {
		m.modal = modalNone
	}
	m.refresh()
	return m, nil
}

func (m *model) movePick(k string) {
	switch k {
	case "up":
		if m.pick > 0 {
			m.pick--
		}
	case "down":
		if m.pick+1 < len(m.pickItems) {
			m.pick++
		}
	case "esc":
		m.modal = modalNone
	}
}

func (m model) onWizard(k string) (tea.Model, tea.Cmd) {
	switch m.wizardStep {
	case 0:
		m.editLine(&m.wizName, k)
		if k == "enter" && strings.TrimSpace(m.wizName) != "" {
			m.wizardStep = 1
		}
	case 1:
		m.editLine(&m.wizDevs, k)
		if k == "enter" {
			if _, err := strconv.Atoi(strings.TrimSpace(m.wizDevs)); err == nil {
				m.wizardStep = 2
			}
		}
	case 2:
		if k == "up" && m.wizModel > 0 {
			m.wizModel--
		}
		if k == "down" && m.wizModel+1 < len(m.wizModels) {
			m.wizModel++
		}
		if k == "s" || k == "S" || k == "enter" {
			m.sameModel = true
			m.wizardStep = 3
		}
	case 3:
		if (k == "left" || k == "up") && m.wizEffort > 0 {
			m.wizEffort--
		}
		if (k == "right" || k == "down") && m.wizEffort+1 < len(m.efforts) {
			m.wizEffort++
		}
		if k == "enter" {
			m.wizardStep = 4
		}
	case 4:
		switch strings.ToLower(k) {
		case "w":
			m.wizWT = !m.wizWT
		case "c":
			m.wizCommit = !m.wizCommit
		case "p":
			m.wizPR = !m.wizPR
		case "g":
			m.wizGitHub = !m.wizGitHub
		case "j":
			m.wizJira = !m.wizJira
		case "enter":
			if err := m.finishWizard(); err != nil {
				m.addSystem("Não consegui salvar o time: " + err.Error())
			} else {
				m.modal = modalNone
				m.hasTeam = true
				m.rebuildAgents()
				m.addSystem("Time **" + m.project.Active + "** criado. Pode pedir uma mudança ou dizer olá.")
			}
		}
	}
	m.refresh()
	return m, nil
}

func (m *model) editLine(field *string, k string) {
	switch k {
	case "backspace":
		r := []rune(*field)
		if len(r) > 0 {
			*field = string(r[:len(r)-1])
		}
	default:
		if len([]rune(k)) == 1 && k != "enter" && k != "esc" {
			*field += k
		}
	}
}

func (m model) wizardView(th Theme, w int) string {
	modelName := "agy"
	if len(m.wizModels) > 0 {
		modelName = m.wizModels[m.wizModel%len(m.wizModels)]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Nome do time: %s\n", markStep(m.wizardStep, 0, m.wizName))
	fmt.Fprintf(&b, "Desenvolvedores: %s\n", markStep(m.wizardStep, 1, m.wizDevs))
	b.WriteString("Modelo por agente (" + modelName + ")")
	if m.sameModel {
		b.WriteString(" (mesmo para todos)")
	}
	b.WriteString(":\n")
	if len(m.wizModels) == 0 {
		b.WriteString("  carregando `agy models`…\n")
	}
	for i, name := range m.wizModels {
		prefix := "  "
		if i == m.wizModel {
			prefix = "❯ "
		}
		if m.wizardStep == 2 && i == m.wizModel {
			b.WriteString(th.style(th.Accent).Render(prefix+name) + "\n")
		} else {
			b.WriteString(prefix + name + "\n")
		}
		if i > 6 {
			b.WriteString("  …\n")
			break
		}
	}
	fmt.Fprintf(&b, "%sEsforço: %s\n", markStep(m.wizardStep, 3, ""), m.efforts[m.wizEffort])
	if m.wizardStep == 4 {
		b.WriteString("❯ Integrações\n")
	}
	fmt.Fprintf(&b, "Worktree por dev %s   auto-commit %s   auto-PR %s\n", tick(m.wizWT), tick(m.wizCommit), tick(m.wizPR))
	fmt.Fprintf(&b, "GitHub %s   Jira %s\n", tick(m.wizGitHub), tick(m.wizJira))
	b.WriteString(th.style(th.Muted).Render("\nEnter continua · s mesmo modelo para todos · w/c/p/g/j alternam"))
	return m.panel(th, w, min(20, 18), "Novo time", b.String())
}

func markStep(cur, step int, value string) string {
	if cur == step {
		return "❯ " + value
	}
	return value
}

func tick(v bool) string {
	if v {
		return "[x]"
	}
	return "[ ]"
}

func (m *model) finishWizard() error {
	n, err := strconv.Atoi(strings.TrimSpace(m.wizDevs))
	if err != nil || n < 1 {
		n = 1
	}
	slug := "agy"
	if len(m.wizModels) > 0 {
		slug = m.wizModels[m.wizModel%len(m.wizModels)]
	}
	effort := m.efforts[m.wizEffort]
	spec := slug
	if slug != "agy" {
		spec = "agy:" + slug + ":" + effort
	}
	created := team.DefaultTeam(strings.TrimSpace(m.wizName), "", spec, "agy", n)
	f := team.File{Settings: team.Settings{
		JiraEnabled: m.wizJira,
		AutoCommit:  m.wizCommit,
		AutoPR:      m.wizPR,
		MaxQALoops:  3,
		BaseBranch:  "main",
		JiraProject: strings.TrimSpace(m.wizJiraKey),
	}}
	wt := m.wizWT
	f.Settings.Worktrees = &wt
	team.UpsertTeam(&f, created)
	if err := team.Save(m.workspace, f); err != nil {
		return err
	}
	_ = gitx.EnsureHiveIgnore(m.workspace)
	m.project = f
	return nil
}

func (m *model) applyModel(spec string) {
	if !strings.HasPrefix(spec, "agy:") && spec != "agy" {
		spec = "agy:" + spec
	}
	for i := range m.project.Teams {
		if m.project.Teams[i].Name != m.project.Active {
			continue
		}
		for j := range m.project.Teams[i].Agents {
			m.project.Teams[i].Agents[j].Provider = "agy"
			m.project.Teams[i].Agents[j].Model = spec
		}
	}
	_ = team.Save(m.workspace, m.project)
	m.rebuildAgents()
	m.addSystem("Modelo atualizado para " + spec + " em todos os agentes.")
}
