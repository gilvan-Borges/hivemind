package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
)

// SharedDirReport records shared directory links, skipped/blocked entries, and warnings.
type SharedDirReport struct {
	Linked   []string `json:"linked,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	Blocked  []string `json:"blocked,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// WorktreeHydrationReport aggregates .worktreeinclude copies and shared directory links.
type WorktreeHydrationReport struct {
	Includes WorktreeIncludeReport `json:"includes"`
	Shared   SharedDirReport       `json:"shared"`
}

// HydrateWorktree applies .worktreeinclude literal copies and links configured
// shared rebuildable cache directories (e.g. node_modules, .gradle, .cache).
func HydrateWorktree(mainRepo, wtDir string, sharedDirs []string) (WorktreeHydrationReport, error) {
	inc, err := ApplyWorktreeInclude(mainRepo, wtDir)
	if err != nil {
		return WorktreeHydrationReport{Includes: inc}, err
	}
	shared, err := LinkSharedDirectories(mainRepo, wtDir, sharedDirs)
	return WorktreeHydrationReport{Includes: inc, Shared: shared}, err
}

// LinkSharedDirectories creates OS directory links (NTFS junctions on Windows,
// symlinks on Unix) from <wtDir>/<rel> to <mainRepo>/<rel> so large rebuildable
// caches (node_modules, .gradle, .cache) are shared without copying.
func LinkSharedDirectories(mainRepo, wtDir string, dirs []string) (SharedDirReport, error) {
	var rep SharedDirReport
	if len(dirs) == 0 {
		return rep, nil
	}
	mainAbs, err := filepath.Abs(strings.TrimSpace(mainRepo))
	if err != nil {
		return rep, err
	}
	wtAbs, err := filepath.Abs(strings.TrimSpace(wtDir))
	if err != nil {
		return rep, err
	}
	if strings.EqualFold(filepath.Clean(mainAbs), filepath.Clean(wtAbs)) {
		return rep, nil
	}

	seen := map[string]bool{}
	for _, raw := range dirs {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if err := validateLiteralWorktreeRel(entry); err != nil {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (%v)", entry, err))
			continue
		}
		cleanRel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry)))
		if pathsec.IsSensitivePath(cleanRel) {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (caminho sensível bloqueado)", cleanRel))
			continue
		}
		if seen[cleanRel] {
			continue
		}
		seen[cleanRel] = true

		src := filepath.Join(mainAbs, filepath.FromSlash(cleanRel))
		srcInfo, err := os.Stat(src)
		if os.IsNotExist(err) || (err == nil && !srcInfo.IsDir()) {
			rep.Skipped = append(rep.Skipped, cleanRel)
			continue
		}
		if err != nil {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (%v)", cleanRel, err))
			continue
		}

		dst := filepath.Join(wtAbs, filepath.FromSlash(cleanRel))
		if _, err := os.Lstat(dst); err == nil {
			rep.Skipped = append(rep.Skipped, cleanRel)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return rep, err
		}
		if err := createDirLink(src, dst); err != nil {
			return rep, fmt.Errorf("vincular diretório compartilhado %s: %w", cleanRel, err)
		}
		rep.Linked = append(rep.Linked, cleanRel)
	}
	if len(rep.Linked) > 0 {
		rep.Warnings = append(rep.Warnings,
			fmt.Sprintf("Diretórios compartilhados vinculados ao repo principal (%s): evite comandos destrutivos como rm/clean nesses caches.", strings.Join(rep.Linked, ", ")))
	}
	return rep, nil
}

func createDirLink(srcAbs, dstAbs string) error {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", dstAbs, srcAbs).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return os.Symlink(srcAbs, dstAbs)
}
