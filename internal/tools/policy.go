// Package tools exposes workspace, shell, git, GitHub and Jira tools to ADK agents.
//
// The catalog is split by domain: policy.go (role policy and shared state),
// fs.go (read/write/edit/list), shell.go (sandboxed shell), git.go, github.go,
// jira.go and qa.go. The public entry point remains ForRole.
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/adk/v2/tool"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Deps are the clients a tool set closes over. Each developer gets its own
// Root so parallel agents cannot write into each other's worktrees.
type Deps struct {
	Root                   string
	Role                   string
	Settings               team.Settings
	DisableGitWrite        bool
	DisableMerge           bool // Hard-gate: omite github_merge_pr do catálogo (usado no modo auto-review)
	ReviewCommentOnly      bool // Quando true, github_review_pr aceita apenas evento COMMENT
	AllowReadOnlyTestShell bool // run_shell continua disponível, mas commit/push/merge/PR via shell ficam bloqueados (auto-review)
	NoShell                bool // Revisão automática sem testes: não entrega run_shell
	ReviewRequestedInObj   bool
	// DocsOnlyWrite libera APENAS o write_doc (gravação restrita a documentação)
	// para fases SDD, onde código permanece bloqueado até a aprovação dos docs.
	DocsOnlyWrite bool
	Shell         shellx.Runner
	Sandbox       sandboxx.Runner
	Allowlist     []string
	Confirm       shellx.Confirmer
	hostConfirm   shellx.Confirmer
	shellPrepared bool
	Approve       func(prompt string) bool
	Git           gitx.Git
	GitHub        githubx.Client
	Jira          jirax.Client
	JiraKey       string
	JiraProject   string
	BaseBranch    string
	// MCP, quando preenchido, expõe só os servidores ligados e as duas
	// ferramentas leves de lista e de ligar sob aprovação.
	MCP *mcpx.Host
	// SkillLookup resolve o corpo completo de uma skill pelo nome para a tool
	// read_skill (progressive disclosure). Quando nil, read_skill não é exposta.
	SkillLookup func(name string) (skillx.Skill, string, bool)
	tracker     *fileReadTracker
}

type fileReadTracker struct {
	mu     sync.Mutex
	mtimes map[string]time.Time
}

func newFileReadTracker() *fileReadTracker {
	return &fileReadTracker{mtimes: make(map[string]time.Time)}
}

func (t *fileReadTracker) record(path string, modTime time.Time) {
	if t == nil || path == "" || modTime.IsZero() {
		return
	}
	t.mu.Lock()
	t.mtimes[filepath.Clean(path)] = modTime
	t.mu.Unlock()
}

func (t *fileReadTracker) refresh(path string) {
	if t == nil || path == "" {
		return
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		t.record(path, info.ModTime())
	}
}

func (t *fileReadTracker) checkNotStale(root, relArg string) error {
	if t == nil {
		return nil
	}
	path, err := workspace.Resolve(root, relArg)
	if err != nil {
		return nil
	}
	clean := filepath.Clean(path)
	t.mu.Lock()
	recorded, ok := t.mtimes[clean]
	t.mu.Unlock()
	if !ok {
		return nil
	}
	info, err := os.Stat(clean)
	if err != nil {
		return nil
	}
	if info.ModTime().After(recorded) {
		return fmt.Errorf("edição recusada: %s foi modificado externamente desde a última leitura; chame read_file novamente antes de editar", relArg)
	}
	return nil
}

// ForRole returns the tools a role is allowed to call.
// Governance rule: ONLY the Chief (RoleProjectManager) or Architect (RoleArchitect)
// may commit, push, create PRs, reply/request-review on PRs, or merge PRs.
func ForRole(role string, deps Deps) ([]tool.Tool, error) {
	normRole := team.NormalizeRole(role)
	deps.Role = normRole
	if deps.tracker == nil {
		deps.tracker = newFileReadTracker()
	}
	if deps.Shell == nil {
		deps.Shell = shellx.Local{}
	}
	if deps.Git == nil {
		deps.Git = gitx.Local{}
	}
	deps = applyShellPolicy(deps)

	var out []tool.Tool
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	switch normRole {
	case team.RoleProjectManager:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if !deps.DisableGitWrite {
			if err := add(deps.gitCommit()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPush()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPull()); err != nil {
				return nil, err
			}
		}
		if !deps.NoShell {
			if err := add(deps.canShell()); err != nil {
				return nil, err
			}
		}
		if err := deps.addJira(&out); err != nil {
			return nil, err
		}
		if deps.DocsOnlyWrite {
			if err := add(deps.writeDoc()); err != nil {
				return nil, err
			}
		}
		if err := deps.addGitHubGovernance(&out, !deps.DisableGitWrite); err != nil {
			return nil, err
		}

	case team.RoleTechLead:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if err := deps.addJiraComment(&out); err != nil {
			return nil, err
		}
		if err := deps.addGitHubRead(&out); err != nil {
			return nil, err
		}

	case team.RoleArchitect:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
			return nil, err
		}
		if err := add(deps.canEdit()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if !deps.DisableGitWrite {
			if err := add(deps.gitCommit()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPush()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPull()); err != nil {
				return nil, err
			}
		}
		if !deps.NoShell {
			if err := add(deps.canShell()); err != nil {
				return nil, err
			}
		}
		if err := deps.addGitHubGovernance(&out, !deps.DisableGitWrite); err != nil {
			return nil, err
		}

	case team.RoleDeveloper:
		// Developers implement and test inside their isolated worktree; they NEVER commit, push, or manage PRs.
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
			return nil, err
		}
		if err := add(deps.canEdit()); err != nil {
			return nil, err
		}
		if err := add(deps.canShell()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}

	case team.RoleQA:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canShell()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if err := add(deps.qaApprove()); err != nil {
			return nil, err
		}
		if err := add(deps.qaChanges()); err != nil {
			return nil, err
		}
		if err := deps.addJiraComment(&out); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("papel sem ferramentas: %s", role)
	}
	if deps.SkillLookup != nil {
		if err := add(deps.canReadSkill()); err != nil {
			return nil, err
		}
	}
	if err := deps.addMCP(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d Deps) canCommitAndManagePR() bool {
	return d.Role == team.RoleProjectManager || d.Role == team.RoleArchitect
}

type textResult struct {
	Text string `json:"text"`
}
