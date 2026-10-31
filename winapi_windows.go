//go:build windows

package sysproxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	modWininet   = syscall.NewLazyDLL("wininet.dll")
	modShell32   = syscall.NewLazyDLL("shell32.dll")
	modOle32     = syscall.NewLazyDLL("ole32.dll")
	procSetOptW  = modWininet.NewProc("InternetSetOptionW")
	procKnownDir = modShell32.NewProc("SHGetKnownFolderPath")
	procCoFree   = modOle32.NewProc("CoTaskMemFree")

	modAdvapi32  = syscall.NewLazyDLL("advapi32.dll")
	procCredRead = modAdvapi32.NewProc("CredReadW")
	procCredWr   = modAdvapi32.NewProc("CredWriteW")
	procCredDel  = modAdvapi32.NewProc("CredDeleteW")
	procCredFree = modAdvapi32.NewProc("CredFree")
)

const (
	internetOptionRefresh         = 37
	internetOptionSettingsChanged = 39
)

// notifyWinINET tells WinINET that the proxy settings in the registry changed
// (INTERNET_OPTION_SETTINGS_CHANGED) and to re-read them (INTERNET_OPTION_REFRESH),
// which is what running applications need to notice the change.
func notifyWinINET() error {
	var errs []error
	for _, opt := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		if r, _, e := procSetOptW.Call(0, opt, 0, 0); r == 0 {
			errs = append(errs, fmt.Errorf("sysproxy: InternetSetOption(%d): %w", opt, e))
		}
	}
	return errors.Join(errs...)
}

// FOLDERID_Profile and FOLDERID_Documents.
var (
	folderIDProfile   = syscall.GUID{Data1: 0x5E6C858F, Data2: 0x0E22, Data3: 0x4760, Data4: [8]byte{0x9A, 0xFE, 0xEA, 0x33, 0x17, 0xB6, 0x71, 0x73}}
	folderIDDocuments = syscall.GUID{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}
)

// knownFolder returns the real location of a shell folder, which follows
// redirections such as OneDrive's Documents.
func knownFolder(id syscall.GUID) (string, error) {
	var p *uint16
	hr, _, _ := procKnownDir.Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&p))) //nolint:gosec // Win32 call
	if p != nil {
		defer func() { _, _, _ = procCoFree.Call(uintptr(unsafe.Pointer(p))) }() //nolint:gosec // Win32 call
	}
	if hr != 0 {
		return "", fmt.Errorf("sysproxy: SHGetKnownFolderPath: HRESULT 0x%x", hr)
	}
	return utf16PtrToString(p), nil
}

// utf16PtrToString converts a NUL-terminated UTF-16 string owned by Windows.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for q := unsafe.Pointer(p); *(*uint16)(q) != 0; q = unsafe.Add(q, 2) { //nolint:gosec // walk the NUL-terminated string
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n)) //nolint:gosec // p points to n valid UTF-16 units
}

// Credential Manager. A proxy password is stored as a "Domain Password" entry
// named after the proxy host, like `cmdkey /add:<host>` does, but through the
// API: the password never appears on a command line, and the entry is marked
// with credComment so that Set does not overwrite, and Unset does not delete,
// an entry that belongs to someone else.
const (
	credTypeDomainPassword = 2
	credPersistLocal       = 2
	errorNotFound          = syscall.Errno(1168)
	credMaxBlobSize        = 2560 // CRED_MAX_CREDENTIAL_BLOB_SIZE

	// credComment marks the entries written by this package.
	credComment = "go-sysproxy"
)

// credential mirrors CREDENTIALW.
type credential struct {
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

// storedCredential describes an entry found in Credential Manager.
type storedCredential struct {
	user, comment string
}

// readCredential returns the entry for target, or nil when there is none.
func readCredential(target string) (*storedCredential, error) {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return nil, err
	}
	var c *credential
	r, _, e := procCredRead.Call(uintptr(unsafe.Pointer(t)), credTypeDomainPassword, 0, uintptr(unsafe.Pointer(&c))) //nolint:gosec // Win32 call
	if r == 0 {
		if e == errorNotFound {
			return nil, nil //nolint:nilnil // "no entry" is not an error
		}
		return nil, fmt.Errorf("sysproxy: CredRead %q: %w", target, e)
	}
	defer func() { _, _, _ = procCredFree.Call(uintptr(unsafe.Pointer(c))) }() //nolint:gosec // Win32 call
	return &storedCredential{user: utf16PtrToString(c.UserName), comment: utf16PtrToString(c.Comment)}, nil
}

// writeCredential stores user/password for target and marks it as ours.
func writeCredential(target, user, password string) error {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	u, err := syscall.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	cm, _ := syscall.UTF16PtrFromString(credComment)
	// The blob is the password in UTF-16, without a terminator (as cmdkey stores it).
	var blob []byte
	for _, r := range utf16.Encode([]rune(password)) {
		blob = binary.LittleEndian.AppendUint16(blob, r)
	}
	if len(blob) > credMaxBlobSize {
		return fmt.Errorf("sysproxy: proxy password is too long for Credential Manager")
	}
	c := credential{
		Type:               credTypeDomainPassword,
		TargetName:         t,
		Comment:            cm,
		CredentialBlobSize: uint32(len(blob)), //nolint:gosec // bounded by credMaxBlobSize
		Persist:            credPersistLocal,
		UserName:           u,
	}
	if len(blob) > 0 {
		c.CredentialBlob = &blob[0]
	}
	if r, _, e := procCredWr.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 { //nolint:gosec // Win32 call
		return fmt.Errorf("sysproxy: CredWrite %q: %w", target, e)
	}
	return nil
}

// deleteCredential removes the entry for target.
func deleteCredential(target string) error {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if r, _, e := procCredDel.Call(uintptr(unsafe.Pointer(t)), credTypeDomainPassword, 0); r == 0 { //nolint:gosec // Win32 call
		return fmt.Errorf("sysproxy: CredDelete %q: %w", target, e)
	}
	return nil
}

// storeProxyCredential saves a proxy login unless an entry that is not ours
// already exists for the host.
func storeProxyCredential(host, user, password string) error {
	existing, err := readCredential(host)
	if err != nil {
		return err
	}
	if existing != nil && existing.comment != credComment {
		return fmt.Errorf("a stored credential for %q (user %q) belongs to something else and was left unchanged", host, existing.user)
	}
	return writeCredential(host, user, password)
}

// removeProxyCredential deletes the entry for host if this package wrote it.
func removeProxyCredential(host string) error {
	existing, err := readCredential(host)
	if err != nil || existing == nil || existing.comment != credComment {
		return err
	}
	return deleteCredential(host)
}
