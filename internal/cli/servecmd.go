package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// newServeCmd builds the `hivemind serve` subcommand (Marco 3 da
// SPEC_APP_MOBILE_NATIVO.md): keeps the Remote Bridge running in the
// background without the IDE window, so the mobile app can keep commanding
// teams while the GUI is closed.
func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Mantém o Remote Bridge (App Mobile) rodando sem a janela da IDE",
		Long: `Roda o Remote Bridge em segundo plano no PC para o App Mobile continuar
e comandar os trabalhos do HiveMind pelo celular — sem precisar deixar a
janela gráfica da IDE aberta.

O comando localiza o binário da HiveMind IDE (hivemind-ide) e o executa em
modo headless (--serve-bridge). O QR de pareamento é exibido no terminal.
Pressione Ctrl+C para encerrar.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := locateIDEBinary()
			if err != nil {
				return err
			}
			child := exec.CommandContext(cmd.Context(), exe, "--serve-bridge")
			child.Stdin = cmd.InOrStdin()
			child.Stdout = cmd.OutOrStdout()
			child.Stderr = cmd.ErrOrStderr()
			if err := child.Start(); err != nil {
				return fmt.Errorf("falha ao iniciar %s: %w", exe, err)
			}
			return child.Wait()
		},
	}
	return cmd
}

// locateIDEBinary finds the hivemind-ide executable. Search order:
//  1. Same directory as the running hivemind binary (installed side-by-side).
//  2. <repo>/ide/build/bin/hivemind-ide (wails build output, dev layout).
//  3. %LOCALAPPDATA%\Programs\hivemind-ide\hivemind-ide.exe (wails installer).
//  4. PATH.
func locateIDEBinary() (string, error) {
	name := "hivemind-ide"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(dir, "ide", "build", "bin", name),
		)
	}
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		candidates = append(candidates, filepath.Join(base, "Programs", "hivemind-ide", name))
	}
	if p, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf(
		"binário da HiveMind IDE não encontrado (%s). Compile a IDE primeiro (cd ide && wails build) ou adicione o %s ao PATH",
		name, name,
	)
}
