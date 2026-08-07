package webapp

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func Listen(spec string, socketGroup string) (net.Listener, error) {
	if strings.HasPrefix(spec, "unix:") {
		return listenUnix(strings.TrimPrefix(spec, "unix:"), socketGroup)
	}
	if strings.HasPrefix(spec, "tcp:") {
		address := strings.TrimPrefix(spec, "tcp:")
		if address == "" {
			return nil, fmt.Errorf("TCP listen address is empty")
		}
		return net.Listen("tcp", address)
	}
	return nil, fmt.Errorf("listen address must start with unix: or tcp:")
}

func listenUnix(path string, groupName string) (net.Listener, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("web Unix socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create web runtime directory: %w", err)
	}
	info, err := os.Lstat(path)
	switch {
	case err == nil && info.Mode()&os.ModeSocket == 0:
		return nil, fmt.Errorf("refusing to replace non-socket path %s", path)
	case err == nil:
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale web socket: %w", err)
		}
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("inspect web socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on web socket: %w", err)
	}
	cleanup := func(cause error) (net.Listener, error) {
		listener.Close()
		os.Remove(path)
		return nil, cause
	}
	if groupName != "" {
		group, err := user.LookupGroup(groupName)
		if err != nil {
			return cleanup(fmt.Errorf("look up web socket group %q: %w", groupName, err))
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			return cleanup(fmt.Errorf("parse web socket group id: %w", err))
		}
		if err := os.Chown(path, -1, gid); err != nil {
			return cleanup(fmt.Errorf("set web socket group: %w", err))
		}
	}
	if err := os.Chmod(path, 0o660); err != nil {
		return cleanup(fmt.Errorf("set web socket permissions: %w", err))
	}
	return listener, nil
}
