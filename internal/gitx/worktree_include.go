package gitx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
)

// WorktreeIncludeFile is the optional repository-root manifest listing literal
// relative paths of gitignored files/directories to copy into new worktrees.
const WorktreeIncludeFile = ".worktreeinclude"

// WorktreeIncludeReport summarizes what ApplyWorktreeInclude copied or blocked.
type WorktreeIncludeReport struct {
	Copied  []string `json:"copied,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Blocked []string `json:"blocked,omitempty"`
}

// ApplyWorktreeInclude reads <mainRepo>/.worktreeinclude and copies each valid,
// non-sensitive literal path into wtDir. Globs, traversal, and paths matched by
// pathsec.IsSensitivePath are always refused.
func ApplyWorktreeInclude(mainRepo, wtDir string) (WorktreeIncludeReport, error) {
	var rep WorktreeIncludeReport
	mainAbs, err := filepath.Abs(strings.TrimSpace(mainRepo))
	if err != nil {
		return rep, err
	}
	wtAbs, err := filepath.Abs(strings.TrimSpace(wtDir))
	if err != nil {
		return rep, err
	}
	raw, err := os.ReadFile(filepath.Join(mainAbs, WorktreeIncludeFile))
	if os.IsNotExist(err) {
		return rep, nil
	}
	if err != nil {
		return rep, err
	}

	mainEval, err := filepath.EvalSymlinks(mainAbs)
	if err != nil {
		mainEval = mainAbs
	}

	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
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

		src := filepath.Join(mainAbs, filepath.FromSlash(cleanRel))
		info, err := os.Lstat(src)
		if os.IsNotExist(err) {
			rep.Skipped = append(rep.Skipped, cleanRel)
			continue
		}
		if err != nil {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (%v)", cleanRel, err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (symlink bloqueado)", cleanRel))
			continue
		}
		srcEval, err := filepath.EvalSymlinks(src)
		if err != nil || !isSubPath(mainEval, srcEval) {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (fora do repositório)", cleanRel))
			continue
		}

		dst := filepath.Join(wtAbs, filepath.FromSlash(cleanRel))
		if _, err := os.Lstat(dst); err == nil {
			rep.Skipped = append(rep.Skipped, cleanRel)
			continue
		}
		if info.IsDir() {
			if err := copyNonSensitiveDir(src, dst, cleanRel, &rep); err != nil {
				return rep, err
			}
			rep.Copied = append(rep.Copied, cleanRel)
			continue
		}
		if err := copySingleFile(src, dst, info.Mode()); err != nil {
			return rep, err
		}
		rep.Copied = append(rep.Copied, cleanRel)
	}
	return rep, nil
}

func validateLiteralWorktreeRel(raw string) error {
	if strings.ContainsAny(raw, "*?[]") {
		return fmt.Errorf("globs não são permitidos")
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "\\") || (len(raw) >= 2 && raw[1] == ':') {
		return fmt.Errorf("caminho absoluto não permitido")
	}
	slash := filepath.ToSlash(raw)
	if slash == "." || slash == ".." || strings.HasPrefix(slash, "../") || strings.Contains(slash, "/../") || strings.HasSuffix(slash, "/..") {
		return fmt.Errorf("travessia de diretório bloqueada")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(slash)))
	if clean == "." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("caminho inválido")
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") || clean == ".hivemind" || strings.HasPrefix(clean, ".hivemind/") {
		return fmt.Errorf("diretório interno protegido")
	}
	return nil
}

func isSubPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func copyNonSensitiveDir(srcDir, dstDir, relBase string, rep *WorktreeIncludeReport) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		subRel := filepath.ToSlash(filepath.Join(filepath.FromSlash(relBase), entry.Name()))
		if pathsec.IsSensitivePath(subRel) {
			rep.Blocked = append(rep.Blocked, fmt.Sprintf("%s (caminho sensível bloqueado)", subRel))
			continue
		}
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())
		info, err := os.Lstat(srcPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if err := copyNonSensitiveDir(srcPath, dstPath, subRel, rep); err != nil {
				return err
			}
			continue
		}
		if err := copySingleFile(srcPath, dstPath, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copySingleFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
