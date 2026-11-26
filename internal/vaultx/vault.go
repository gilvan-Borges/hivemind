// Package vaultx builds and maintains a lightweight, Git-excluded Obsidian-style
// Markdown vault + AST/symbol Repo Map inside each conversation worktree
// (`.hivemind/vault/`) so agents navigate code, specs, Squad contracts, and
// lane history directly without blind repo-wide scanning.
package vaultx

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

const (
	vaultRelDir   = ".hivemind/vault"
	maxRepoMapLen = 40 * 1024
	maxScanFiles  = 4000
	maxFileBytes  = 256 * 1024
)

// Options configures vault generation for a worktree or workspace.
type Options struct {
	Root         string
	Branch       string
	LaneSlot     string
	Objective    string
	JiraKey      string
	PRNumber     int
	StackSummary string
	TestHint     string
	BuildHint    string
	Now          time.Time
}

// Summary describes the generated vault files and compact prompt pointer.
type Summary struct {
	VaultDir     string
	IndexPath    string
	RepoMapPath  string
	ModuleCount  int
	SymbolFiles  int
	SpecCount    int
	PromptBanner string
}

// SquadBoardSnapshot captures the live Squad state persisted into SQUAD_BOARD.md.
type SquadBoardSnapshot struct {
	Objective  string
	Branch     string
	Owners     map[string][]string
	Touched    map[string][]string
	Notes      []SquadNote
	Directives []SquadDirective
	QAFailures []string
}

// SquadNote mirrors TeamNote for persistent Blackboard rendering.
type SquadNote struct {
	Author  string
	To      string
	Topic   string
	Content string
}

// SquadDirective mirrors TeamDirective for persistent Blackboard rendering.
type SquadDirective struct {
	From    string
	To      string
	Content string
}

// LaneEntry appends a concise turn record to `.hivemind/vault/lanes/<slot>.md`.
type LaneEntry struct {
	Slot      string
	Branch    string
	Objective string
	JiraKey   string
	PRNumber  int
	Summary   string
	Status    string
	Files     []string
	Timestamp time.Time
}

var skipDirs = map[string]bool{
	".git": true, ".hivemind": true, "node_modules": true, "target": true,
	"dist": true, "build": true, "out": true, ".next": true, ".nuxt": true,
	".gradle": true, ".idea": true, ".vscode": true, "vendor": true,
	"__pycache__": true, ".venv": true, "venv": true, "bin": true, "obj": true,
}

var (
	reJavaKotlin = regexp.MustCompile(`^\s*(?:public\s+|protected\s+|private\s+|abstract\s+|final\s+|sealed\s+|data\s+|open\s+)*(class|interface|record|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	reGoTypeFunc = regexp.MustCompile(`^(?:type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:struct|interface)|func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\()`)
	reTSJS       = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?(function|class|interface|type|const|enum)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	rePython     = regexp.MustCompile(`^(?:async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	reTestFile   = regexp.MustCompile(`(?i)(_test\.go|\.test\.[tj]sx?|\.spec\.[tj]sx?|Test\.(?:java|kt)|Tests\.(?:java|kt)|test_[^/]+\.py|_test\.py)$`)
)

type repoFileInfo struct {
	rel     string
	module  string
	ext     string
	isTest  bool
	symbols []string
}

// EnsureGitExclude appends `.hivemind/` to `.git/info/exclude` (resolved via
// `--git-common-dir` so it covers worktrees too) without touching tracked files.
func EnsureGitExclude(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-common-dir")
	procx.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return nil
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	excludePath := filepath.Join(filepath.Clean(common), "info", "exclude")
	existingBytes, _ := os.ReadFile(excludePath)
	existing := string(existingBytes)
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == ".hivemind/" || t == ".hivemind" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("# HiveMind local vault & state\n.hivemind/\n")
	return atomicx.WriteFile(excludePath, []byte(b.String()), 0o644)
}

// GenerateWorktreeVault builds `.hivemind/vault/` (INDEX.md, repomap.md,
// modules/*.md, SPECS.md, DECISIONS.md, and lanes/<slot>.md) for the given root.
func GenerateWorktreeVault(opts Options) (Summary, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return Summary{}, fmt.Errorf("vaultx: root vazio")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	_ = EnsureGitExclude(root)

	vaultDir := filepath.Join(root, filepath.FromSlash(vaultRelDir))
	modulesDir := filepath.Join(vaultDir, "modules")
	lanesDir := filepath.Join(vaultDir, "lanes")
	for _, dir := range []string{vaultDir, modulesDir, lanesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Summary{}, err
		}
	}

	files, specs, adrs, hasMemory := scanWorkspace(root)
	modNames, modFiles := groupByModule(files)

	// 1. Write modules/<mod>.md
	for _, mod := range modNames {
		if err := writeModuleNote(modulesDir, mod, modFiles[mod]); err != nil {
			return Summary{}, err
		}
	}

	// 2. Write repomap.md (lightweight Aider-style structural symbol map)
	repoMapPath := filepath.Join(vaultDir, "repomap.md")
	symFileCount := writeRepoMap(repoMapPath, modNames, modFiles)

	// 3. Write SPECS.md
	if err := writeSpecsNote(filepath.Join(vaultDir, "SPECS.md"), specs, adrs, hasMemory); err != nil {
		return Summary{}, err
	}

	// 4. Initialize DECISIONS.md & SQUAD_BOARD.md only if absent (never clobber live notes)
	decisionsPath := filepath.Join(vaultDir, "DECISIONS.md")
	if _, err := os.Stat(decisionsPath); os.IsNotExist(err) {
		_ = atomicx.WriteFile(decisionsPath, []byte("# Decisões & Aprendizados do Projeto\n\nRegistre aqui decisões arquiteturais e descobertas duráveis entre conversas ([[INDEX]]).\n"), 0o644)
	}
	boardPath := filepath.Join(vaultDir, "SQUAD_BOARD.md")
	if _, err := os.Stat(boardPath); os.IsNotExist(err) {
		_ = atomicx.WriteFile(boardPath, []byte("# Squad Board (Blackboard Vivo)\n\nContratos entre agentes, arquivos em edição e falhas recentes aparecem aqui automaticamente durante execuções do Squad.\n"), 0o644)
	}

	// 5. Update lane diary if slot provided
	laneLink := ""
	slot := sanitizeSlug(opts.LaneSlot)
	if slot != "" {
		laneLink = "lanes/" + slot
		_ = initOrUpdateLaneHeader(filepath.Join(lanesDir, slot+".md"), opts, now)
	}

	// 6. Write INDEX.md
	indexPath := filepath.Join(vaultDir, "INDEX.md")
	if err := writeIndexNote(indexPath, opts, modNames, modFiles, len(specs)+len(adrs), laneLink, now); err != nil {
		return Summary{}, err
	}

	banner := buildPromptBanner(opts, modNames, len(specs)+len(adrs), laneLink)
	return Summary{
		VaultDir:     vaultDir,
		IndexPath:    indexPath,
		RepoMapPath:  repoMapPath,
		ModuleCount:  len(modNames),
		SymbolFiles:  symFileCount,
		SpecCount:    len(specs) + len(adrs),
		PromptBanner: banner,
	}, nil
}

// SyncSquadBoard persists the active TeamBoard state into `.hivemind/vault/SQUAD_BOARD.md`.
func SyncSquadBoard(root string, snap SquadBoardSnapshot) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	_ = EnsureGitExclude(root)
	vaultDir := filepath.Join(root, filepath.FromSlash(vaultRelDir))
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Squad Board (Blackboard Vivo)\n\n")
	b.WriteString("Voltar: [[INDEX]] · [[DECISIONS]] · [[repomap]]\n\n")
	if strings.TrimSpace(snap.Branch) != "" {
		fmt.Fprintf(&b, "- **Branch da worktree:** `%s`\n", strings.TrimSpace(snap.Branch))
	}
	if strings.TrimSpace(snap.Objective) != "" {
		obj := strings.ReplaceAll(strings.TrimSpace(snap.Objective), "\n", " ")
		if len(obj) > 260 {
			obj = obj[:260] + "…"
		}
		fmt.Fprintf(&b, "- **Objetivo:** %s\n", obj)
	}
	b.WriteByte('\n')

	if len(snap.Owners) > 0 || len(snap.Touched) > 0 {
		b.WriteString("## Agentes & Arquivos\n\n")
		agents := sortedKeysUnion(snap.Owners, snap.Touched)
		for _, ag := range agents {
			fmt.Fprintf(&b, "### %s\n", ag)
			if own := snap.Owners[ag]; len(own) > 0 {
				fmt.Fprintf(&b, "- **Escopo:** `%s`\n", strings.Join(own, "`, `"))
			}
			if touched := snap.Touched[ag]; len(touched) > 0 {
				fmt.Fprintf(&b, "- **Editados:** `%s`\n", strings.Join(touched, "`, `"))
			}
			b.WriteByte('\n')
		}
	}

	if len(snap.Notes) > 0 {
		b.WriteString("## Contratos & Notas Compartilhadas (`share_team_note`)\n\n")
		for _, n := range snap.Notes {
			target := n.To
			if target == "" {
				target = "todos"
			}
			fmt.Fprintf(&b, "- **[%s → %s] %s:** %s\n", n.Author, target, n.Topic, n.Content)
		}
		b.WriteByte('\n')
	}

	if len(snap.QAFailures) > 0 {
		b.WriteString("## Tentativas & Reprovações QA (NÃO repetir estes erros)\n\n")
		for _, f := range snap.QAFailures {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(f))
		}
		b.WriteByte('\n')
	}

	if len(snap.Directives) > 0 {
		b.WriteString("## Diretrizes da Mesa Redonda\n\n")
		for _, d := range snap.Directives {
			fmt.Fprintf(&b, "- **%s → %s:** %s\n", d.From, d.To, d.Content)
		}
		b.WriteByte('\n')
	}

	return atomicx.WriteFile(filepath.Join(vaultDir, "SQUAD_BOARD.md"), []byte(b.String()), 0o644)
}

// AppendLaneNote appends a turn outcome entry into `.hivemind/vault/lanes/<slot>.md`.
func AppendLaneNote(root string, entry LaneEntry) error {
	root = strings.TrimSpace(root)
	slot := sanitizeSlug(entry.Slot)
	if root == "" || slot == "" {
		return nil
	}
	_ = EnsureGitExclude(root)
	lanesDir := filepath.Join(root, filepath.FromSlash(vaultRelDir), "lanes")
	if err := os.MkdirAll(lanesDir, 0o755); err != nil {
		return err
	}
	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	path := filepath.Join(lanesDir, slot+".md")
	existing, _ := os.ReadFile(path)
	var b strings.Builder
	if len(existing) == 0 {
		fmt.Fprintf(&b, "# Conversa `%s`\n\nVoltar: [[../INDEX]] · [[../SQUAD_BOARD]]\n\n", slot)
	} else {
		b.Write(existing)
		if !bytes.HasSuffix(existing, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "## %s", ts.Format("2006-01-02 15:04"))
	if entry.Status != "" {
		fmt.Fprintf(&b, " · %s", entry.Status)
	}
	b.WriteByte('\n')
	if entry.Branch != "" {
		fmt.Fprintf(&b, "- **Branch:** `%s`\n", entry.Branch)
	}
	if entry.JiraKey != "" {
		fmt.Fprintf(&b, "- **Jira:** `%s`\n", entry.JiraKey)
	}
	if entry.PRNumber > 0 {
		fmt.Fprintf(&b, "- **PR:** #%d\n", entry.PRNumber)
	}
	if sum := strings.TrimSpace(entry.Summary); sum != "" {
		if len(sum) > 500 {
			sum = sum[:500] + "…"
		}
		fmt.Fprintf(&b, "- **Resumo:** %s\n", strings.ReplaceAll(sum, "\n", " "))
	}
	if len(entry.Files) > 0 {
		limit := len(entry.Files)
		if limit > 15 {
			limit = 15
		}
		fmt.Fprintf(&b, "- **Arquivos:** `%s`\n", strings.Join(entry.Files[:limit], "`, `"))
	}
	b.WriteByte('\n')
	return atomicx.WriteFile(path, []byte(b.String()), 0o644)
}

func scanWorkspace(root string) (files []repoFileInfo, specs []string, adrs []string, hasMemory bool) {
	if _, err := os.Stat(filepath.Join(root, ".hivemind", "MEMORY.md")); err == nil {
		hasMemory = true
	}
	moduleRoots := discoverModuleRoots(root)
	count := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] || (strings.HasPrefix(name, ".") && name != ".agents" && name != ".specify") {
				if p != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		count++
		if count > maxScanFiles {
			return filepath.SkipAll
		}
		rel, rErr := filepath.Rel(root, p)
		if rErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		low := strings.ToLower(rel)
		if strings.HasPrefix(low, "specs/") && strings.HasSuffix(low, ".md") {
			specs = append(specs, rel)
			return nil
		}
		if strings.HasPrefix(low, "docs/adr/") && strings.HasSuffix(low, ".md") {
			adrs = append(adrs, rel)
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !isCodeExt(ext) {
			return nil
		}
		mod := resolveModule(rel, moduleRoots)
		syms := extractSymbols(p, ext)
		files = append(files, repoFileInfo{
			rel:     rel,
			module:  mod,
			ext:     ext,
			isTest:  reTestFile.MatchString(rel),
			symbols: syms,
		})
		return nil
	})
	sort.Strings(specs)
	sort.Strings(adrs)
	return files, specs, adrs, hasMemory
}

func discoverModuleRoots(root string) []string {
	var mods []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return mods
	}
	for _, e := range entries {
		if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(root, e.Name())
		for _, marker := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "go.mod", "package.json", "pyproject.toml", "Cargo.toml"} {
			if _, sErr := os.Stat(filepath.Join(sub, marker)); sErr == nil {
				mods = append(mods, e.Name())
				break
			}
		}
	}
	sort.Strings(mods)
	return mods
}

func resolveModule(rel string, moduleRoots []string) string {
	for _, m := range moduleRoots {
		if strings.HasPrefix(rel, m+"/") {
			return m
		}
	}
	if idx := strings.IndexByte(rel, '/'); idx > 0 {
		return rel[:idx]
	}
	return "root"
}

func isCodeExt(ext string) bool {
	switch ext {
	case ".go", ".java", ".kt", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".cs":
		return true
	default:
		return false
	}
}

func extractSymbols(fullPath, ext string) []string {
	info, err := os.Stat(fullPath)
	if err != nil || info.Size() > maxFileBytes {
		return nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() && lines < 300 {
		lines++
		line := sc.Text()
		var sym string
		switch ext {
		case ".java", ".kt":
			if m := reJavaKotlin.FindStringSubmatch(line); len(m) >= 3 {
				sym = m[1] + " " + m[2]
			}
		case ".go":
			if m := reGoTypeFunc.FindStringSubmatch(line); len(m) >= 3 {
				if m[1] != "" {
					sym = "type " + m[1]
				} else if m[2] != "" {
					sym = "func " + m[2]
				}
			}
		case ".ts", ".tsx", ".js", ".jsx":
			if m := reTSJS.FindStringSubmatch(line); len(m) >= 3 {
				sym = m[1] + " " + m[2]
			}
		case ".py":
			if m := rePython.FindStringSubmatch(line); len(m) >= 3 {
				sym = m[1] + " " + m[2]
			}
		}
		if sym != "" && !seen[sym] {
			seen[sym] = true
			out = append(out, sym)
			if len(out) >= 12 {
				break
			}
		}
	}
	return out
}

func groupByModule(files []repoFileInfo) ([]string, map[string][]repoFileInfo) {
	byMod := make(map[string][]repoFileInfo)
	for _, f := range files {
		byMod[f.module] = append(byMod[f.module], f)
	}
	var names []string
	for k := range byMod {
		names = append(names, k)
		sort.Slice(byMod[k], func(i, j int) bool {
			return byMod[k][i].rel < byMod[k][j].rel
		})
	}
	sort.Strings(names)
	return names, byMod
}

func writeModuleNote(modulesDir, mod string, files []repoFileInfo) error {
	slug := sanitizeSlug(mod)
	if slug == "" {
		slug = "module"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Módulo `%s`\n\nVoltar: [[../INDEX]] · [[../repomap]] · [[../SQUAD_BOARD]]\n\n", mod)
	var prod, tests []repoFileInfo
	for _, f := range files {
		if f.isTest {
			tests = append(tests, f)
		} else {
			prod = append(prod, f)
		}
	}
	fmt.Fprintf(&b, "- **Arquivos de código:** %d\n- **Arquivos de teste:** %d\n\n", len(prod), len(tests))
	if len(prod) > 0 {
		b.WriteString("## Principais arquivos e símbolos\n\n")
		limit := len(prod)
		if limit > 80 {
			limit = 80
		}
		for _, f := range prod[:limit] {
			if len(f.symbols) > 0 {
				fmt.Fprintf(&b, "- `%s` — %s\n", f.rel, strings.Join(f.symbols, ", "))
			} else {
				fmt.Fprintf(&b, "- `%s`\n", f.rel)
			}
		}
		if len(prod) > limit {
			fmt.Fprintf(&b, "- … e mais %d arquivo(s)\n", len(prod)-limit)
		}
		b.WriteByte('\n')
	}
	if len(tests) > 0 {
		b.WriteString("## Testes\n\n")
		limit := len(tests)
		if limit > 40 {
			limit = 40
		}
		for _, f := range tests[:limit] {
			fmt.Fprintf(&b, "- `%s`\n", f.rel)
		}
		if len(tests) > limit {
			fmt.Fprintf(&b, "- … e mais %d teste(s)\n", len(tests)-limit)
		}
		b.WriteByte('\n')
	}
	return atomicx.WriteFile(filepath.Join(modulesDir, slug+".md"), []byte(b.String()), 0o644)
}

func writeRepoMap(path string, modNames []string, modFiles map[string][]repoFileInfo) int {
	var b strings.Builder
	b.WriteString("# Repo Map (Símbolos por Módulo)\n\nVoltar: [[INDEX]] · [[SPECS]] · [[SQUAD_BOARD]]\n\n")
	symFiles := 0
	for _, mod := range modNames {
		list := modFiles[mod]
		var withSyms []repoFileInfo
		for _, f := range list {
			if len(f.symbols) > 0 && !f.isTest {
				withSyms = append(withSyms, f)
			}
		}
		if len(withSyms) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", mod)
		for _, f := range withSyms {
			if b.Len() >= maxRepoMapLen-256 {
				b.WriteString("\n…(repomap truncado para manter leitura rápida; consulte [[modules/" + sanitizeSlug(mod) + "]])\n")
				_ = atomicx.WriteFile(path, []byte(b.String()), 0o644)
				return symFiles
			}
			symFiles++
			fmt.Fprintf(&b, "- `%s`: %s\n", f.rel, strings.Join(f.symbols, ", "))
		}
		b.WriteByte('\n')
	}
	_ = atomicx.WriteFile(path, []byte(b.String()), 0o644)
	return symFiles
}

func writeSpecsNote(path string, specs, adrs []string, hasMemory bool) error {
	var b strings.Builder
	b.WriteString("# Specs, ADRs & Memória\n\nVoltar: [[INDEX]] · [[DECISIONS]]\n\n")
	if hasMemory {
		b.WriteString("- **Memória do time:** `.hivemind/MEMORY.md`\n\n")
	}
	if len(adrs) > 0 {
		b.WriteString("## ADRs\n\n")
		for _, a := range adrs {
			fmt.Fprintf(&b, "- `%s`\n", a)
		}
		b.WriteByte('\n')
	}
	if len(specs) > 0 {
		b.WriteString("## Specs\n\n")
		limit := len(specs)
		if limit > 80 {
			limit = 80
		}
		for _, s := range specs[:limit] {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		if len(specs) > limit {
			fmt.Fprintf(&b, "- … e mais %d arquivo(s) em `specs/`\n", len(specs)-limit)
		}
		b.WriteByte('\n')
	}
	if len(adrs) == 0 && len(specs) == 0 && !hasMemory {
		b.WriteString("Nenhuma spec ou ADR encontrada nesta branch.\n")
	}
	return atomicx.WriteFile(path, []byte(b.String()), 0o644)
}

func initOrUpdateLaneHeader(path string, opts Options, now time.Time) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Conversa `%s`\n\nVoltar: [[../INDEX]] · [[../SQUAD_BOARD]]\n\n", sanitizeSlug(opts.LaneSlot))
	fmt.Fprintf(&b, "- **Iniciada em:** %s\n", now.Format("2006-01-02 15:04"))
	if opts.Branch != "" {
		fmt.Fprintf(&b, "- **Branch:** `%s`\n", opts.Branch)
	}
	if opts.JiraKey != "" {
		fmt.Fprintf(&b, "- **Jira:** `%s`\n", opts.JiraKey)
	}
	if opts.PRNumber > 0 {
		fmt.Fprintf(&b, "- **PR:** #%d\n", opts.PRNumber)
	}
	if obj := strings.TrimSpace(opts.Objective); obj != "" {
		clean := strings.ReplaceAll(obj, "\n", " ")
		if len(clean) > 300 {
			clean = clean[:300] + "…"
		}
		fmt.Fprintf(&b, "- **Objetivo inicial:** %s\n", clean)
	}
	b.WriteByte('\n')
	return atomicx.WriteFile(path, []byte(b.String()), 0o644)
}

func writeIndexNote(path string, opts Options, modNames []string, modFiles map[string][]repoFileInfo, specCount int, laneLink string, now time.Time) error {
	var b strings.Builder
	b.WriteString("# Vault da Worktree (Obsidian + Repo Map)\n\n")
	fmt.Fprintf(&b, "- **Atualizado em:** %s\n", now.Format("2006-01-02 15:04:05"))
	if opts.Branch != "" {
		fmt.Fprintf(&b, "- **Branch:** `%s`\n", opts.Branch)
	}
	if opts.StackSummary != "" {
		fmt.Fprintf(&b, "- **Stack:** %s\n", opts.StackSummary)
	}
	if opts.TestHint != "" {
		fmt.Fprintf(&b, "- **Testes:** `%s`\n", opts.TestHint)
	}
	if opts.BuildHint != "" {
		fmt.Fprintf(&b, "- **Build:** `%s`\n", opts.BuildHint)
	}
	if opts.JiraKey != "" {
		fmt.Fprintf(&b, "- **Jira:** `%s`\n", opts.JiraKey)
	}
	if opts.PRNumber > 0 {
		fmt.Fprintf(&b, "- **PR:** #%d\n", opts.PRNumber)
	}

	b.WriteString("\n## Mapa Principal\n\n")
	b.WriteString("- [[repomap]] — Assinaturas de classes, interfaces e funções por módulo\n")
	fmt.Fprintf(&b, "- [[SPECS]] — Specs, ADRs e `.hivemind/MEMORY.md` (%d arquivo(s))\n", specCount)
	b.WriteString("- [[SQUAD_BOARD]] — Blackboard vivo de contratos entre Devs, escopos e falhas de QA\n")
	b.WriteString("- [[DECISIONS]] — Decisões técnicas e aprendizados duráveis\n")
	if laneLink != "" {
		fmt.Fprintf(&b, "- [[%s]] — Diário desta conversa/lane\n", laneLink)
	}

	if len(modNames) > 0 {
		b.WriteString("\n## Módulos\n\n")
		for _, m := range modNames {
			fmt.Fprintf(&b, "- [[modules/%s]] — `%s` (%d arquivo(s))\n", sanitizeSlug(m), m, len(modFiles[m]))
		}
	}
	return atomicx.WriteFile(path, []byte(b.String()), 0o644)
}

func buildPromptBanner(opts Options, modNames []string, specCount int, laneLink string) string {
	var b strings.Builder
	b.WriteString("\n- **Vault Obsidian & Repo Map da Worktree (`.hivemind/vault/`):** ")
	b.WriteString("Índice pronto em `.hivemind/vault/INDEX.md`, mapa de símbolos em `.hivemind/vault/repomap.md`, specs em `.hivemind/vault/SPECS.md` e blackboard em `.hivemind/vault/SQUAD_BOARD.md`")
	if laneLink != "" {
		fmt.Fprintf(&b, " (diário da conversa: `.hivemind/vault/%s.md`)", laneLink)
	}
	b.WriteString(". ")
	if len(modNames) > 0 {
		limit := len(modNames)
		if limit > 8 {
			limit = 8
		}
		fmt.Fprintf(&b, "Módulos mapeados (%d): `%s`", len(modNames), strings.Join(modNames[:limit], "`, `"))
		if len(modNames) > limit {
			fmt.Fprintf(&b, " (+%d)", len(modNames)-limit)
		}
		b.WriteString(". ")
	}
	if specCount > 0 {
		fmt.Fprintf(&b, "Specs/ADRs indexados: %d. ", specCount)
	}
	b.WriteString("Consulte `.hivemind/vault/INDEX.md` ou `repomap.md` com `read_file` antes de sair varrendo o repositório às cegas.\n")
	return b.String()
}

func sanitizeSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func sortedKeysUnion(a, b map[string][]string) []string {
	seen := make(map[string]bool)
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
