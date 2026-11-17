package team

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Path is the project configuration file.
func Path(workspace string) string {
	return filepath.Join(workspace, ".hivemind", "team.yaml")
}

// Load reads the team file. A missing file returns an error that wraps os.ErrNotExist.
func Load(workspace string) (File, error) {
	path := Path(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("ler %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("interpretar %s: %w", path, err)
	}
	f.Normalize()
	for _, t := range f.Teams {
		if err := t.Validate(); err != nil {
			return File{}, err
		}
	}
	return f, nil
}

// Save writes the team file, creating .hivemind when needed.
func Save(workspace string, f File) error {
	f.Normalize()
	seen := map[string]struct{}{}
	for _, t := range f.Teams {
		if err := t.Validate(); err != nil {
			return err
		}
		if _, ok := seen[t.Name]; ok {
			return fmt.Errorf("time duplicado: %s", t.Name)
		}
		seen[t.Name] = struct{}{}
	}
	if f.Active != "" {
		if _, ok := seen[f.Active]; !ok {
			return fmt.Errorf("time ativo %q não existe", f.Active)
		}
	}
	dir := filepath.Join(workspace, ".hivemind")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	path := Path(workspace)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// UpsertTeam adds or replaces a team and makes it active.
func UpsertTeam(f *File, t Team) {
	for i := range f.Teams {
		if f.Teams[i].Name == t.Name {
			f.Teams[i] = t
			f.Active = t.Name
			return
		}
	}
	f.Teams = append(f.Teams, t)
	f.Active = t.Name
}
