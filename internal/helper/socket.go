package helper

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// ListenUnix creates a root-owned group-writable helper socket. It refuses to
// replace regular files or symlinks at the configured path.
func ListenUnix(path string, groupName string) (net.Listener, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("helper socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create helper runtime directory: %w", err)
	}

	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode()&os.ModeSocket == 0:
		return nil, fmt.Errorf("refusing to replace non-socket path %s", path)
	case err == nil:
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale helper socket: %w", err)
		}
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("inspect helper socket: %w", err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on helper socket: %w", err)
	}
	cleanup := func(cause error) (net.Listener, error) {
		listener.Close()
		os.Remove(path)
		return nil, cause
	}

	group, err := user.LookupGroup(groupName)
	if err != nil {
		return cleanup(fmt.Errorf("look up helper socket group %q: %w", groupName, err))
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return cleanup(fmt.Errorf("parse helper socket group id: %w", err))
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return cleanup(fmt.Errorf("set helper socket ownership: %w", err))
	}
	if err := os.Chmod(path, 0o660); err != nil {
		return cleanup(fmt.Errorf("set helper socket permissions: %w", err))
	}
	return listener, nil
}
