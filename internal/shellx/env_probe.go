package shellx

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// MissingToolHint returns a concise PT-BR download link and installation hint
// for a missing CLI binary or SDK.
func MissingToolHint(bin string) string {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(bin)))
	name = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".bat"), ".cmd")
	switch name {
	case "flutter":
		return `Flutter SDK (https://docs.flutter.dev/get-started/install) ou, se o repo usar .fvmrc, instale o FVM (https://fvm.app/documentation/getting-started/installation — ex.: dart pub global activate fvm ou choco install fvm) e rode "fvm install" na raiz do projeto`
	case "dart":
		return `Dart / Flutter SDK (https://dart.dev/get-dart ou https://docs.flutter.dev/get-started/install)`
	case "fvm":
		return `FVM (https://fvm.app/documentation/getting-started/installation — Windows: choco install fvm ou dart pub global activate fvm; depois rode "fvm install" na raiz do projeto)`
	case "mvn", "mvnw":
		return `Apache Maven (https://maven.apache.org/download.cgi — Windows: winget install Apache.Maven)`
	case "java", "javac":
		return `OpenJDK (https://adoptium.net — Windows: winget install EclipseAdoptium.Temurin.21.JDK)`
	case "gradle", "gradlew":
		return `Gradle (https://gradle.org/install/ — Windows: winget install Gradle.Gradle)`
	case "node", "npm", "npx", "pnpm", "yarn":
		return `Node.js LTS (https://nodejs.org — Windows: winget install OpenJS.NodeJS.LTS)`
	case "python", "python3", "pytest":
		return `Python (https://www.python.org/downloads/ — Windows: winget install Python.Python.3.12) ou uv (https://docs.astral.sh/uv/ — winget install astral-sh.uv)`
	case "uv":
		return `uv (https://docs.astral.sh/uv/ — Windows: winget install astral-sh.uv)`
	case "go":
		return `Go (https://go.dev/dl/ — Windows: winget install GoLang.Go)`
	case "cargo", "rustc":
		return `Rustup (https://rustup.rs/ — Windows: winget install Rustlang.Rustup)`
	case "dotnet":
		return `.NET SDK (https://dotnet.microsoft.com/download — Windows: winget install Microsoft.DotNet.SDK.8)`
	case "docker":
		return `Docker Desktop (https://www.docker.com/products/docker-desktop/ — Windows: winget install Docker.DockerDesktop) ou iniciar o daemon do Docker`
	case "bash", "sh":
		return `Git for Windows / Git Bash (https://git-scm.com/downloads/win — Windows: winget install Git.Git)`
	default:
		return fmt.Sprintf(`instale %q e adicione ao PATH do sistema`, name)
	}
}

// FormatMissingExecutableDiagnostic returns a crisp guidance block appended when
// run_shell fails with ErrNotFound so the model stops immediately and asks the
// user instead of scanning the disk or editing manifests.
func FormatMissingExecutableDiagnostic(bin string, rawErr error) string {
	name := strings.TrimSpace(bin)
	if name == "" {
		name = "ferramenta"
	}
	return fmt.Sprintf(
		"[DIAGNÓSTICO DE AMBIENTE] Executável %q não encontrado no PATH.\n"+
			"Como instalar: %s\n"+
			"Ação obrigatória: NÃO faça varredura no disco procurando executáveis e NÃO edite manifestos do projeto (pubspec.yaml, package.json, pom.xml, etc.) para burlar versão. Informe ao usuário o que falta com o link/comando acima e pergunte se deseja instalar agora para concluir os testes ou publicar COMMENT.",
		name, MissingToolHint(name),
	)
}

// ProbeWorktreeEnvironment inspects dir (without spawning subprocesses) to map
// the worktree stack, version pins, and which required runtimes are available or
// missing on PATH.
func ProbeWorktreeEnvironment(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}

	var (
		stacks    []string
		stackSeen = map[string]bool{}
		pins      []string
		pinSeen   = map[string]bool{}
		reqTools  []string
		toolSeen  = map[string]bool{}
	)
	addStack := func(s string) {
		if s != "" && !stackSeen[s] {
			stackSeen[s] = true
			stacks = append(stacks, s)
		}
	}
	addPin := func(p string) {
		if p != "" && !pinSeen[p] {
			pinSeen[p] = true
			pins = append(pins, p)
		}
	}
	addTool := func(t string) {
		if t != "" && !toolSeen[t] {
			toolSeen[t] = true
			reqTools = append(reqTools, t)
		}
	}

	scanDir := func(target string) {
		has := func(rel string) bool {
			_, sErr := os.Stat(filepath.Join(target, rel))
			return sErr == nil
		}
		if has("pubspec.yaml") {
			data, _ := os.ReadFile(filepath.Join(target, "pubspec.yaml"))
			text := string(data)
			isFlutter := strings.Contains(text, "flutter:")
			if isFlutter {
				addStack("Flutter / Dart")
				addTool("flutter")
				addTool("dart")
			} else {
				addStack("Dart")
				addTool("dart")
			}
			if pin := extractPubspecFlutterPin(text); pin != "" {
				addPin("pubspec.yaml flutter=" + pin)
			}
			if fvmPin := readFvmrcPin(filepath.Join(target, ".fvmrc")); fvmPin != "" {
				addPin(".fvmrc flutter=" + fvmPin)
				addTool("fvm")
			} else if has(".fvmrc") {
				addTool("fvm")
			}
		}
		if has("pom.xml") {
			addStack("Java / Maven")
			addTool("mvn")
			addTool("java")
		}
		if has("build.gradle") || has("build.gradle.kts") {
			addStack("Java / Gradle")
			addTool("java")
			if !has("gradlew") && !has("gradlew.bat") {
				addTool("gradle")
			}
		}
		if has("go.mod") {
			addStack("Go")
			addTool("go")
		}
		if has("package.json") {
			addStack("Node.js / TypeScript")
			addTool("node")
			addTool("npm")
		}
		if has("pyproject.toml") || has("requirements.txt") {
			addStack("Python")
			addTool("python")
			addTool("uv")
		}
		if has("Cargo.toml") {
			addStack("Rust")
			addTool("cargo")
		}
		if m, _ := filepath.Glob(filepath.Join(target, "*.sln")); len(m) > 0 {
			addStack(".NET / C#")
			addTool("dotnet")
		} else if m, _ := filepath.Glob(filepath.Join(target, "*.csproj")); len(m) > 0 {
			addStack(".NET / C#")
			addTool("dotnet")
		}
		if has("Dockerfile") || has("docker-compose.yml") || has("compose.yaml") {
			addTool("docker")
		}
	}

	scanDir(dir)
	if len(stacks) == 0 {
		if entries, readErr := os.ReadDir(dir); readErr == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target" || name == "build" || name == "dist" || name == "docs" || name == "specs" {
					continue
				}
				scanDir(filepath.Join(dir, name))
			}
		}
	}

	if len(stacks) == 0 && len(reqTools) == 0 {
		return ""
	}

	var available, missing []string
	// Probe python/uv together cleanly: if uv is present, python tooling is satisfied,
	// and likewise python3 satisfies python.
	hasPython := false
	if _, err := exec.LookPath("python"); err == nil {
		hasPython = true
	} else if _, err := exec.LookPath("python3"); err == nil {
		hasPython = true
	}
	_, uvErr := exec.LookPath("uv")
	hasUV := uvErr == nil

	for _, toolName := range reqTools {
		switch toolName {
		case "python":
			if hasPython {
				available = append(available, "python")
			} else if !hasUV {
				missing = append(missing, "python -> "+MissingToolHint("python"))
			}
		case "uv":
			if hasUV {
				available = append(available, "uv")
			} else if !hasPython {
				missing = append(missing, "uv -> "+MissingToolHint("uv"))
			}
		default:
			if p, err := exec.LookPath(toolName); err == nil && strings.TrimSpace(p) != "" {
				available = append(available, toolName)
			} else {
				missing = append(missing, toolName+" -> "+MissingToolHint(toolName))
			}
		}
	}

	// Report bash status (including automatic Git Bash fallback on Windows).
	if p, err := exec.LookPath("bash"); err == nil && !isBrokenWindowsBashStub(p) {
		available = append(available, "bash")
	} else if runtime.GOOS == "windows" {
		if gb := findGitBash(); gb != "" {
			available = append(available, "bash (Git Bash resolvido automaticamente)")
		} else {
			missing = append(missing, "bash -> "+MissingToolHint("bash"))
		}
	} else {
		missing = append(missing, "bash -> "+MissingToolHint("bash"))
	}

	var b strings.Builder
	b.WriteString("[Ambiente da Worktree]\n")
	if len(stacks) > 0 {
		b.WriteString("Stack da worktree: " + strings.Join(stacks, " + ") + "\n")
	}
	if len(pins) > 0 {
		b.WriteString("Travas de versão detectadas: " + strings.Join(pins, ", ") + "\n")
	}
	if len(available) > 0 {
		b.WriteString("Ferramentas disponíveis no PATH: " + strings.Join(available, ", ") + "\n")
	}
	if len(missing) > 0 {
		b.WriteString("Ferramentas AUSENTES no PATH:\n")
		for _, m := range missing {
			b.WriteString("- " + m + "\n")
		}
	}
	b.WriteString("Regra de ambiente: se faltar ferramenta ou a versão exata exigida pelo PR, pare na 1ª tentativa (NÃO cace no disco e NÃO edite arquivos/manifestos do PR), informe o usuário com o link/comando de instalação e pergunte se deseja instalar para rodar os testes ou publicar COMMENT.\n")
	return b.String()
}

func extractPubspecFlutterPin(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	inEnv := false
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
			inEnv = strings.HasPrefix(trimmed, "environment:")
			continue
		}
		if !inEnv {
			continue
		}
		if strings.HasPrefix(trimmed, "flutter:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "flutter:"))
			if idx := strings.Index(val, "#"); idx >= 0 {
				val = strings.TrimSpace(val[:idx])
			}
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

func readFvmrcPin(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return ""
	}
	var parsed struct {
		Flutter string `json:"flutter"`
	}
	if json.Unmarshal([]byte(raw), &parsed) == nil && strings.TrimSpace(parsed.Flutter) != "" {
		return strings.TrimSpace(parsed.Flutter)
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "{") {
			return strings.Trim(line, `"'`)
		}
	}
	return ""
}
