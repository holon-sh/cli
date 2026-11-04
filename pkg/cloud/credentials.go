package cloud

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/holon-sh/cli/internal/clientutil"
)

const maxCredentialBytes = 4 * maxOAuthToken

func openCredentialDir(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != privateDirPerm {
		return nil, errCredentialUnavailable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errCredentialUnavailable
	}
	return root, nil
}

func readCredentialFile(path string) ([]byte, error) {
	root, err := openCredentialDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNotAuthenticated
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != privateFilePerm {
		return nil, errCredentialUnavailable
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, errCredentialUnavailable
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm() != privateFilePerm {
		return nil, errCredentialUnavailable
	}
	data, err := clientutil.ReadLimited(file, maxCredentialBytes)
	if err != nil {
		return nil, errCredentialUnavailable
	}
	return data, nil
}

func (m *Client) writeState(state authState) error {
	data, _ := json.Marshal(state)
	if len(data) > maxCredentialBytes {
		return errCredentialUnavailable
	}
	root, err := openCredentialDir(filepath.Dir(m.statePath))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return writeAtomicFile(root, filepath.Base(m.statePath), data)
}

func removeCredentialFile(path string) error {
	root, err := openCredentialDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(filepath.Base(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errCredentialUnavailable
	}
	return nil
}

func writeAtomicFile(root *os.Root, target string, data []byte) error {
	name := ".auth-" + rand.Text()
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFilePerm)
	if err != nil {
		return errCredentialUnavailable
	}
	defer func() { _ = root.Remove(name) }()
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return errCredentialUnavailable
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return errCredentialUnavailable
	}
	if err := root.Rename(name, target); err != nil {
		return errCredentialUnavailable
	}
	return nil
}
