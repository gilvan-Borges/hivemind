//go:build windows

package secretx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	maxCredBlobBytes        = 2048
	dpapiFilePrefix         = "dpapi-file:"
)

var (
	modAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	modCrypt32  = windows.NewLazySystemDLL("crypt32.dll")

	procCredWriteW     = modAdvapi32.NewProc("CredWriteW")
	procCredReadW      = modAdvapi32.NewProc("CredReadW")
	procCredDeleteW    = modAdvapi32.NewProc("CredDeleteW")
	procCredEnumerateW = modAdvapi32.NewProc("CredEnumerateW")
	procCredFree       = modAdvapi32.NewProc("CredFree")

	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

type windowsVault struct{}

func newPlatformVault() Vault {
	return &windowsVault{}
}

func (w *windowsVault) Available() (bool, string) {
	if err := modAdvapi32.Load(); err != nil {
		return false, err.Error()
	}
	if err := procCredWriteW.Find(); err != nil {
		return false, "CredWriteW indisponível"
	}
	if err := procCryptProtectData.Find(); err != nil {
		return false, "CryptProtectData (DPAPI) indisponível"
	}
	return true, ""
}

func targetName(name string) string {
	return ServicePrefix + name
}

func (w *windowsVault) Set(name, secret string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if secret == "" {
		return errors.New("valor do segredo não pode ser vazio")
	}
	if ok, reason := w.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}

	protected, err := dpapiProtect([]byte(secret))
	if err != nil {
		return fmt.Errorf("DPAPI CryptProtectData: %w", err)
	}

	blobToWrite := protected
	if len(protected) > maxCredBlobBytes {
		blobPath, err := dpapiBlobFilePath(name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(blobPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(blobPath, protected, 0o600); err != nil {
			return err
		}
		blobToWrite = []byte(dpapiFilePrefix + blobPath)
	}

	targetPtr, err := windows.UTF16PtrFromString(targetName(name))
	if err != nil {
		return err
	}
	userPtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         targetPtr,
		CredentialBlobSize: uint32(len(blobToWrite)),
		CredentialBlob:     &blobToWrite[0],
		Persist:            credPersistLocalMachine,
		UserName:           userPtr,
	}
	r1, _, e1 := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r1 == 0 {
		return fmt.Errorf("CredWriteW: %w", e1)
	}
	return nil
}

func (w *windowsVault) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if ok, reason := w.Available(); !ok {
		return "", fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	targetPtr, err := windows.UTF16PtrFromString(targetName(name))
	if err != nil {
		return "", err
	}
	var pCred *credential
	r1, _, e1 := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&pCred)),
	)
	if r1 == 0 {
		if errors.Is(e1, windows.ERROR_NOT_FOUND) {
			return "", ErrSecretNotFound
		}
		return "", fmt.Errorf("CredReadW: %w", e1)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pCred)))

	raw := unsafe.Slice(pCred.CredentialBlob, int(pCred.CredentialBlobSize))
	cipherBytes := append([]byte(nil), raw...)
	if strings.HasPrefix(string(cipherBytes), dpapiFilePrefix) {
		filePath := strings.TrimPrefix(string(cipherBytes), dpapiFilePrefix)
		fileBytes, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("ler blob DPAPI: %w", err)
		}
		cipherBytes = fileBytes
	}
	plain, err := dpapiUnprotect(cipherBytes)
	if err != nil {
		return "", fmt.Errorf("DPAPI CryptUnprotectData: %w", err)
	}
	return string(plain), nil
}

func (w *windowsVault) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if ok, reason := w.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	if p, err := dpapiBlobFilePath(name); err == nil {
		_ = os.Remove(p)
	}
	targetPtr, err := windows.UTF16PtrFromString(targetName(name))
	if err != nil {
		return err
	}
	r1, _, e1 := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(targetPtr)),
		credTypeGeneric,
		0,
	)
	if r1 == 0 {
		if errors.Is(e1, windows.ERROR_NOT_FOUND) {
			return ErrSecretNotFound
		}
		return fmt.Errorf("CredDeleteW: %w", e1)
	}
	return nil
}

func (w *windowsVault) ListNames() ([]string, error) {
	if ok, reason := w.Available(); !ok {
		return nil, fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	filterPtr, err := windows.UTF16PtrFromString(ServicePrefix + "*")
	if err != nil {
		return nil, err
	}
	var count uint32
	var pCreds **credential
	r1, _, e1 := procCredEnumerateW.Call(
		uintptr(unsafe.Pointer(filterPtr)),
		0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&pCreds)),
	)
	if r1 == 0 {
		if errors.Is(e1, windows.ERROR_NOT_FOUND) {
			return nil, nil
		}
		return nil, fmt.Errorf("CredEnumerateW: %w", e1)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pCreds)))

	ptrs := unsafe.Slice(pCreds, int(count))
	names := make([]string, 0, len(ptrs))
	for _, c := range ptrs {
		if c == nil || c.TargetName == nil {
			continue
		}
		target := windows.UTF16PtrToString(c.TargetName)
		if strings.HasPrefix(target, ServicePrefix) {
			names = append(names, strings.TrimPrefix(target, ServicePrefix))
		}
	}
	sort.Strings(names)
	return names, nil
}

func dpapiProtect(plain []byte) ([]byte, error) {
	in := dataBlob{
		cbData: uint32(len(plain)),
		pbData: &plain[0],
	}
	var out dataBlob
	const cryptProtectUIForbidden = 0x1
	r1, _, e1 := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, e1
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, int(out.cbData)))
	return res, nil
}

func dpapiUnprotect(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("blob DPAPI vazio")
	}
	in := dataBlob{
		cbData: uint32(len(cipher)),
		pbData: &cipher[0],
	}
	var out dataBlob
	const cryptProtectUIForbidden = 0x1
	r1, _, e1 := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, e1
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, int(out.cbData)))
	return res, nil
}

func dpapiBlobFilePath(name string) (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		cfg, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = cfg
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(base, "HiveMind", "resident", "vault", hex.EncodeToString(sum[:])+".dpapi"), nil
}
