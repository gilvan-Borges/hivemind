package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

const (
	handoffObjectiveMax = 4 << 10
	handoffFactMax      = 1500
	handoffFactsMax     = 32
	handoffDiffMax      = 2 << 10
)

// HandoffCheck is one named result carried to the next role.
type HandoffCheck struct {
	Name string `json:"name"`
	Pass bool   `json:"pass"`
}

// HandoffPackage is the only context the next role receives from the previous one.
type HandoffPackage struct {
	Schema       int            `json:"schema"`
	RunID        string         `json:"runId"`
	FromRole     string         `json:"fromRole"`
	ToRole       string         `json:"toRole"`
	Objective    string         `json:"objective"`
	Facts        []string       `json:"facts"`
	Files        []string       `json:"files"`
	Checks       []HandoffCheck `json:"checks"`
	DiffStat     string         `json:"diffStat"`
	DeliveryHash string         `json:"deliveryHash"`
}

type handoffEdge struct {
	From string
	To   string
	Hash string
}

func (r *Runner) sealHandoff(runID string) {
	if r == nil {
		return
	}
	r.devSlices = sanitizeSlices(r.devSlices)
	if strings.TrimSpace(r.Workspace) == "" {
		return
	}
	if r.chiefPlan == "" && r.chiefContextBrief == "" && len(r.devSlices) == 0 {
		return
	}
	pkg := r.makeHandoffPackage(runID, "chief", team.RoleDeveloper)
	r.chiefPlan = pkg.Objective
	r.chiefContextBrief = strings.Join(pkg.Facts, "\n")
	r.handoffHash = pkg.DeliveryHash
	_ = persistHandoff(r.Workspace, pkg)
}

func sanitizeSlices(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	for _, sl := range in {
		s := sanitizeHandoffText(sl)
		// Corte UTF-8 seguro: acentuação PT-BR nunca é fatiada no meio de um rune.
		s = strx.TruncateBytes(s, handoffFactMax, "")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (r *Runner) makeHandoffPackage(runID, from, to string) HandoffPackage {
	objective := sanitizeHandoffText(r.chiefPlan)
	if objective == "" {
		objective = sanitizeHandoffText(r.objectiveText)
	}
	// O objetivo acima de 4 KiB é cortado, não recusado (UTF-8 seguro, Spec 02 §3.3).
	objective = strx.TruncateBytes(objective, handoffObjectiveMax, "")
	var facts []string
	for _, src := range append([]string{r.chiefContextBrief}, r.devSlices...) {
		for _, line := range strings.Split(sanitizeHandoffText(src), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			line = strx.TruncateBytes(line, handoffFactMax, "")
			facts = append(facts, line)
			if len(facts) == handoffFactsMax {
				break
			}
		}
		if len(facts) == handoffFactsMax {
			break
		}
	}
	sum := sha256.Sum256([]byte(objective + "\n" + strings.Join(facts, "\n")))
	return HandoffPackage{
		Schema:       1,
		RunID:        safeRunID(runID),
		FromRole:     from,
		ToRole:       to,
		Objective:    objective,
		Facts:        facts,
		Files:        relativeOnly(facts),
		DeliveryHash: hex.EncodeToString(sum[:]),
	}
}

// ExternalHandoff builds the sanitized package used when the resident hands
// work to the squad. Shell lines are dropped and the objective is capped.
func ExternalHandoff(runID, objective string, facts []string) HandoffPackage {
	objective = sanitizeHandoffText(objective)
	objective = strx.TruncateBytes(objective, handoffObjectiveMax, "")
	var clean []string
	for _, fact := range facts {
		line := strings.TrimSpace(sanitizeHandoffText(fact))
		if line == "" {
			continue
		}
		line = strx.TruncateBytes(line, handoffFactMax, "")
		clean = append(clean, line)
		if len(clean) == handoffFactsMax {
			break
		}
	}
	sum := sha256.Sum256([]byte(objective + "\n" + strings.Join(clean, "\n")))
	return HandoffPackage{
		Schema:       1,
		RunID:        safeRunID(runID),
		FromRole:     "resident",
		ToRole:       team.RoleDeveloper,
		Objective:    objective,
		Facts:        clean,
		Files:        relativeOnly(clean),
		DeliveryHash: hex.EncodeToString(sum[:]),
	}
}

// DeliverExternalHandoff writes the sanitized package outside the chat transcript.
func DeliverExternalHandoff(workspaceRoot string, pkg HandoffPackage) error {
	return persistHandoff(workspaceRoot, pkg)
}

func sanitizeHandoffText(s string) string {
	s = resident.RedactSecrets(s)
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, "sh -c") || strings.Contains(low, "cmd /c") || strings.Contains(low, "powershell") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func relativeOnly(lines []string) []string {
	var out []string
	for _, line := range lines {
		if line == "" || filepath.IsAbs(line) || strings.Contains(line, "..") {
			continue
		}
		if strings.ContainsAny(line, " \t") {
			continue
		}
		out = append(out, filepath.ToSlash(line))
	}
	return out
}

func safeRunID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "sem-run"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "sem-run"
	}
	return b.String()
}

func (p HandoffPackage) render() string {
	var b strings.Builder
	b.WriteString("# Pacote de handoff\n\n")
	b.WriteString("- De: " + p.FromRole + "\n")
	b.WriteString("- Para: " + p.ToRole + "\n")
	b.WriteString("- Hash: " + p.DeliveryHash + "\n\n")
	b.WriteString("## Objetivo\n\n")
	b.WriteString(p.Objective)
	b.WriteString("\n\n## Fatos\n\n")
	for _, fact := range p.Facts {
		b.WriteString("- " + fact + "\n")
	}
	return b.String()
}

func persistHandoff(workspaceRoot string, pkg HandoffPackage) error {
	dir := filepath.Join(workspace.AppDataRoot(), "handoffs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	jsonPath := filepath.Join(dir, pkg.RunID+".json")
	if err := writePrivate(jsonPath, raw); err != nil {
		return err
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	mdDir := filepath.Join(workspaceRoot, ".hivemind", "handoff")
	if err := os.MkdirAll(mdDir, 0o700); err != nil {
		return err
	}
	body := []byte(pkg.render())
	if err := writePrivate(filepath.Join(mdDir, "latest.md"), body); err != nil {
		return err
	}
	return writePrivate(filepath.Join(mdDir, pkg.RunID+".md"), body)
}

func loadPersistedHandoff(runID string) (HandoffPackage, bool) {
	cleanID := safeRunID(runID)
	if cleanID == "" || cleanID == "sem-run" {
		return HandoffPackage{}, false
	}
	jsonPath := filepath.Join(workspace.AppDataRoot(), "handoffs", cleanID+".json")
	raw, err := os.ReadFile(jsonPath)
	if err != nil || len(raw) == 0 {
		return HandoffPackage{}, false
	}
	var pkg HandoffPackage
	if err := json.Unmarshal(raw, &pkg); err != nil || strings.TrimSpace(pkg.Objective) == "" {
		return HandoffPackage{}, false
	}
	return pkg, true
}

func writePrivate(path string, body []byte) error {
	// Escrita atômica (temp + Sync + Rename): um crash nunca deixa um pacote de
	// handoff meio escrito em .hivemind/handoff (W1-01 padrão, concluído aqui).
	if err := atomicx.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func acceptHandoff(edges *[]handoffEdge, from, to, hash string, qaIter, maxLoops int, stall bool) (bool, string) {
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.ToLower(strings.TrimSpace(to))
	if from == "" || to == "" {
		return false, "papel ausente"
	}
	if from == to {
		return false, "aresta para o mesmo papel"
	}
	if maxLoops <= 0 {
		maxLoops = 3
	}
	for _, e := range *edges {
		if e.Hash == hash && e.From == from && e.To == to {
			return true, ""
		}
	}
	visited := map[string]bool{}
	for _, e := range *edges {
		if e.Hash != hash {
			continue
		}
		visited[e.From] = true
		visited[e.To] = true
		if e.From == from && e.To != to {
			return false, "papel já entregou este hash"
		}
	}
	if visited[to] {
		// A volta Dev↔QA dentro do teto do loopagent continua permitida.
		// Qualquer outro papel já visitado, inclusive o mesmo papel, é recusado.
		loop := (from == team.RoleQA && to == team.RoleDeveloper) || (from == team.RoleDeveloper && to == team.RoleQA)
		if loop && qaIter < maxLoops && !stall {
			*edges = append(*edges, handoffEdge{From: from, To: to, Hash: hash})
			return true, ""
		}
		return false, "papel já visitado com este hash"
	}
	*edges = append(*edges, handoffEdge{From: from, To: to, Hash: hash})
	return true, ""
}

// offerHandoff records one edge. A refused edge returns a model response and does not call callModel.
func (r *Runner) offerHandoff(from, to, hash string, callModel func() *model.LLMResponse) *model.LLMResponse {
	r.handoffMu.Lock()
	defer r.handoffMu.Unlock()
	return r.offerHandoffLocked(from, to, hash, callModel)
}

func (r *Runner) offerHandoffLocked(from, to, hash string, callModel func() *model.LLMResponse) *model.LLMResponse {
	maxLoops := r.Project.Settings.MaxQALoops
	ok, reason := acceptHandoff(&r.handoffEdges, from, to, hash, r.qaLoopIter, maxLoops, r.qaStallFired)
	if !ok {
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "handoff_rejected", Text: reason, FromRole: from, ToRole: to})
		}
		return &model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: reason}}},
			TurnComplete: true,
		}
	}
	if strings.EqualFold(from, team.RoleQA) && strings.EqualFold(to, team.RoleDeveloper) {
		r.qaLoopIter++
	}
	r.handoffTo = strings.ToLower(to)
	r.handoffHash = hash
	if callModel != nil {
		return callModel()
	}
	return nil
}

func (r *Runner) handoffModelGuard(role string) llmagent.BeforeModelCallback {
	return func(agent.Context, *model.LLMRequest) (*model.LLMResponse, error) {
		r.handoffMu.Lock()
		defer r.handoffMu.Unlock()
		to := strings.ToLower(strings.TrimSpace(role))
		if to == "" || r.handoffTo == to {
			return nil, nil
		}
		from := r.handoffTo
		if from == "" {
			from = "chief"
		}
		hash := r.handoffHash
		if hash == "" {
			hash = "sem-entrega"
		}
		return r.offerHandoffLocked(from, to, hash, nil), nil
	}
}
