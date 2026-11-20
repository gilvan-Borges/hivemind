package shellres

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestShortcutIsNotCtrlK(t *testing.T) {
	if PaletteShortcut == "Ctrl+K" || strings.Contains(PaletteShortcut, "Ctrl+K") {
		t.Fatalf("atalho da casca não pode ser Ctrl+K: %s", PaletteShortcut)
	}
	if PaletteShortcut != "Ctrl+Alt+Shift+H" {
		t.Fatalf("atalho: %s", PaletteShortcut)
	}
}

func TestLinuxDoesNotCreateCore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	calls := 0
	err := Run(context.Background(), func() (map[string]Handler, error) {
		calls++
		return nil, nil
	})
	if err != ErrUnavailable || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDeliverKeyDoesNotWriteFile(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	_, err = DeliverKey(func([]byte) error { return ErrNoHandle })
	if err == nil {
		t.Fatal("handle duplicado falhou e a fase deveria parar")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("chave caiu em arquivo: %v", entries)
	}
}

func TestExecCommandStillGuarded(t *testing.T) {
	_, err := Dispatch(context.Background(), "exec_command", json.RawMessage(`{"argv":["sh","-c","echo hi"]}`), nil)
	if err == nil {
		t.Fatal("exec_command deveria ser recusado")
	}
	if strings.Contains(err.Error(), "casca não executa") {
		t.Fatal("passou da validação do argv")
	}
}

func TestStatusRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	var key [32]byte
	key[0] = 7
	methods := map[string]Handler{
		"ResidentStatus": func(context.Context, json.RawMessage) (any, error) {
			return map[string]string{"state": "ocioso"}, nil
		},
	}
	errc := make(chan error, 1)
	go func() { errc <- Serve(context.Background(), left, &key, methods) }()
	nonce, box, err := sealBox(&key, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(right, frame{Method: "ResidentStatus", Nonce: nonce, Box: box}); err != nil {
		t.Fatal(err)
	}
	raw, err := readFrame(right)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Nonce string `json:"nonce"`
		Box   string `json:"box"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Error != "" {
		t.Fatalf("resposta: %+v", resp)
	}
	opened, err := openBox(&key, resp.Nonce, resp.Box)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(opened), "ocioso") {
		t.Fatalf("status: %s", opened)
	}
	_ = right.Close()
	<-errc
}
