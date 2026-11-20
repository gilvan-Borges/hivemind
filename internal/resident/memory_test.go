package resident

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryStoreIsolatesAccounts(t *testing.T) {
	base := t.TempDir()

	acctA := NewMemoryStore(base, "alice@empresa.com")
	acctB := NewMemoryStore(base, "bob@empresa.com")
	local := NewMemoryStore(base, "")

	if acctA.AccountDir() == acctB.AccountDir() || acctA.AccountDir() == local.AccountDir() {
		t.Fatalf("contas compartilharam o mesmo diretório: A=%s B=%s local=%s", acctA.AccountDir(), acctB.AccountDir(), local.AccountDir())
	}

	if _, err := acctA.AddFact("projeto-x", "Alice prefere respostas em tópicos curtos", false); err != nil {
		t.Fatalf("AddFact A: %v", err)
	}
	if _, err := acctA.AddFact("geral", "Pin exclusivo da Alice", true); err != nil {
		t.Fatalf("AddFact pin A: %v", err)
	}
	if _, err := acctB.AddFact("projeto-x", "Bob trabalha no módulo de pagamentos", false); err != nil {
		t.Fatalf("AddFact B: %v", err)
	}

	// Bob must NEVER see Alice's facts or pins.
	resB, err := acctB.Retrieve("projeto-x", "Alice tópicos pagamentos")
	if err != nil {
		t.Fatalf("Retrieve B: %v", err)
	}
	for _, f := range append(resB.Pinned, resB.Matches...) {
		if strings.Contains(f.Text, "Alice") {
			t.Fatalf("vazamento de memória entre contas: Bob recebeu fato da Alice: %+v", f)
		}
	}
	if len(resB.Matches) != 1 || !strings.Contains(resB.Matches[0].Text, "pagamentos") {
		t.Fatalf("Bob deveria ver apenas seu próprio fato: %+v", resB.Matches)
	}

	// Local (unlinked) must see neither Alice nor Bob.
	resLocal, err := local.Retrieve("projeto-x", "Alice Bob pagamentos")
	if err != nil {
		t.Fatalf("Retrieve local: %v", err)
	}
	if len(resLocal.Pinned) != 0 || len(resLocal.Matches) != 0 {
		t.Fatalf("conta local não deveria ver fatos de contas vinculadas: %+v", resLocal)
	}

	// Switching back to Alice still preserves her facts intact.
	acctAReloaded := NewMemoryStore(base, "alice@empresa.com")
	factsA, err := acctAReloaded.ListFacts("")
	if err != nil {
		t.Fatalf("ListFacts A reloaded: %v", err)
	}
	if len(factsA) != 2 {
		t.Fatalf("esperado 2 fatos preservados para Alice, veio %d", len(factsA))
	}

	// Path traversal in accountId must stay inside baseDir.
	evil := NewMemoryStore(base, "../../etc/passwd")
	rel, err := filepath.Rel(base, evil.AccountDir())
	if err != nil || strings.HasPrefix(rel, "..") || strings.ContainsAny(rel, `/\`) {
		t.Fatalf("SanitizeAccountID escapou do baseDir: dir=%s rel=%s err=%v", evil.AccountDir(), rel, err)
	}
}

func TestMemoryStoreEnforcesBudgetsAndRedaction(t *testing.T) {
	base := t.TempDir()
	store := NewMemoryStore(base, "local")

	// Secrets must be redacted before persisting to memory.jsonl.
	fact, err := store.AddFact("geral", "Minha chave é sk-or-v1-1234567890abcdef123456", false)
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	if strings.Contains(fact.Text, "1234567890abcdef123456") {
		t.Fatalf("segredo não foi redigido na memória pessoal: %q", fact.Text)
	}

	// Insert 30 facts and verify retrieval caps at <= 8 items and <= 4 KiB.
	for i := 0; i < 30; i++ {
		txt := fmt.Sprintf("Fato %d sobre arquitetura hivemind relay %s", i, strings.Repeat("detalhe ", 40))
		if _, err := store.AddFact("geral", txt, false); err != nil {
			t.Fatalf("AddFact %d: %v", i, err)
		}
	}
	ret, err := store.Retrieve("geral", "arquitetura hivemind relay")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(ret.Matches) > MaxMemoryItems {
		t.Fatalf("retornou %d trechos, acima do teto %d", len(ret.Matches), MaxMemoryItems)
	}
	totalMatchBytes := 0
	for _, f := range ret.Matches {
		totalMatchBytes += len(f.Text)
	}
	if totalMatchBytes > MaxMemoryBytes {
		t.Fatalf("memória recuperada somou %d bytes, acima do teto %d", totalMatchBytes, MaxMemoryBytes)
	}

	// Session isolation between accounts.
	other := NewMemoryStore(base, "outra@conta.com")
	if err := store.AppendSessionTurn("geral", "oi local", "resposta local"); err != nil {
		t.Fatalf("AppendSessionTurn: %v", err)
	}
	otherMsgs, err := other.LoadSessionMessages("geral", 10)
	if err != nil {
		t.Fatalf("LoadSessionMessages other: %v", err)
	}
	if len(otherMsgs) != 0 {
		t.Fatalf("outra conta leu sessão alheia: %+v", otherMsgs)
	}
}
