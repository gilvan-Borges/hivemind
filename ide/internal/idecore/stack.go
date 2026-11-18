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
func DetectStack(workspace string) ProjectStack {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	ps := ProjectStack{
		Root: abs,
		Name: filepath.Base(abs),
	}

	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(abs, rel))
		return err == nil
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

	if has("go.mod") {
		addStack("Go")
		addBuild("go")
		ps.TestHint = "go test ./..."
	}
	if has("pom.xml") {
		addStack("Java / Maven")
		addBuild("mvn")
		if ps.TestHint == "" {
			ps.TestHint = "mvn test"
		}
	}
	if has("build.gradle") || has("build.gradle.kts") {
		addStack("Java / Gradle")
		addBuild("gradle")
		if ps.TestHint == "" {
			ps.TestHint = "./gradlew test"
		}
	}
	if has("angular.json") {
		addStack("Angular / TypeScript")
		addBuild("ng / npm")
	}
	if has("package.json") {
		pkg := readPackageJSONDeps(filepath.Join(abs, "package.json"))
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
		if has("tsconfig.json") && !strings.Contains(strings.Join(ps.Stacks, ","), "TypeScript") {
			addStack("TypeScript")
		}
		addBuild("npm")
		if ps.TestHint == "" {
			ps.TestHint = "npm test"
		}
	}
	if has("pyproject.toml") || has("requirements.txt") {
		addStack("Python")
		addBuild("python / pytest")
		if ps.TestHint == "" {
			ps.TestHint = "pytest"
		}
	}
	if has("Cargo.toml") {
		addStack("Rust")
		addBuild("cargo")
		if ps.TestHint == "" {
			ps.TestHint = "cargo test"
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(abs, "*.sln")); len(matches) > 0 {
		addStack(".NET / C#")
		addBuild("dotnet")
	} else if matches, _ := filepath.Glob(filepath.Join(abs, "*.csproj")); len(matches) > 0 {
		addStack(".NET / C#")
		addBuild("dotnet")
	}
	if has("Dockerfile") || has("docker-compose.yml") || has("compose.yaml") {
		addBuild("Docker")
	}

	if len(ps.Stacks) == 0 {
		ps.Summary = "Stack genérica / não identificada"
	} else {
		ps.Summary = strings.Join(ps.Stacks, " + ")
	}
	return ps
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
