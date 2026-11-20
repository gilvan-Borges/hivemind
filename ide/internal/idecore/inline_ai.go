package idecore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// InlineAIStatus reports whether fast HTTP ghost-text completions and Ctrl+K inline edits
// are available for a workspace, and which provider/model will be used.
type InlineAIStatus struct {
	GhostAvailable bool   `json:"ghostAvailable"`
	GhostProvider  string `json:"ghostProvider,omitempty"`
	GhostModel     string `json:"ghostModel,omitempty"`
	GhostReason    string `json:"ghostReason,omitempty"`
	EditAvailable  bool   `json:"editAvailable"`
	EditProvider   string `json:"editProvider,omitempty"`
	EditModel      string `json:"editModel,omitempty"`
}

// InlineCompleteRequest carries editor cursor context for Fill-In-The-Middle ghost text.
type InlineCompleteRequest struct {
	Workspace string `json:"workspace"`
	FilePath  string `json:"filePath"`
	Prefix    string `json:"prefix"`
	Suffix    string `json:"suffix"`
	Model     string `json:"model,omitempty"`
}

// InlineCompleteResponse returns a cleaned inline suggestion for the current cursor.
type InlineCompleteResponse struct {
	Completion   string `json:"completion"`
	Model        string `json:"model,omitempty"`
	Provider     string `json:"provider,omitempty"`
	InputTokens  int32  `json:"inputTokens,omitempty"`
	OutputTokens int32  `json:"outputTokens,omitempty"`
}

// InlineEditRequest carries the selected code range and user instruction for Ctrl+K edit.
type InlineEditRequest struct {
	Workspace     string `json:"workspace"`
	FilePath      string `json:"filePath"`
	Instruction   string `json:"instruction"`
	SelectedText  string `json:"selectedText"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	BeforeContext string `json:"beforeContext"`
	AfterContext  string `json:"afterContext"`
	Model         string `json:"model,omitempty"`
}

// InlineEditResponse returns the replacement text and computed line diff preview.
type InlineEditResponse struct {
	Replacement  string     `json:"replacement"`
	DiffLines    []DiffLine `json:"diffLines"`
	Added        int        `json:"added"`
	Removed      int        `json:"removed"`
	Model        string     `json:"model,omitempty"`
	Provider     string     `json:"provider,omitempty"`
	InputTokens  int32      `json:"inputTokens,omitempty"`
	OutputTokens int32      `json:"outputTokens,omitempty"`
}

// inlineModelFactory allows deterministic unit tests without live HTTP calls.
var inlineModelFactory = modelx.New

func isHTTPInlineProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "opencode", "opencode-go", "openai", "openai-responses", "anthropic", "gemini", "vertex":
		return true
	default:
		return false
	}
}

func hasHTTPProviderCredentials(workspaceDir, provider string) bool {
	ctx := modelx.WithWorkspace(context.Background(), workspaceDir)
	if modelx.CredentialIsolated(ctx, provider) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "opencode", "opencode-go":
		if modelx.ResolveEnv(ctx, "opencode", "OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY") != "" {
			return true
		}
		if prof, mode, _ := modelx.ResolveProfile(ctx, "opencode"); prof != nil && (len(prof.OrgPatterns) > 0 || mode == "auto_org" || mode == "manual" || mode == "override") {
			return false
		}
		key, _, _ := modelx.LoadOpenCodeCredentials()
		return strings.TrimSpace(key) != ""
	case "openai", "openai-responses":
		return modelx.ResolveEnv(ctx, "openai", "OPENAI_API_KEY", "OPENAI_BASE_URL") != ""
	case "anthropic":
		return modelx.ResolveEnv(ctx, "anthropic", "ANTHROPIC_API_KEY") != ""
	case "gemini":
		return modelx.ResolveEnv(ctx, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY") != ""
	case "vertex":
		return modelx.ResolveEnv(ctx, "gemini", "GOOGLE_CLOUD_PROJECT") != ""
	default:
		return false
	}
}

// ResolveInlineModel picks the best provider and model for inline AI tasks.
// When requireHTTP is true (Ghost Text), subprocess providers such as agy are skipped
// because they do not support low-latency completions; when false (Ctrl+K edit),
// any configured model (including agy) can be used as fallback.
func ResolveInlineModel(workspaceDir, requestedModel string, requireHTTP bool) (provider, modelSpec, reason string) {
	if spec := strings.TrimSpace(requestedModel); spec != "" {
		prov := team.NormalizeProvider("", spec)
		if requireHTTP && !isHTTPInlineProvider(prov) {
			return "", "", fmt.Sprintf("Ghost text requer provider HTTP (OpenCode, OpenAI, Anthropic ou Gemini); modelo %q usa subprocesso %s.", spec, prov)
		}
		return prov, spec, ""
	}

	var cfg team.File
	hasCfg := false
	if strings.TrimSpace(workspaceDir) != "" {
		if loaded, err := team.Load(workspaceDir); err == nil {
			cfg = loaded
			hasCfg = true
		}
	}

	if hasCfg {
		if spec := strings.TrimSpace(cfg.Settings.InlineModel); spec != "" {
			prov := team.NormalizeProvider("", spec)
			if !requireHTTP || isHTTPInlineProvider(prov) {
				return prov, spec, ""
			}
		}
		if active, err := cfg.ActiveTeam(""); err == nil {
			// Prefer HTTP model configured on developer or chief agents.
			for _, role := range []string{team.RoleDeveloper, team.RoleArchitect, team.RoleProjectManager} {
				for _, ag := range active.ByRole(role) {
					prov := team.NormalizeProvider(ag.Provider, ag.Model)
					if isHTTPInlineProvider(prov) && strings.TrimSpace(ag.Model) != "" && hasHTTPProviderCredentials(workspaceDir, prov) {
						return prov, strings.TrimSpace(ag.Model), ""
					}
				}
			}
		}
	}

	// Fallback to any configured HTTP credentials in the user environment/store.
	// Org-scoped providers that do not match this workspace stay unavailable.
	ctx := modelx.WithWorkspace(context.Background(), workspaceDir)
	if !modelx.CredentialIsolated(ctx, "opencode") {
		if key, _, _ := modelx.LoadOpenCodeCredentials(); strings.TrimSpace(key) != "" {
			if prof, mode, _ := modelx.ResolveProfile(ctx, "opencode"); prof == nil || (len(prof.OrgPatterns) == 0 && mode != "auto_org" && mode != "manual" && mode != "override") {
				return "opencode", "opencode-go:glm-5.3-flash", ""
			}
		}
	}
	if modelx.ResolveEnv(ctx, "openai", "OPENAI_API_KEY") != "" {
		return "openai", "gpt-4o-mini", ""
	}
	if modelx.ResolveEnv(ctx, "anthropic", "ANTHROPIC_API_KEY") != "" {
		return "anthropic", "claude-3-5-haiku-latest", ""
	}
	if modelx.ResolveEnv(ctx, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY") != "" {
		return "gemini", "gemini-2.5-flash", ""
	}

	if requireHTTP {
		return "", "", "Ghost text requer um provider HTTP configurado (ex.: Token OpenCode Zen, OPENAI_API_KEY, ANTHROPIC_API_KEY ou GEMINI_API_KEY)."
	}

	// Ctrl+K Inline Edit fallback: reuse configured developer or chief model (even agy).
	if hasCfg {
		if active, err := cfg.ActiveTeam(""); err == nil {
			for _, role := range []string{team.RoleDeveloper, team.RoleArchitect, team.RoleProjectManager} {
				if list := active.ByRole(role); len(list) > 0 && strings.TrimSpace(list[0].Model) != "" {
					ag := list[0]
					return team.NormalizeProvider(ag.Provider, ag.Model), strings.TrimSpace(ag.Model), ""
				}
			}
		}
	}
	prov, mod := modelx.DefaultProviderModel()
	return prov, mod, ""
}

// GetInlineAIStatus returns availability and resolved models for Ghost Text and Ctrl+K.
func GetInlineAIStatus(workspaceDir string) InlineAIStatus {
	gp, gm, gr := ResolveInlineModel(workspaceDir, "", true)
	ep, em, _ := ResolveInlineModel(workspaceDir, "", false)
	return InlineAIStatus{
		GhostAvailable: gp != "" && gm != "",
		GhostProvider:  gp,
		GhostModel:     gm,
		GhostReason:    gr,
		EditAvailable:  ep != "" && em != "",
		EditProvider:   ep,
		EditModel:      em,
	}
}

// CompleteInlineCode generates a short, single-shot Fill-In-The-Middle ghost text completion.
func CompleteInlineCode(ctx context.Context, req InlineCompleteRequest) (InlineCompleteResponse, error) {
	prov, modSpec, reason := ResolveInlineModel(req.Workspace, req.Model, true)
	if prov == "" || modSpec == "" {
		return InlineCompleteResponse{}, fmt.Errorf("%s", reason)
	}

	prefix := trimTailBytes(req.Prefix, 4000)
	suffix := strx.TruncateBytes(req.Suffix, 1500, "")
	if strings.TrimSpace(prefix) == "" && strings.TrimSpace(suffix) == "" {
		return InlineCompleteResponse{}, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	runCtx = modelx.WithWorkspace(runCtx, req.Workspace)
	runCtx = modelx.WithSessionID(runCtx, "inline-ghost-"+filepath.Base(req.FilePath))

	llm, err := inlineModelFactory(runCtx, prov, modSpec)
	if err != nil {
		return InlineCompleteResponse{}, err
	}

	temp := float32(0.15)
	sysText := "Você é um motor de autocompletar código inline (FIM). Retorne APENAS o trecho exato de código que completa a posição <CURSOR>, sem markdown, sem cercas ``` e sem explicações. Se nada fizer sentido completar, retorne vazio. Mantenha no máximo 4 linhas concisas e respeite a indentação existente."
	userPrompt := fmt.Sprintf("Arquivo: %s\nComplete exatamente no marcador <CURSOR>:\n%s<CURSOR>%s", req.FilePath, prefix, suffix)

	raw, inTok, outTok, err := generateSingleText(runCtx, llm, modSpec, sysText, userPrompt, 160, &temp)
	if err != nil {
		return InlineCompleteResponse{}, err
	}
	if inTok > 0 || outTok > 0 {
		modelx.RecordAIUsage(prov, modSpec, int64(inTok), int64(outTok), int64(inTok+outTok))
	}

	cleaned := cleanInlineCompletion(raw, prefix, suffix)
	return InlineCompleteResponse{
		Completion:   cleaned,
		Model:        modSpec,
		Provider:     prov,
		InputTokens:  inTok,
		OutputTokens: outTok,
	}, nil
}

// GenerateInlineEdit generates a surgical code replacement for Ctrl+K inline editing
// and computes a unified-style line diff preview between SelectedText and Replacement.
func GenerateInlineEdit(ctx context.Context, req InlineEditRequest) (InlineEditResponse, error) {
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		return InlineEditResponse{}, fmt.Errorf("descreva a alteração desejada para o Ctrl+K")
	}
	prov, modSpec, reason := ResolveInlineModel(req.Workspace, req.Model, false)
	if prov == "" || modSpec == "" {
		return InlineEditResponse{}, fmt.Errorf("%s", reason)
	}

	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	runCtx = modelx.WithWorkspace(runCtx, req.Workspace)
	runCtx = modelx.WithSessionID(runCtx, "inline-edit-"+filepath.Base(req.FilePath))

	llm, err := inlineModelFactory(runCtx, prov, modSpec)
	if err != nil {
		return InlineEditResponse{}, err
	}

	before := trimTailBytes(req.BeforeContext, 3500)
	after := strx.TruncateBytes(req.AfterContext, 2000, "")
	temp := float32(0.1)

	sysText := "Você é um assistente de edição inline de código (Ctrl+K). Retorne APENAS o código substituto exato para o bloco selecionado (ou o código a inserir quando a seleção estiver vazia). NÃO inclua explicações, NÃO inclua markdown nem cercas ``` ao redor, e preserve rigorosamente a indentação e o estilo do arquivo."
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Arquivo: %s (linhas %d-%d)\n", req.FilePath, req.StartLine, req.EndLine)
	fmt.Fprintf(&prompt, "Instrução: %s\n\n", instruction)
	if before != "" {
		fmt.Fprintf(&prompt, "[Contexto antes]\n%s\n\n", before)
	}
	if req.SelectedText != "" {
		fmt.Fprintf(&prompt, "[Código selecionado a substituir]\n%s\n\n", req.SelectedText)
	} else {
		prompt.WriteString("[Código selecionado a substituir]\n(seleção vazia — gere o código a inserir exatamente nesta linha)\n\n")
	}
	if after != "" {
		fmt.Fprintf(&prompt, "[Contexto depois]\n%s\n", after)
	}

	raw, inTok, outTok, err := generateSingleText(runCtx, llm, modSpec, sysText, prompt.String(), 2048, &temp)
	if err != nil {
		return InlineEditResponse{}, err
	}
	if inTok > 0 || outTok > 0 {
		modelx.RecordAIUsage(prov, modSpec, int64(inTok), int64(outTok), int64(inTok+outTok))
	}

	replacement := stripOuterCodeFence(raw)
	diffLines, added, removed := ComputeLineDiff(req.SelectedText, replacement)
	return InlineEditResponse{
		Replacement:  replacement,
		DiffLines:    diffLines,
		Added:        added,
		Removed:      removed,
		Model:        modSpec,
		Provider:     prov,
		InputTokens:  inTok,
		OutputTokens: outTok,
	}, nil
}

func generateSingleText(ctx context.Context, llm model.LLM, modelName, systemText, userText string, maxTokens int32, temp *float32) (text string, inTok, outTok int32, err error) {
	req := &model.LLMRequest{
		Model: modelName,
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{{Text: systemText}},
			},
			MaxOutputTokens: maxTokens,
			Temperature:     temp,
		},
		Contents: []*genai.Content{
			{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{{Text: userText}},
			},
		},
	}

	var b strings.Builder
	for resp, iterErr := range llm.GenerateContent(ctx, req, false) {
		if iterErr != nil {
			return "", inTok, outTok, iterErr
		}
		if resp == nil {
			continue
		}
		if resp.UsageMetadata != nil {
			if resp.UsageMetadata.PromptTokenCount > inTok {
				inTok = resp.UsageMetadata.PromptTokenCount
			}
			if resp.UsageMetadata.CandidatesTokenCount > outTok {
				outTok = resp.UsageMetadata.CandidatesTokenCount
			}
		}
		if resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part != nil && !part.Thought && part.Text != "" {
					b.WriteString(part.Text)
				}
			}
		}
	}
	return b.String(), inTok, outTok, nil
}

// stripOuterCodeFence removes a single wrapping markdown ```lang ... ``` fence if present.
func stripOuterCodeFence(raw string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return strings.TrimRight(s, "\n")
	}
	nl := strings.IndexByte(trimmed, '\n')
	if nl < 0 {
		return ""
	}
	inner := trimmed[nl+1:]
	if idx := strings.LastIndex(inner, "```"); idx >= 0 {
		inner = inner[:idx]
	}
	return strings.TrimRight(inner, "\n")
}

// cleanInlineCompletion normalizes ghost text output: removes accidental markdown fences,
// trims repeated prefix/suffix overlap, and caps line count.
func cleanInlineCompletion(raw, prefix, suffix string) string {
	s := stripOuterCodeFence(raw)
	s = strings.ReplaceAll(s, "<CURSOR>", "")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	// If the model echoed the current line's prefix before completing, strip that echo.
	lastLine := prefix
	if nl := strings.LastIndexByte(prefix, '\n'); nl >= 0 {
		lastLine = prefix[nl+1:]
	}
	if len(lastLine) >= 2 && strings.HasPrefix(s, lastLine) {
		s = s[len(lastLine):]
	}
	// If the completion ends by repeating the start of suffix, trim the overlap.
	firstSuffix := suffix
	if nl := strings.IndexByte(suffix, '\n'); nl >= 0 {
		firstSuffix = suffix[:nl]
	}
	firstSuffix = strings.TrimSpace(firstSuffix)
	if len(firstSuffix) >= 1 && strings.HasSuffix(strings.TrimRight(s, " \t"), firstSuffix) {
		idx := strings.LastIndex(s, firstSuffix)
		if idx >= 0 {
			s = s[:idx]
		}
	}
	// Cap to at most 6 lines for clean inline ghost rendering.
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	s = strings.Join(lines, "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

func trimTailBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	// Advance to next UTF-8 rune boundary.
	for start < len(s) && (s[start]&0xC0) == 0x80 {
		start++
	}
	return s[start:]
}

// ComputeLineDiff computes a clean line-level diff (LCS) between oldText and newText.
func ComputeLineDiff(oldText, newText string) (lines []DiffLine, added, removed int) {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		norm := strings.ReplaceAll(s, "\r\n", "\n")
		return strings.Split(norm, "\n")
	}
	a := split(oldText)
	b := split(newText)

	// Guard against huge selections: fall back to block replace when matrix would be large.
	if len(a)*len(b) > 250000 {
		for _, ln := range a {
			lines = append(lines, DiffLine{Kind: DiffRemoved, Text: "-" + ln})
			removed++
		}
		for _, ln := range b {
			lines = append(lines, DiffLine{Kind: DiffAdded, Text: "+" + ln})
			added++
		}
		return lines, added, removed
	}

	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	i, j := 0, 0
	for i < m && j < n {
		if a[i] == b[j] {
			lines = append(lines, DiffLine{Kind: DiffContext, Text: " " + a[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			lines = append(lines, DiffLine{Kind: DiffRemoved, Text: "-" + a[i]})
			removed++
			i++
		} else {
			lines = append(lines, DiffLine{Kind: DiffAdded, Text: "+" + b[j]})
			added++
			j++
		}
	}
	for i < m {
		lines = append(lines, DiffLine{Kind: DiffRemoved, Text: "-" + a[i]})
		removed++
		i++
	}
	for j < n {
		lines = append(lines, DiffLine{Kind: DiffAdded, Text: "+" + b[j]})
		added++
		j++
	}
	return lines, added, removed
}
