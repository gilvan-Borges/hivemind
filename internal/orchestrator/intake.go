package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// intakeInlineMaxBytes é o teto do objetivo na primeira mensagem user do
	// turno. Acima disso o prompt fica com cabeça+cauda inline e o miolo é
	// substituído por um marcador apontando o arquivo integral.
	intakeInlineMaxBytes = 8 << 10
	// intakeHeadBytes / intakeTailBytes são as fatias preservadas inline: a
	// intenção do usuário costuma estar no começo E no fim do texto.
	intakeHeadBytes = 6 << 10
	intakeTailBytes = 2 << 10
	// intakeTextAttachmentLimit é o teto de um anexo textual inline; maiores
	// viram arquivo e o prompt recebe só a nota com o caminho.
	intakeTextAttachmentLimit = 4 << 10
)

// IntakeDir guarda, relativo ao workspace, o conteúdo removido do prompt
// inicial para leitura seletiva (read_file) pelos agentes. Vive sob
// .hivemind/spillover/intake para reutilizar o carve-out escopado do jail
// (workspace.Options.AllowSpilloverRead + pathsec.IsSpilloverPath), mantendo
// team.yaml/sessions/worktrees bloqueados enquanto permite que read_file leia
// o arquivo de intake sem erro de permissão.
const IntakeDir = ".hivemind/spillover/intake"

func intakeHash8(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:8]
}

func isTextualMIME(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if m == "" {
		return false
	}
	return strings.HasPrefix(m, "text/") ||
		strings.Contains(m, "json") ||
		strings.Contains(m, "xml") ||
		strings.Contains(m, "csv") ||
		strings.Contains(m, "yaml")
}

// runeSafeHead devolve os primeiros n bytes sem quebrar um rune no fim.
func runeSafeHead(s string, n int) string {
	if n >= len(s) {
		return s
	}
	head := s[:n]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	return head
}

// runeSafeTail devolve os últimos n bytes sem quebrar um rune no começo.
func runeSafeTail(s string, n int) string {
	if n >= len(s) {
		return s
	}
	tail := s[len(s)-n:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return tail
}

func (r *Runner) writeIntakeFile(name string, data []byte) (string, bool) {
	if strings.TrimSpace(r.Workspace) == "" {
		return "", false
	}
	dir := filepath.Join(r.Workspace, filepath.FromSlash(IntakeDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(name)), data, 0o600); err != nil {
		return "", false
	}
	return IntakeDir + "/" + filepath.Base(name), true
}

// boundIntake ajusta o objetivo e os anexos ao teto de contexto da primeira
// mensagem do turno (a que o Chefe vê na triagem). Objetivo acima de 8 KiB
// fica com cabeça+cauda inline e o miolo vira arquivo em .hivemind/intake/;
// anexo textual acima de 4 KiB vira arquivo com uma nota de uma linha no
// prompt. Falha ao gravar mantém o conteúdo inline — nunca se perde input do
// usuário, só se perde a economia.
func (r *Runner) boundIntake(objective string, atts []Attachment) (string, []Attachment) {
	var notes []string

	if len(objective) > intakeInlineMaxBytes {
		raw := objective
		if rel, ok := r.writeIntakeFile("objective-"+intakeHash8([]byte(raw))+".txt", []byte(raw)); ok {
			cut := len(raw) - intakeHeadBytes - intakeTailBytes
			objective = runeSafeHead(raw, intakeHeadBytes) +
				fmt.Sprintf("\n\n[TRECHO CENTRAL REMOVIDO PARA ECONOMIA DE CONTEXTO: o objetivo original tem %d bytes; %d bytes no meio foram substituídos por esta nota. O texto INTEGRAL está em `%s` — use read_file (start_line/end_line) apenas se realmente precisar do miolo antes de decidir; para triage e plano, o começo e o fim acima costumam bastar.]\n\n",
					len(raw), cut, rel) +
				runeSafeTail(raw, intakeTailBytes)
		}
	}

	out := make([]Attachment, 0, len(atts))
	for _, att := range atts {
		if isTextualMIME(att.MIMEType) && len(att.Data) > intakeTextAttachmentLimit {
			name := strings.TrimSpace(att.Name)
			if name == "" {
				name = "attachment-" + intakeHash8(att.Data)
			}
			if rel, ok := r.writeIntakeFile(name+".txt", att.Data); ok {
				notes = append(notes, fmt.Sprintf(
					"[Anexo %q (%s, %d bytes) salvo em `%s` — leia trechos com read_file apenas se precisar do conteúdo.]",
					strings.TrimSpace(att.Name), att.MIMEType, len(att.Data), rel))
				continue
			}
		}
		out = append(out, att)
	}

	if len(notes) > 0 {
		if objective != "" {
			objective += "\n\n"
		}
		objective += strings.Join(notes, "\n")
	}
	return objective, out
}
