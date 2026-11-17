package team

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndRoles(t *testing.T) {
	dir := t.TempDir()
	f := File{
		Active: "alfa",
		Settings: Settings{
			JiraEnabled: true,
			AutoCommit:  true,
			MaxQALoops:  2,
		},
		Teams: []Team{DefaultTeam("alfa", "Time principal", "gemini-flash-latest", "gemini", 2)},
	}
	f.Teams = append(f.Teams, DefaultTeam("beta", "", "claude-sonnet-4-5", "anthropic", 1))
	if err := Save(dir, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active != "alfa" || len(got.Teams) != 2 {
		t.Fatalf("file = %+v", got)
	}
	active, err := got.ActiveTeam("")
	if err != nil {
		t.Fatal(err)
	}
	if err := active.ValidateForRun(); err != nil {
		t.Fatal(err)
	}
	if n := len(active.ByRole(RoleDeveloper)); n != 2 {
		t.Fatalf("devs = %d", n)
	}
	if got.Teams[1].Agents[0].Provider != "anthropic" {
		t.Fatalf("provider inferred wrong: %+v", got.Teams[1].Agents[0])
	}
	if _, err := os.Stat(filepath.Join(dir, ".hivemind", "team.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeRole(t *testing.T) {
	if NormalizeRole("dev") != RoleDeveloper {
		t.Fatal("dev")
	}
	if NormalizeRole("PM") != RoleProjectManager {
		t.Fatal("pm")
	}
	if NormalizeRole("nope") != "" {
		t.Fatal("expected empty")
	}
}

func TestExampleFile(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "team.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hivemind"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	active, err := f.ActiveTeam("")
	if err != nil {
		t.Fatal(err)
	}
	if err := active.ValidateForRun(); err != nil {
		t.Fatal(err)
	}
}
