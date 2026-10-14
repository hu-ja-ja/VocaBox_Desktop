package main

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// maxSessionBody bounds the session file, which holds one Cookie header.
const maxSessionBody = 64 << 10

// sessionStore keeps the niconico session cookies across runs. The value
// is a Cookie header ("user_session=…; …"), never the password, and is
// encrypted with DPAPI, which only this Windows user can read.
type sessionStore struct {
	mylist *Mylist
}

// path is the file the session is kept in, beside the other app data.
func (s sessionStore) path() (string, error) {
	return s.mylist.configPath("session.dat")
}

// Save encrypts and writes the Cookie header through a temp file and
// rename, so a crash mid-write never leaves a torn file behind.
func (s sessionStore) Save(cookies string) error {
	cookies = strings.TrimSpace(cookies)
	if cookies == "" {
		return errors.New("ログイン情報が空です")
	}
	blob, err := dpapiProtect([]byte(cookies))
	if err != nil {
		return errors.New("ログイン情報を保存できませんでした")
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, blob, 0o600, "ログイン情報を保存できませんでした")
}

// Load returns the stored Cookie header, "" when none is saved.
func (s sessionStore) Load() (string, error) {
	path, err := s.path()
	if err != nil {
		return "", err
	}
	blob, err := readBoundedFile(path, maxSessionBody)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil || len(blob) == 0 {
		return "", errors.New("ログイン情報を読み込めませんでした")
	}
	body, err := dpapiUnprotect(blob)
	if err != nil {
		return "", errors.New("ログイン情報を読み込めませんでした")
	}
	return string(body), nil
}

// Clear forgets the stored session; a missing file is already clear.
func (s sessionStore) Clear() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.New("ログイン情報を削除できませんでした")
	}
	return nil
}

// DPAPI (CryptProtectData) keeps a secret for the Windows user who wrote
// it, as the browser's own password and cookie stores do.
var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

// cryptUIFlag keeps the calls from showing a prompt (CRYPTPROTECT_UI_FORBIDDEN).
const cryptUIFlag = 0x1

// dataBlob is DATA_BLOB of dpapi.h.
type dataBlob struct {
	size uint32
	data *byte
}

// take copies the blob's bytes and frees them.
func (b dataBlob) take() []byte {
	if b.data == nil || b.size == 0 {
		return nil
	}
	out := make([]byte, b.size)
	copy(out, unsafe.Slice(b.data, int(b.size)))
	procLocalFree.Call(uintptr(unsafe.Pointer(b.data)))
	return out
}

func dpapiProtect(clear []byte) ([]byte, error) {
	in := dataBlob{size: uint32(len(clear))}
	if len(clear) > 0 {
		in.data = &clear[0]
	}
	var out dataBlob
	ok, _, _ := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, cryptUIFlag,
		uintptr(unsafe.Pointer(&out)))
	if ok == 0 {
		return nil, errors.New("CryptProtectData failed")
	}
	return out.take(), nil
}

func dpapiUnprotect(blob []byte) ([]byte, error) {
	in := dataBlob{size: uint32(len(blob))}
	if len(blob) > 0 {
		in.data = &blob[0]
	}
	var out dataBlob
	ok, _, _ := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, cryptUIFlag,
		uintptr(unsafe.Pointer(&out)))
	if ok == 0 {
		return nil, errors.New("CryptUnprotectData failed")
	}
	return out.take(), nil
}
