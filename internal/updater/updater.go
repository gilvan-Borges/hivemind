// Package updater verifica e baixa atualizações do HiveMind a partir de
// GitHub Releases (docs/SPEC_ATUALIZACAO.md Fase 9 e docs/specs/10-mobile-
// companion-supply-chain-and-ci.md §3.2).
//
// Contrato do release (gerado por scripts/build-installer.ps1 em dist/):
//
//	manifest.json   {"version","commit","buildDate","artifacts":{"installer":{"name","sha256","size"}}}
//	checksums.txt   "<sha256>  <arquivo>" por linha, formato sha256sum
//
// O verificador nunca executa nada: baixa o artefato, confere o SHA-256 e
// devolve o caminho para o chamador decidir (a IDE chama no pós-merge da
// Onda 5; nada aqui sobrepõe a decisão da pessoa).
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL points at the "latest" release assets of the HiveMind repo.
const DefaultBaseURL = "https://github.com/gilvan-Borges/HiveMind/releases/latest/download"

const (
	maxManifestBytes int64 = 1 << 20   // 1 MiB: manifestos maiores são suspeitos.
	maxArtifactBytes int64 = 512 << 20 // 512 MiB: instalador jamais passa disso.
)

// Artifact is one downloadable file described by the release manifest.
type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

// Release is the parsed manifest.json of a GitHub release.
type Release struct {
	Version   string              `json:"version"`
	Commit    string              `json:"commit,omitempty"`
	BuildDate string              `json:"buildDate,omitempty"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

// Update is a release strictly newer than the running version, with the
// artifact the caller asked about resolved to a downloadable URL.
type Update struct {
	Release  Release
	Artifact Artifact
	URL      string
}

// Client fetches and verifies releases. Zero value is unusable: use New.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New builds a Client with sane defaults.
func New(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// ErrNoUpdate means the running version is already the latest known.
var ErrNoUpdate = errors.New("atualização não disponível: versão instalada já é a mais recente")

// FetchRelease downloads and parses manifest.json from the release.
func (c *Client) FetchRelease(ctx context.Context) (*Release, error) {
	body, err := c.get(ctx, c.BaseURL+"/manifest.json", maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("ler manifesto do release: %w", err)
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("ler manifesto do release: %w", err)
	}
	var rel Release
	if err := json.Unmarshal(raw, &rel); err != nil {
		return nil, fmt.Errorf("manifesto inválido: %w", err)
	}
	if strings.TrimSpace(rel.Version) == "" {
		return nil, errors.New("manifesto sem versão")
	}
	return &rel, nil
}

// Check compares the running version with the latest release and returns the
// update to perform, or ErrNoUpdate when nothing newer exists.
func (c *Client) Check(ctx context.Context, currentVersion, artifact string) (*Update, error) {
	rel, err := c.FetchRelease(ctx)
	if err != nil {
		return nil, err
	}
	if !IsNewer(currentVersion, rel.Version) {
		return nil, ErrNoUpdate
	}
	art, ok := rel.Artifacts[artifact]
	if !ok {
		return nil, fmt.Errorf("release %s sem artefato %q", rel.Version, artifact)
	}
	if !isHex64(art.SHA256) {
		return nil, fmt.Errorf("artefato %q sem SHA-256 válido no manifesto", artifact)
	}
	return &Update{
		Release:  *rel,
		Artifact: art,
		URL:      c.BaseURL + "/" + art.Name,
	}, nil
}

// DownloadArtifact streams the update's installer to destDir, verifying the
// SHA-256 advertised in the manifest before the file is left in place. A
// mismatch removes the partial file and fails.
func (c *Client) DownloadArtifact(ctx context.Context, upd *Update, destDir string) (string, error) {
	if upd == nil || upd.Artifact.Name == "" {
		return "", errors.New("atualização sem artefato")
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fmt.Errorf("criar %s: %w", destDir, err)
	}
	dest := filepath.Join(destDir, upd.Artifact.Name)
	partial := dest + ".part"
	if err := downloadTo(ctx, c.HTTP, upd.URL, partial, upd.Artifact.Size); err != nil {
		os.Remove(partial)
		return "", err
	}
	if err := VerifyFileSHA256(partial, upd.Artifact.SHA256); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("artefato baixado não confere: %w", err)
	}
	if err := os.Rename(partial, dest); err != nil {
		os.Remove(partial)
		return "", fmt.Errorf("finalizar download: %w", err)
	}
	return dest, nil
}

// VerifyFileSHA256 hashes path and compares with the expected hex digest.
func VerifyFileSHA256(path, wantHex string) error {
	want := strings.ToLower(strings.TrimSpace(wantHex))
	if !isHex64(want) {
		return errors.New("digest esperado não é SHA-256 hex de 64 caracteres")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("SHA-256 divergente: arquivo %s, manifesto %s", got, want)
	}
	return nil
}

// ParseChecksums reads a sha256sum-style file ("<hash>  <name>" per line) and
// returns name -> lowercase hex hash. Binary-mode markers ("*name") are
// tolerated. Used to cross-check manifest hashes by hand.
func ParseChecksums(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("linha %d de checksums.txt malformada", i+1)
		}
		hash, name := fields[0], strings.TrimPrefix(fields[1], "*")
		if !isHex64(hash) {
			return nil, fmt.Errorf("linha %d de checksums.txt sem SHA-256 válido", i+1)
		}
		out[name] = strings.ToLower(hash)
	}
	return out, nil
}

// IsNewer reports whether candidate is strictly newer than current using a
// tolerant semver comparison: "v" prefix, "x.y.z" with numeric parts and a
// free-form pre-release suffix. Numeric parts compare numerically (0.10 > 0.9).
func IsNewer(current, candidate string) bool {
	if strings.TrimSpace(candidate) == "" {
		return false
	}
	if strings.TrimSpace(current) == "" {
		return true
	}
	c := splitVersion(current)
	n := splitVersion(candidate)
	for i := 0; i < 3; i++ {
		cn, cerr := strconv.Atoi(c[i])
		nn, nerr := strconv.Atoi(n[i])
		if cerr == nil && nerr == nil {
			if nn != cn {
				return nn > cn
			}
			continue
		}
		if n[i] != c[i] {
			return n[i] > c[i]
		}
	}
	// Mesmos três números: sufixo novo (ex. "0.2.1-beta" vs "0.2.1") não
	// conta como atualização — release final sempre ganha do sufixo.
	return false
}

// splitVersion extracts the three dotted components of "vX.Y.Z-suffix",
// stripping any pre-release suffix per component.
func splitVersion(v string) [3]string {
	v = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(v)), "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]string
	for i := range out {
		if i < len(parts) {
			out[i] = strings.TrimSpace(strings.SplitN(parts[i], "-", 2)[0])
		} else {
			out[i] = "0"
		}
	}
	return out
}

func (c *Client) get(ctx context.Context, url string, capBytes int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d em %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

func downloadTo(ctx context.Context, hc *http.Client, url, destPath string, declaredSize int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("baixar %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("baixar %s: HTTP %d", url, resp.StatusCode)
	}
	limit := maxArtifactBytes
	if declaredSize > 0 && declaredSize < limit {
		limit = declaredSize
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	written, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("baixar %s: %w", url, err)
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return fmt.Errorf("artefato %s excede o tamanho declarado/limite", url)
	}
	return nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
