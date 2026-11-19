package idecore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

// skillNameRe valida nomes de skills (pastas) criados pelo painel da IDE.
var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// NativeSkillsDir retorna (criando se necessário) o catálogo nativo da IDE:
// %LOCALAPPDATA%\HiveMind\skills. Skills criadas aqui valem para TODOS os
// projetos abertos na IDE e são gerenciadas pelo painel de Skills.
func NativeSkillsDir() string {
	dir := skillx.UserSkillsDir()
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// SkillInfo é uma entrada do painel de Skills da IDE.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "native" | "project"
	Path        string `json:"path"`
	UpdatedAt   string `json:"updatedAt"` // YYYY-MM-DD
	SizeBytes   int64  `json:"sizeBytes"`
	Enabled     bool   `json:"enabled"`
}

// ListSkills devolve o catálogo combinado: nativas da IDE primeiro, depois as do projeto.
func ListSkills(projectRoot string) ([]SkillInfo, error) {
	out := []SkillInfo{}
	for _, s := range skillx.LoadNative(NativeSkillsDir()) {
		out = append(out, skillToInfo(s, NativeSkillsDir()))
	}
	for _, s := range skillx.Load(projectRoot) {
		out = append(out, skillToInfo(s, projectRoot))
	}
	return out, nil
}

func skillToInfo(s skillx.Skill, baseRoot string) SkillInfo {
	info := SkillInfo{
		Name:        s.Name,
		Description: s.Description,
		Source:      s.Source,
		Path:        s.Path,
		SizeBytes:   0,
		Enabled:     s.Enabled,
	}
	if st, err := os.Stat(filepath.Join(baseRoot, filepath.FromSlash(s.Path))); err == nil {
		info.SizeBytes = st.Size()
		info.UpdatedAt = st.ModTime().Format("2006-01-02")
	}
	return info
}

func nativeSkillPath(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if !skillNameRe.MatchString(n) {
		return "", fmt.Errorf("nome de skill inválido %q: use letras minúsculas, números, ponto, hífen ou underline", name)
	}
	return filepath.Join(NativeSkillsDir(), n, "SKILL.md"), nil
}

// ReadNativeSkill devolve o corpo (markdown) de uma skill nativa para edição.
func ReadNativeSkill(name string) (string, error) {
	p, err := nativeSkillPath(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("skill nativa %q não encontrada: %w", name, err)
	}
	return string(data), nil
}

// SaveNativeSkill cria ou atualiza uma skill nativa da IDE a partir do markdown do editor.
func SaveNativeSkill(name, body string) error {
	p, err := nativeSkillPath(name)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return errors.New("o corpo da skill (SKILL.md) não pode ficar vazio")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(trimmed+"\n"), 0o644)
}

// DeleteNativeSkill remove uma skill nativa da IDE (mantém intactas as skills do projeto).
func DeleteNativeSkill(name string) error {
	p, err := nativeSkillPath(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("skill nativa %q não encontrada", name)
	}
	return os.RemoveAll(dir)
}

// InstallSkillToProject copia uma skill nativa da IDE para o catálogo do projeto
// (.agents/skills/<nome>/), sobrescrevendo se já existir. O catálogo passa a
// valer também fora da IDE e sobrevive em worktrees via Git (se commitado).
func InstallSkillToProject(name, projectRoot string) error {
	p, err := nativeSkillPath(name)
	if err != nil {
		return err
	}
	srcDir := filepath.Dir(p)
	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("skill nativa %q não encontrada", name)
	}
	dstDir := filepath.Join(projectRoot, skillx.SkillsDir, strings.ToLower(strings.TrimSpace(name)))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	return copyDir(srcDir, dstDir)
}

// RemoveProjectSkill remove uma skill do catálogo do projeto (.agents/skills/<nome>/).
func RemoveProjectSkill(name, projectRoot string) error {
	n := strings.ToLower(strings.TrimSpace(name))
	if !skillNameRe.MatchString(n) {
		return fmt.Errorf("nome de skill inválido: %q", name)
	}
	dir := filepath.Join(projectRoot, skillx.SkillsDir, n)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("skill do projeto %q não encontrada", name)
	}
	return os.RemoveAll(dir)
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

// SetNativeSkillEnabled liga ou desliga uma skill nativa sem apagar o texto.
func SetNativeSkillEnabled(name string, enabled bool) error {
	return skillx.SetSkillEnabled(NativeSkillsDir(), name, enabled)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
