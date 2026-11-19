package idecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ProjectStack summarizes detected languages, frameworks, and build tools in a workspace.
type ProjectStack struct {
	Root       string   `json:"root"`
	Name       string   `json:"name"`
	Stacks     []string `json:"stacks"`
	BuildTools []string `json:"buildTools"`
	TestHint   string   `json:"testHint"`
	Summary    string   `json:"summary"`
}

// DetectStack inspects workspace marker files to identify the project's technology stack.
// When the repository root has no language/framework manifests (common in monorepos),
// it also scans immediate first-level subdirectories while skipping ignored/hidden dirs.
func DetectStack(workspace string) ProjectStack {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	ps := ProjectStack{
		Root: abs,
		Name: filepath.Base(abs),
	}

	addStack := func(s string) {
		for _, ex := range ps.Stacks {
			if ex == s {
				return
			}
		}
		ps.Stacks = append(ps.Stacks, s)
	}
	addBuild := func(b string) {
		for _, ex := range ps.BuildTools {
			if ex == b {
				return
			}
		}
		ps.BuildTools = append(ps.BuildTools, b)
	}
	setTestHint := func(hint string) {
		if ps.TestHint == "" && hint != "" {
			ps.TestHint = hint
		}
	}

	inspectStackDir(abs, "", addStack, addBuild, setTestHint)

	// Monorepo fallback: if no primary stack was detected at the root, inspect
	// first-level non-ignored subdirectories (e.g. api/, web/, backend/, frontend/).
	if len(ps.Stacks) == 0 {
		if entries, readErr := os.ReadDir(abs); readErr == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasPrefix(name, ".") || ignoredDirs[name] || name == "vendor" || name == "docs" || name == "specs" {
					continue
				}
				inspectStackDir(filepath.Join(abs, name), name, addStack, addBuild, setTestHint)
			}
		}
	}

	if len(ps.Stacks) == 0 {
		ps.Summary = "Stack genérica / não identificada"
	} else {
		ps.Summary = strings.Join(ps.Stacks, " + ")
	}
	return ps
}

func inspectStackDir(dir, relPrefix string, addStack, addBuild, setTestHint func(string)) {
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, rel))
		return err == nil
	}
	hintFor := func(cmd string) string {
		if relPrefix == "" {
			return cmd
		}
		return "cd " + relPrefix + " && " + cmd
	}

	if has("go.mod") {
		addStack("Go")
		addBuild("go")
		setTestHint(hintFor("go test ./..."))
	}
	if has("pom.xml") {
		addStack("Java / Maven")
		addBuild("mvn")
		setTestHint(hintFor("mvn test"))
	}
	if has("build.gradle") || has("build.gradle.kts") {
		addStack("Java / Gradle")
		addBuild("gradle")
		setTestHint(hintFor("./gradlew test"))
	}
	if has("angular.json") {
		addStack("Angular / TypeScript")
		addBuild("ng / npm")
	}
	if has("package.json") {
		pkg := readPackageJSONDeps(filepath.Join(dir, "package.json"))
		switch {
		case strings.Contains(pkg, "@angular/core"):
			addStack("Angular / Node.js")
		case strings.Contains(pkg, "next"):
			addStack("Next.js / React")
		case strings.Contains(pkg, "react"):
			addStack("React / Node.js")
		case strings.Contains(pkg, "vue"):
			addStack("Vue / Node.js")
		default:
			addStack("Node.js")
		}
		if has("tsconfig.json") {
			addStack("TypeScript")
		}
		addBuild("npm")
		setTestHint(hintFor("npm test"))
	}
	if has("pyproject.toml") || has("requirements.txt") {
		addStack("Python")
		addBuild("python / pytest")
		setTestHint(hintFor("pytest"))
	}
	if has("Cargo.toml") {
		addStack("Rust")
		addBuild("cargo")
		setTestHint(hintFor("cargo test"))
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.sln")); len(matches) > 0 {
		addStack(".NET / C#")
		addBuild("dotnet")
	} else if matches, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(matches) > 0 {
		addStack(".NET / C#")
		addBuild("dotnet")
	}
	if has("Dockerfile") || has("docker-compose.yml") || has("compose.yaml") {
		addBuild("Docker")
	}
}

// FormatForChief returns a concise context block injected into the Chief's prompt.
func (ps ProjectStack) FormatForChief() string {
	var b strings.Builder
	b.WriteString("[Contexto do Projeto]\n")
	b.WriteString("Projeto: " + ps.Name + "\n")
	b.WriteString("Stack detectada: " + ps.Summary + "\n")
	if len(ps.BuildTools) > 0 {
		b.WriteString("Ferramentas de build: " + strings.Join(ps.BuildTools, ", ") + "\n")
	}
	if ps.TestHint != "" {
		b.WriteString("Comando sugerido de testes: " + ps.TestHint + "\n")
	}
	return b.String()
}

func readPackageJSONDeps(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var parsed struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return string(data)
	}
	var keys []string
	for k := range parsed.Dependencies {
		keys = append(keys, k)
	}
	for k := range parsed.DevDependencies {
		keys = append(keys, k)
	}
	return strings.Join(keys, " ")
}
