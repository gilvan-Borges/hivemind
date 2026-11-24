package browserx

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExtensionIDMatchesManifestKey(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "extension", "chrome", "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	der, err := base64.StdEncoding.DecodeString(manifest.Key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	const alpha = "abcdefghijklmnop"
	id := make([]byte, 0, 32)
	for i := 0; i < 16; i++ {
		id = append(id, alpha[sum[i]>>4], alpha[sum[i]&0x0f])
	}
	if string(id) != ExtensionID {
		t.Fatalf("id %s não bate com a chave do manifesto (%s)", ExtensionID, id)
	}
}
