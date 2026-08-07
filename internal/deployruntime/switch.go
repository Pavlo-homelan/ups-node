// Package deployruntime performs the transactional hand-off between the
// field-proven Python services and their Go replacements.
package deployruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Mode string

const (
	ModePython Mode = "python"
	ModeGo     Mode = "go"
)

const (
	pythonHelper = "ups-pi-node-helper.service"
	pythonWeb    = "ups-pi-node.service"
	goHelper     = "ups-pi-node-helper-go.service"
	goWeb        = "ups-pi-node-web-go.service"
)

type Runner interface {
	Run(context.Context, string, ...string) error
}

type Config struct {
	StateFile     string
	NginxLink     string
	PythonSnippet string
	GoSnippet     string
	GoWebSocket   string
	LocalUnitDir  string
	Systemctl     string
	Nginx         string
	Curl          string
	HealthTries   int
	HealthDelay   time.Duration
}

func DefaultConfig() Config {
	return Config{
		StateFile:     "/etc/ups-pi-node/runtime",
		NginxLink:     "/etc/nginx/snippets/ups-pi-node-runtime.conf",
		PythonSnippet: "/etc/nginx/snippets/ups-pi-node-python.conf",
		GoSnippet:     "/etc/nginx/snippets/ups-pi-node-go.conf",
		GoWebSocket:   "/run/ups-pi-node-web/web.sock",
		LocalUnitDir:  "/etc/systemd/system",
		Systemctl:     "/usr/bin/systemctl",
		Nginx:         "/usr/sbin/nginx",
		Curl:          "/usr/bin/curl",
		HealthTries:   15,
		HealthDelay:   time.Second,
	}
}

type Switcher struct {
	config Config
	runner Runner
}

func New(config Config, runner Runner) (*Switcher, error) {
	if runner == nil {
		return nil, fmt.Errorf("runtime command runner is required")
	}
	if config.StateFile == "" || config.NginxLink == "" || config.PythonSnippet == "" ||
		config.GoSnippet == "" || config.GoWebSocket == "" || config.LocalUnitDir == "" || config.Systemctl == "" ||
		config.Nginx == "" || config.Curl == "" {
		return nil, fmt.Errorf("runtime switch paths are incomplete")
	}
	if config.HealthTries <= 0 || config.HealthDelay < 0 {
		return nil, fmt.Errorf("runtime health retry settings are invalid")
	}
	return &Switcher{config: config, runner: runner}, nil
}

func (switcher *Switcher) CurrentMode() (Mode, error) {
	payload, err := os.ReadFile(switcher.config.StateFile)
	if os.IsNotExist(err) {
		return ModePython, nil
	}
	if err != nil {
		return "", fmt.Errorf("read runtime state: %w", err)
	}
	return ParseMode(strings.TrimSpace(string(payload)))
}

func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case ModePython:
		return ModePython, nil
	case ModeGo:
		return ModeGo, nil
	default:
		return "", fmt.Errorf("runtime must be %q or %q", ModePython, ModeGo)
	}
}

func (switcher *Switcher) Status(ctx context.Context) (string, error) {
	mode, err := switcher.CurrentMode()
	if err != nil {
		return "", err
	}
	selectedHelper, selectedWeb := pythonHelper, pythonWeb
	if mode == ModeGo {
		selectedHelper, selectedWeb = goHelper, goWeb
	}
	helperActive := switcher.active(ctx, selectedHelper)
	webActive := switcher.active(ctx, selectedWeb)
	return fmt.Sprintf("runtime=%s helper_active=%t web_active=%t", mode, helperActive, webActive), nil
}

// Apply converges the node onto one runtime. A failed hand-off invokes the
// opposite, already-packaged runtime before the original error is returned.
func (switcher *Switcher) Apply(ctx context.Context, target Mode) error {
	if _, err := ParseMode(string(target)); err != nil {
		return err
	}
	if err := switcher.preflight(target); err != nil {
		return err
	}
	var err error
	var rollbackMode Mode
	if target == ModeGo {
		err = switcher.applyGo(ctx)
		rollbackMode = ModePython
	} else {
		err = switcher.applyPython(ctx)
		rollbackMode = ModeGo
	}
	if err == nil {
		return nil
	}
	rollbackErr := switcher.rollback(ctx, rollbackMode)
	if rollbackErr != nil {
		return fmt.Errorf("switch to %s: %w; rollback to %s also failed: %v", target, err, rollbackMode, rollbackErr)
	}
	return fmt.Errorf("switch to %s: %w; restored %s runtime", target, err, rollbackMode)
}

func (switcher *Switcher) preflight(target Mode) error {
	snippet := switcher.config.PythonSnippet
	if target == ModeGo {
		snippet = switcher.config.GoSnippet
	}
	info, err := os.Stat(snippet)
	if err != nil {
		return fmt.Errorf("inspect %s nginx snippet: %w", target, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s nginx snippet is not a regular file", target)
	}
	if target == ModeGo {
		for _, unit := range []string{goHelper, goWeb} {
			for _, path := range []string{
				filepath.Join(switcher.config.LocalUnitDir, unit),
				filepath.Join(switcher.config.LocalUnitDir, unit+".d"),
			} {
				if _, err := os.Lstat(path); err == nil {
					return fmt.Errorf("refusing unverified local systemd override %s", path)
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("inspect local systemd override %s: %w", path, err)
				}
			}
		}
	}
	return nil
}

func (switcher *Switcher) applyGo(ctx context.Context) error {
	if err := switcher.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	pythonWasActive := switcher.active(ctx, pythonHelper)
	if err := switcher.systemctl(ctx, "restart", goWeb); err != nil {
		return err
	}
	if pythonWasActive {
		if err := switcher.waitGoHealth(ctx); err != nil {
			return fmt.Errorf("validate Go web against Python helper: %w", err)
		}
	}
	if err := switcher.systemctl(ctx, "stop", pythonHelper); err != nil {
		return err
	}
	if err := switcher.systemctl(ctx, "restart", goHelper); err != nil {
		return err
	}
	// Drop any healthy snapshot collected from the old helper. The new web
	// process must build readiness exclusively from the new hardware owner.
	if err := switcher.systemctl(ctx, "restart", goWeb); err != nil {
		return err
	}
	if err := switcher.waitGoHealth(ctx); err != nil {
		return fmt.Errorf("validate Go runtime: %w", err)
	}
	if err := switcher.selectNginx(ctx, switcher.config.GoSnippet); err != nil {
		return err
	}
	if err := switcher.systemctl(ctx, "stop", pythonWeb); err != nil {
		return err
	}
	if err := switcher.enableSelected(ctx, goHelper, goWeb, pythonHelper, pythonWeb); err != nil {
		return err
	}
	if err := switcher.verifySelected(ctx, goHelper, goWeb, switcher.waitGoHealth); err != nil {
		return err
	}
	return switcher.writeMode(ModeGo)
}

func (switcher *Switcher) applyPython(ctx context.Context) error {
	if err := switcher.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	goWasActive := switcher.active(ctx, goHelper)
	if err := switcher.systemctl(ctx, "restart", pythonWeb); err != nil {
		return err
	}
	if goWasActive {
		if err := switcher.waitPythonHealth(ctx); err != nil {
			return fmt.Errorf("validate Python web against Go helper: %w", err)
		}
	}
	if err := switcher.systemctl(ctx, "stop", goHelper); err != nil {
		return err
	}
	if err := switcher.systemctl(ctx, "restart", pythonHelper); err != nil {
		return err
	}
	// As above, force a fresh status collection through the selected helper.
	if err := switcher.systemctl(ctx, "restart", pythonWeb); err != nil {
		return err
	}
	if err := switcher.waitPythonHealth(ctx); err != nil {
		return fmt.Errorf("validate Python runtime: %w", err)
	}
	if err := switcher.selectNginx(ctx, switcher.config.PythonSnippet); err != nil {
		return err
	}
	if err := switcher.systemctl(ctx, "stop", goWeb); err != nil {
		return err
	}
	if err := switcher.enableSelected(ctx, pythonHelper, pythonWeb, goHelper, goWeb); err != nil {
		return err
	}
	if err := switcher.verifySelected(ctx, pythonHelper, pythonWeb, switcher.waitPythonHealth); err != nil {
		return err
	}
	return switcher.writeMode(ModePython)
}

func (switcher *Switcher) verifySelected(
	ctx context.Context,
	helper string,
	web string,
	health func(context.Context) error,
) error {
	if !switcher.active(ctx, helper) {
		return fmt.Errorf("selected helper %s is not active", helper)
	}
	if !switcher.active(ctx, web) {
		return fmt.Errorf("selected web service %s is not active", web)
	}
	if err := health(ctx); err != nil {
		return fmt.Errorf("final selected-runtime health check: %w", err)
	}
	return nil
}

func (switcher *Switcher) rollback(ctx context.Context, target Mode) error {
	if target == ModePython {
		return switcher.applyPython(ctx)
	}
	return switcher.applyGo(ctx)
}

func (switcher *Switcher) waitGoHealth(ctx context.Context) error {
	return switcher.retry(ctx, func() error {
		return switcher.runner.Run(
			ctx, switcher.config.Curl, "--fail", "--silent", "--show-error", "--max-time", "3",
			"--unix-socket", switcher.config.GoWebSocket, "http://localhost/healthz",
		)
	})
}

func (switcher *Switcher) waitPythonHealth(ctx context.Context) error {
	return switcher.retry(ctx, func() error {
		return switcher.runner.Run(
			ctx, switcher.config.Curl, "--fail", "--silent", "--show-error", "--max-time", "3",
			"http://127.0.0.1:8000/login",
		)
	})
}

func (switcher *Switcher) retry(ctx context.Context, check func() error) error {
	var lastErr error
	for attempt := 0; attempt < switcher.config.HealthTries; attempt++ {
		if err := check(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt+1 == switcher.config.HealthTries {
			break
		}
		timer := time.NewTimer(switcher.config.HealthDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("health check failed after %d attempts: %w", switcher.config.HealthTries, lastErr)
}

func (switcher *Switcher) selectNginx(ctx context.Context, snippet string) error {
	if err := replaceSymlink(switcher.config.NginxLink, snippet); err != nil {
		return err
	}
	if err := switcher.runner.Run(ctx, switcher.config.Nginx, "-t"); err != nil {
		return fmt.Errorf("validate nginx configuration: %w", err)
	}
	if err := switcher.systemctl(ctx, "reload", "nginx.service"); err != nil {
		return err
	}
	return nil
}

func (switcher *Switcher) enableSelected(
	ctx context.Context,
	helper string,
	web string,
	oldHelper string,
	oldWeb string,
) error {
	if err := switcher.systemctl(ctx, "enable", helper, web); err != nil {
		return err
	}
	return switcher.systemctl(ctx, "disable", oldHelper, oldWeb)
}

func (switcher *Switcher) active(ctx context.Context, unit string) bool {
	return switcher.runner.Run(ctx, switcher.config.Systemctl, "is-active", "--quiet", unit) == nil
}

func (switcher *Switcher) systemctl(ctx context.Context, args ...string) error {
	if err := switcher.runner.Run(ctx, switcher.config.Systemctl, args...); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (switcher *Switcher) writeMode(mode Mode) error {
	if err := os.MkdirAll(filepath.Dir(switcher.config.StateFile), 0o755); err != nil {
		return fmt.Errorf("create runtime state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(switcher.config.StateFile), ".runtime-*")
	if err != nil {
		return fmt.Errorf("create runtime state: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set runtime state permissions: %w", err)
	}
	if _, err := fmt.Fprintf(temporary, "%s\n", mode); err != nil {
		temporary.Close()
		return fmt.Errorf("write runtime state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync runtime state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close runtime state: %w", err)
	}
	if err := os.Rename(temporaryName, switcher.config.StateFile); err != nil {
		return fmt.Errorf("activate runtime state: %w", err)
	}
	return nil
}

func replaceSymlink(link string, target string) error {
	if !filepath.IsAbs(link) || !filepath.IsAbs(target) {
		return fmt.Errorf("nginx runtime link and target must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("create nginx snippet directory: %w", err)
	}
	temporary := fmt.Sprintf("%s.tmp-%d", link, os.Getpid())
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale nginx runtime link: %w", err)
	}
	if err := os.Symlink(target, temporary); err != nil {
		return fmt.Errorf("create nginx runtime link: %w", err)
	}
	if err := os.Rename(temporary, link); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("activate nginx runtime link: %w", err)
	}
	return nil
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, program string, args ...string) error {
	command := exec.CommandContext(ctx, program, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if len(message) > 2048 {
		message = message[:2048]
	}
	if message == "" {
		return err
	}
	return errors.Join(err, errors.New(message))
}
