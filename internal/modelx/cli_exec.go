package modelx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

// cli_exec.go é o ÚNICO ponto de spawn de CLIs de modelo no modelx (Spec 06
// §3.1: agy.go, stream.go e cursor.go duplicavam o wrap .cmd/.bat e o leitor
// NDJSON com EOF-antes-de-Wait).

// cliCommand wraps .cmd/.bat scripts on Windows (they cannot be exec'd
// directly) and returns the configured *exec.Cmd for every other binary.
func cliCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(bin))
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && (ext == ".cmd" || ext == ".bat") {
		wrapped := make([]string, 0, len(args)+3)
		wrapped = append(wrapped, "/d", "/c", bin)
		wrapped = append(wrapped, args...)
		cmd = exec.CommandContext(ctx, "cmd.exe", wrapped...)
	} else {
		cmd = exec.CommandContext(ctx, bin, args...)
	}
	procx.ConfigureContext(cmd)
	return cmd
}

const maxCLILine = 1024 * 1024

var errStopCLIStream = errors.New("stream interrompido")

// readCLIStream lê até o EOF, linha a linha, descartando linhas maiores que
// max mas SEMPARAR de drenar o pipe (um tool_call com corpo de arquivo passa
// de 4 MiB; parar o scanner trava o processo filho até o timeout). handle
// devolve false para encerrar a leitura (errStopCLIStream).
func readCLIStream(r io.Reader, max int, handle func(line string) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if skipping {
			if err == bufio.ErrBufferFull {
				continue
			}
			skipping = false
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			continue
		}
		if err == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > max {
				buf = nil
				skipping = true
				continue
			}
			buf = append(buf, chunk...)
			continue
		}
		if len(chunk) > 0 {
			buf = append(buf, chunk...)
		}
		if len(buf) > 0 && len(buf) <= max {
			line := string(buf)
			buf = nil
			if !handle(line) {
				return errStopCLIStream
			}
		} else {
			buf = nil
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// limitWriter capta no máximo n bytes de stderr (linhas de erro gigantes não
// inflam a memória do processo pai).
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	orig := len(p)
	if l.n <= 0 || orig == 0 {
		return orig, nil
	}
	write := p
	if len(write) > l.n {
		write = write[:l.n]
	}
	n, err := l.w.Write(write)
	l.n -= n
	if err != nil || n < len(write) {
		return n, err
	}
	return orig, nil
}
