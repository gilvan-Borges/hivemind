package idecore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// ErrWorkspaceDirty is returned by SyncIntegrationToWorkspace when the user's
// working tree has local changes. Sync refuses to run in that case: copying the
// integration output over uncommitted work would destroy it (the IDE used to do
// this automatically after every successful run).
var ErrWorkspaceDirty = errors.New("workspace tem alterações locais não commitadas")

// SyncIntegrationToWorkspace copies added/modified/renamed/deleted files from the
// default hivemind/integracao worktree into the user's open workspace working tree.
func SyncIntegrationToWorkspace(ctx context.Context, workspaceDir string) (int, error) {
	return SyncWorktreeToWorkspace(ctx, workspaceDir, "", "")
}

// SyncWorktreeToWorkspace copies added/modified/renamed/deleted files from a conversation
// worktree (or _integracao fallback when wtDir/branchRef are empty) into the user's open
// workspace working tree without switching branches or creating automatic commits.
//
// It is a no-op when the branch has nothing new. When the user's working tree is not
// clean it returns ErrWorkspaceDirty and changes nothing.
func SyncWorktreeToWorkspace(ctx context.Context, workspaceDir, wtDir, branchRef string) (int, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) || !g.HasCommit(ctx, workspaceDir) {
		return 0, nil
	}
	branch := strings.TrimSpace(branchRef)
	if branch == "" {
		branch = gitx.IntegrationBranch
	}
	if !g.BranchExists(ctx, workspaceDir, branch) {
		return 0, nil
	}
	intWT := strings.TrimSpace(wtDir)
	if intWT == "" {
		intWT = workspace.WorktreePath(workspaceDir, "_integracao")
	}
	if info, err := os.Stat(intWT); err != nil || !info.IsDir() {
		legacyWT := filepath.Join(workspaceDir, ".hivemind", "worktrees", "_integracao")
		if lInfo, lErr := os.Stat(legacyWT); lErr == nil && lInfo.IsDir() {
			intWT = legacyWT
		} else {
			return 0, nil
		}
	}
	status, err := runGit(ctx, workspaceDir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(status) != "" && hasUserChangesInWorkspace(ctx, workspaceDir, status) {
		return 0, ErrWorkspaceDirty
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "--name-status", "HEAD.."+branch)
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, err
	}

	copyFromWT := func(relSlash string) error {
		relOS := filepath.FromSlash(relSlash)
		src, err := workspace.Resolve(intWT, relOS)
		if err != nil {
			return err
		}
		dst, err := workspace.Resolve(workspaceDir, relOS)
		if err != nil {
			return err
		}
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return atomicWriteFile(dst, data, info.Mode().Perm())
	}

	removeInWorkspace := func(relSlash string) error {
		dst, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relSlash))
		if err != nil {
			return err
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	synced := 0
	var failures []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		for i := range parts {
			parts[i] = gitx.UnquotePath(parts[i])
		}
		code := parts[0]
		switch {
		case strings.HasPrefix(code, "R") && len(parts) >= 3:
			// Copy the new path before removing the old one so a failed copy
			// never loses the user's file.
			if err := copyFromWT(parts[2]); err != nil {
				failures = append(failures, parts[2]+": "+err.Error())
				continue
			}
			if err := removeInWorkspace(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		case code == "D":
			if err := removeInWorkspace(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		default:
			if err := copyFromWT(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		}
	}
	if len(failures) > 0 {
		return synced, fmt.Errorf("falha ao sincronizar %d arquivo(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return synced, nil
}

// hasUserChanges reports whether git status --porcelain contains changes that
// are not HiveMind's own bookkeeping. .hivemind/ holds sessions and worktrees;
// it is expected to be untracked while the project has not committed the
// recommended .gitignore entry, and must not block a safe integration sync.
func hasUserChanges(porcelain string) bool {
	return hasUserChangesInWorkspace(context.Background(), "", porcelain)
}

func hasUserChangesInWorkspace(ctx context.Context, workspaceDir, porcelain string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(porcelain, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 4 {
			continue
		}
		code := ""
		if len(line) >= 2 {
			code = line[:2]
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = gitx.UnquotePath(path)
		path = filepath.ToSlash(path)
		if path == ".hivemind" || strings.HasPrefix(path, ".hivemind/") {
			continue
		}
		if path == ".gitignore" && workspaceDir != "" && isHiveMindOnlyGitignoreChange(ctx, workspaceDir, code == "??") {
			continue
		}
		return true
	}
	return false
}

func isHiveMindIgnoreLine(raw string) bool {
	s := strings.TrimSpace(raw)
	return s == "" || s == "# HiveMind" || s == ".hivemind/" || s == ".hivemind"
}

// isHiveMindOnlyGitignoreChange returns true when .gitignore only contains
// HiveMind's own EnsureHiveIgnore addition ("# HiveMind\n.hivemind/\n"), so
// prepareDevs adding .hivemind/ does not falsely block integration sync.
func isHiveMindOnlyGitignoreChange(ctx context.Context, workspaceDir string, untracked bool) bool {
	if untracked {
		data, err := os.ReadFile(filepath.Join(workspaceDir, ".gitignore"))
		if err != nil {
			return false
		}
		sawEntry := false
		for _, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if !isHiveMindIgnoreLine(ln) {
				return false
			}
			if strings.TrimSpace(ln) == ".hivemind/" || strings.TrimSpace(ln) == ".hivemind" {
				sawEntry = true
			}
		}
		return sawEntry
	}
	diffOut, err := runGit(ctx, workspaceDir, "diff", "HEAD", "--", ".gitignore")
	if err != nil {
		return false
	}
	sawChange := false
	for _, ln := range strings.Split(strings.ReplaceAll(diffOut, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, "@@") || strings.HasPrefix(ln, "diff ") || strings.HasPrefix(ln, "index ") || strings.HasPrefix(ln, `\ No newline`) {
			continue
		}
		if strings.HasPrefix(ln, "-") {
			return false
		}
		if strings.HasPrefix(ln, "+") {
			if !isHiveMindIgnoreLine(ln[1:]) {
				return false
			}
			sawChange = true
		}
	}
	return sawChange
}

// atomicWriteFile writes data to path through a temporary file in the same
// directory, then renames it into place. A crash mid-write cannot truncate the
// original file. When mode == 0 (new file), O_CREATE with 0o666 is used so the
// OS process umask is respected rather than bypassed via Chmod(0644).
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	var (
		tmp     *os.File
		tmpName string
		err     error
	)
	if mode == 0 {
		for range 8 {
			var rnd [6]byte
			_, _ = rand.Read(rnd[:])
			cand := filepath.Join(dir, "."+base+"."+hex.EncodeToString(rnd[:])+".tmp")
			tmp, err = os.OpenFile(cand, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
			if err == nil {
				tmpName = cand
				break
			}
			if !os.IsExist(err) {
				return err
			}
		}
		if err != nil {
			return err
		}
	} else {
		tmp, err = os.CreateTemp(dir, "."+base+".*.tmp")
		if err != nil {
			return err
		}
		tmpName = tmp.Name()
	}
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpName, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}
