// Package version é a fonte única de versão dos binários do HiveMind
// (docs/specs/10-mobile-companion-supply-chain-and-ci.md §3.2).
//
// O valor canônico vive no arquivo VERSION na raiz do repositório. O build
// injeta via -ldflags:
//
//	go build -ldflags "-X github.com/gilvan-Borges/HiveMind/internal/version.Version=$(cat VERSION) -X github.com/gilvan-Borges/HiveMind/internal/version.Commit=<sha> -X github.com/gilvan-Borges/HiveMind/internal/version.BuildDate=<data>"
//
// O default em código só existe para builds de desenvolvimento sem ldflags; o
// teste TestVersionMatchesVersionFile impede que ele derive do arquivo VERSION.
package version

// Version is the HiveMind release version, injected at build time.
// Must match the VERSION file at the repository root.
var Version = "0.2.0"

// Commit is the short (or full) git SHA injected at build time.
var Commit = "dev"

// BuildDate is the RFC3339 build timestamp injected at build time.
var BuildDate = ""

// String renders "version (commit, date)" for `hivemind version`, the relay
// --version flag and the IDE about screen.
func String() string {
	out := Version
	if Commit != "" && Commit != "dev" {
		out += " (" + Commit
		if BuildDate != "" {
			out += ", " + BuildDate
		}
		out += ")"
	}
	return out
}

// IsInjected reports whether build metadata was injected via -ldflags.
func IsInjected() bool { return Commit != "dev" && Commit != "" }
