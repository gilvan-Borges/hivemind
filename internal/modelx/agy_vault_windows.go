package modelx

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	agyCredTargetName       = "gemini:antigravity"
	agyCredUserName         = "antigravity"
)

type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32DLL     = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW   = advapi32DLL.NewProc("CredReadW")
	procCredWriteW  = advapi32DLL.NewProc("CredWriteW")
	procCredDeleteW = advapi32DLL.NewProc("CredDeleteW")
	procCredFree    = advapi32DLL.NewProc("CredFree")

	agyVaultSwitchMu sync.Mutex

	pendingAgyLoginMu     sync.Mutex
	pendingAgyCmd         *exec.Cmd
	pendingAgyStdin       io.WriteCloser
	pendingAgyBackupBlob  []byte
	pendingAgyBackupEmail string
	pendingAgyActive      bool
)

type agyOAuthCredMeta struct {
	IDTokenEmail string `json:"id_token_email"`
	AuthMethod   string `json:"auth_method"`
	WriterID     string `json:"writer_id"`
}

// ReadLiveAgyOAuthCredential reads the active Antigravity Google OAuth session blob
// from Windows Credential Manager ("gemini:antigravity") and extracts its Google email
// ("id_token_email") without exposing any token material.
func ReadLiveAgyOAuthCredential() (raw []byte, email string, err error) {
	if runtime.GOOS != "windows" {
		return nil, "", fmt.Errorf("vault OAuth do agy suportado no Windows")
	}
	targetPtr, err := syscall.UTF16PtrFromString(agyCredTargetName)
	if err != nil {
		return nil, "", err
	}
	var cred *winCredential
	r1, _, e1 := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if r1 == 0 || cred == nil {
		return nil, "", fmt.Errorf("nenhuma sessão OAuth ativa em %s (%v)", agyCredTargetName, e1)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))

	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return nil, "", fmt.Errorf("sessão OAuth vazia")
	}
	blob := unsafe.Slice(cred.CredentialBlob, int(cred.CredentialBlobSize))
	copied := append([]byte(nil), blob...)

	var meta agyOAuthCredMeta
	if json.Unmarshal(copied, &meta) == nil {
		email = strings.TrimSpace(meta.IDTokenEmail)
	}
	return copied, email, nil
}

// WriteLiveAgyOAuthCredential atomically writes a saved Antigravity Google OAuth session
// blob into Windows Credential Manager ("gemini:antigravity").
func WriteLiveAgyOAuthCredential(raw []byte) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("vault OAuth do agy suportado no Windows")
	}
	if len(raw) == 0 {
		return fmt.Errorf("blob OAuth vazio")
	}
	targetPtr, err := syscall.UTF16PtrFromString(agyCredTargetName)
	if err != nil {
		return err
	}
	userPtr, err := syscall.UTF16PtrFromString(agyCredUserName)
	if err != nil {
		return err
	}
	cred := winCredential{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		CredentialBlobSize: uint32(len(raw)),
		CredentialBlob:     &raw[0],
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}
	r1, _, e1 := procCredWriteW.Call(
		uintptr(unsafe.Pointer(&cred)),
		0,
	)
	if r1 == 0 {
		return fmt.Errorf("gravar sessão OAuth em %s: %v", agyCredTargetName, e1)
	}
	return nil
}

// DeleteLiveAgyOAuthCredential removes the active "gemini:antigravity" entry from
// Windows Credential Manager so `agy` prompts for a new Google account login in the browser
// (after the previous account's blob has been safely stored in its isolated profile folder).
func DeleteLiveAgyOAuthCredential() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	targetPtr, err := syscall.UTF16PtrFromString(agyCredTargetName)
	if err != nil {
		return err
	}
	r1, _, e1 := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		uintptr(credTypeGeneric),
		0,
	)
	if r1 == 0 {
		return fmt.Errorf("limpar slot OAuth ativo: %v", e1)
	}
	return nil
}

// AgyProfileVaultPath returns the isolated file path where a named AGY Google account's
// OAuth session blob is persisted (%APPDATA%\HiveMind\agy-profiles\<profileID>\oauth-session.json).
func AgyProfileVaultPath(profileID string) (string, error) {
	base, err := profilesFilePathFn()
	if err != nil {
		return "", err
	}
	slug := sanitizeSlug(profileID)
	if slug == "" {
		slug = "default"
	}
	return filepath.Join(filepath.Dir(base), "agy-profiles", slug, "oauth-session.json"), nil
}

// HasAgyVaultSession reports whether an isolated AGY profile folder has a saved Google OAuth session.
func HasAgyVaultSession(profileID string) (bool, string) {
	p, err := AgyProfileVaultPath(profileID)
	if err != nil || p == "" {
		return false, ""
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return false, ""
	}
	var meta agyOAuthCredMeta
	if json.Unmarshal(data, &meta) == nil && strings.TrimSpace(meta.IDTokenEmail) != "" {
		return true, strings.TrimSpace(meta.IDTokenEmail)
	}
	return true, ""
}

// SyncCurrentLiveAgyAccountToProfiles inspects the active Google account logged into `agy`
// (via "gemini:antigravity" -> id_token_email), automatically registers or updates its
// ProviderProfile identified by its email address, and backs up its OAuth session into
// its isolated `%APPDATA%\HiveMind\agy-profiles\<id>\oauth-session.json` folder.
func SyncCurrentLiveAgyAccountToProfiles() (*ProviderProfile, error) {
	raw, email, err := ReadLiveAgyOAuthCredential()
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	if email == "" {
		email = "conta-google-ativa"
	}
	profID := sanitizeSlug("agy-" + email)
	if profID == "" {
		profID = "agy-principal"
	}

	profilesMu.Lock()
	doc, lerr := loadProfilesLocked()
	if lerr != nil {
		profilesMu.Unlock()
		return nil, lerr
	}

	foundIdx := -1
	hasAnyAgy := false
	for i := range doc.Profiles {
		if doc.Profiles[i].Provider != "agy" {
			continue
		}
		hasAnyAgy = true
		savedEmail := strings.TrimSpace(doc.Profiles[i].Env["AGY_EMAIL"])
		if strings.EqualFold(doc.Profiles[i].ID, profID) || (savedEmail != "" && strings.EqualFold(savedEmail, email)) {
			foundIdx = i
			profID = doc.Profiles[i].ID
			break
		}
	}

	vaultPath, verr := AgyProfileVaultPath(profID)
	if verr == nil && vaultPath != "" {
		_ = os.MkdirAll(filepath.Dir(vaultPath), 0o700)
		_ = writeSecretFile(vaultPath, raw)
	}

	var resultProf ProviderProfile
	if foundIdx >= 0 {
		if doc.Profiles[foundIdx].Env == nil {
			doc.Profiles[foundIdx].Env = map[string]string{}
		}
		doc.Profiles[foundIdx].Env["AGY_EMAIL"] = email
		if doc.Profiles[foundIdx].AuthMode == "" || doc.Profiles[foundIdx].AuthMode == "live" {
			doc.Profiles[foundIdx].AuthMode = "vault"
		}
		if strings.TrimSpace(doc.Profiles[foundIdx].Name) == "" || strings.EqualFold(doc.Profiles[foundIdx].Name, doc.Profiles[foundIdx].ID) {
			doc.Profiles[foundIdx].Name = email
		}
		resultProf = doc.Profiles[foundIdx]
	} else {
		resultProf = ProviderProfile{
			ID:        profID,
			Provider:  "agy",
			Name:      email,
			AuthMode:  "vault",
			Env:       map[string]string{"AGY_EMAIL": email},
			IsDefault: !hasAnyAgy,
		}
		doc.Profiles = append(doc.Profiles, resultProf)
	}

	_ = saveProfilesLocked(doc)
	profilesMu.Unlock()
	return &resultProf, nil
}

// ActivateAgyProfileSession switches the active Antigravity Google OAuth session
// to profileID's isolated `oauth-session.json` (first backing up whichever account
// is currently active so no token refresh is ever lost).
func ActivateAgyProfileSession(profileID string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	pid := sanitizeSlug(profileID)
	if pid == "" || pid == "__system__" || pid == "__global__" {
		return nil
	}
	vaultPath, err := AgyProfileVaultPath(pid)
	if err != nil {
		return err
	}
	targetBlob, err := os.ReadFile(vaultPath)
	if err != nil || len(targetBlob) == 0 {
		return fmt.Errorf("sessão OAuth isolada não encontrada para %s", profileID)
	}

	// Backup the currently active live credential to its matching profile folder first.
	_, _ = SyncCurrentLiveAgyAccountToProfiles()

	return WriteLiveAgyOAuthCredential(targetBlob)
}

// EnsureLiveAgyCredentialRestored guarantees that if the live "gemini:antigravity"
// slot is empty (and no OAuth code entry is currently in progress), the primary saved
// AGY vault account (e.g. Conta #1) is automatically restored from its isolated folder.
func EnsureLiveAgyCredentialRestored() {
	if runtime.GOOS != "windows" {
		return
	}
	pendingAgyLoginMu.Lock()
	inLogin := pendingAgyActive
	pendingAgyLoginMu.Unlock()
	if inLogin {
		return
	}
	if raw, _, err := ReadLiveAgyOAuthCredential(); err == nil && len(raw) > 0 {
		return
	}
	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	profilesMu.Unlock()
	if err != nil {
		return
	}
	for _, p := range doc.Profiles {
		if p.Provider != "agy" {
			continue
		}
		if vpath, verr := AgyProfileVaultPath(p.ID); verr == nil && vpath != "" {
			if blob, rerr := os.ReadFile(vpath); rerr == nil && len(blob) > 0 {
				_ = WriteLiveAgyOAuthCredential(blob)
				return
			}
		}
	}
}

// ConnectNewAgyGoogleAccount backs up the current AGY Google account into its isolated
// folder, clears the live credential slot, and starts `agy models` in the background with
// an open stdin pipe so `agy` opens the browser to `antigravity.google/oauth-callback` and
// waits for the user to paste the authorization code (`4/0A...`) directly inside the HiveMind modal.
func ConnectNewAgyGoogleAccount(workspace string) (ProfilesOverview, error) {
	// 1. Snapshot current account so it is 100% preserved in its isolated folder.
	prevRaw, prevEmail, _ := ReadLiveAgyOAuthCredential()
	if len(prevRaw) > 0 {
		_, _ = SyncCurrentLiveAgyAccountToProfiles()
	}

	agyBin, err := LookAgy()
	if err != nil {
		return ListProfilesOverview(workspace), err
	}

	// Kill any stale pending login process first.
	pendingAgyLoginMu.Lock()
	if pendingAgyStdin != nil {
		_ = pendingAgyStdin.Close()
		pendingAgyStdin = nil
	}
	if pendingAgyCmd != nil && pendingAgyCmd.Process != nil {
		_ = pendingAgyCmd.Process.Kill()
		pendingAgyCmd = nil
	}
	if len(prevRaw) > 0 {
		pendingAgyBackupBlob = prevRaw
		pendingAgyBackupEmail = prevEmail
	}
	pendingAgyActive = true
	pendingAgyLoginMu.Unlock()

	// 2. Clear live slot so agy opens the browser for Google OAuth sign-in.
	_ = DeleteLiveAgyOAuthCredential()

	// 3. Start `agy models` headless with a piped stdin ready to receive the `4/0A...` code from the modal.
	cmd := exec.Command(agyBin, "models")
	procx.HideWindow(cmd)
	stdinPipe, pipeErr := cmd.StdinPipe()
	if pipeErr != nil {
		pendingAgyLoginMu.Lock()
		pendingAgyActive = false
		pendingAgyLoginMu.Unlock()
		EnsureLiveAgyCredentialRestored()
		return ListProfilesOverview(workspace), fmt.Errorf("preparar pipe OAuth do agy: %w", pipeErr)
	}
	if startErr := cmd.Start(); startErr != nil {
		_ = stdinPipe.Close()
		pendingAgyLoginMu.Lock()
		pendingAgyActive = false
		pendingAgyLoginMu.Unlock()
		EnsureLiveAgyCredentialRestored()
		return ListProfilesOverview(workspace), fmt.Errorf("iniciar login OAuth do agy: %w", startErr)
	}

	pendingAgyLoginMu.Lock()
	pendingAgyCmd = cmd
	pendingAgyStdin = stdinPipe
	pendingAgyLoginMu.Unlock()

	go func(c *exec.Cmd) {
		_ = c.Wait()
	}(cmd)

	return ListProfilesOverview(workspace), nil
}

// SubmitAgyOAuthCode sends the authorization code (`4/0A...`) copied from
// `antigravity.google/oauth-callback` into the waiting `agy` process stdin, waits for `agy`
// to finish the token exchange, reads the new account's `id_token_email`, and saves its
// OAuth session into its isolated `%APPDATA%\HiveMind\agy-profiles\<id>\oauth-session.json` folder.
func SubmitAgyOAuthCode(code, workspace string) (ProfilesOverview, error) {
	cleanCode := strings.TrimSpace(code)
	if cleanCode == "" {
		return ListProfilesOverview(workspace), fmt.Errorf("cole o código de autenticação gerado pelo Google Antigravity (ex: 4/0A...)")
	}

	pendingAgyLoginMu.Lock()
	stdin := pendingAgyStdin
	pendingAgyStdin = nil
	pendingAgyLoginMu.Unlock()

	if stdin == nil {
		return ListProfilesOverview(workspace), fmt.Errorf("nenhuma sessão de login aguardando código; clique em '+ Logar Nova Conta Google' novamente")
	}

	_, writeErr := io.WriteString(stdin, cleanCode+"\n")
	_ = stdin.Close()
	if writeErr != nil {
		CancelAgyOAuthLogin(workspace)
		return ListProfilesOverview(workspace), fmt.Errorf("enviar código OAuth para o agy: %w", writeErr)
	}

	// Poll up to 20s for agy to complete the OAuth token exchange and write "gemini:antigravity".
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(350 * time.Millisecond)
		raw, email, rerr := ReadLiveAgyOAuthCredential()
		if rerr == nil && len(raw) > 0 {
			pendingAgyLoginMu.Lock()
			pendingAgyActive = false
			pendingAgyCmd = nil
			pendingAgyBackupBlob = nil
			pendingAgyLoginMu.Unlock()

			_, _ = SyncCurrentLiveAgyAccountToProfiles()
			_ = email
			return ListProfilesOverview(workspace), nil
		}
	}

	// If token exchange failed (e.g. invalid/expired code), restore backup account so nothing is lost.
	CancelAgyOAuthLogin(workspace)
	return ListProfilesOverview(workspace), fmt.Errorf("o código informado não pôde ser validado pelo Google Antigravity (verifique se copiou o código completo e tente novamente)")
}

// CancelAgyOAuthLogin aborts an in-progress Google OAuth login and immediately restores
// the previous Google account into the live "gemini:antigravity" slot.
func CancelAgyOAuthLogin(workspace string) ProfilesOverview {
	pendingAgyLoginMu.Lock()
	if pendingAgyStdin != nil {
		_ = pendingAgyStdin.Close()
		pendingAgyStdin = nil
	}
	if pendingAgyCmd != nil && pendingAgyCmd.Process != nil {
		_ = pendingAgyCmd.Process.Kill()
		pendingAgyCmd = nil
	}
	backup := pendingAgyBackupBlob
	pendingAgyBackupBlob = nil
	pendingAgyActive = false
	pendingAgyLoginMu.Unlock()

	if len(backup) > 0 {
		_ = WriteLiveAgyOAuthCredential(backup)
	} else {
		EnsureLiveAgyCredentialRestored()
	}
	return ListProfilesOverview(workspace)
}
