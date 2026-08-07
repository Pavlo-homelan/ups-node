package deployruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedCommand struct {
	program string
	args    []string
}

type fakeRunner struct {
	active          map[string]bool
	commands        []recordedCommand
	failGoHelperRun bool
	stopGoOnDisable bool
}

func (runner *fakeRunner) Run(_ context.Context, program string, args ...string) error {
	runner.commands = append(runner.commands, recordedCommand{program: program, args: append([]string(nil), args...)})
	if program != "systemctl" {
		return nil
	}
	if len(args) == 3 && args[0] == "is-active" && args[1] == "--quiet" {
		if runner.active[args[2]] {
			return nil
		}
		return errors.New("inactive")
	}
	if len(args) == 2 && args[0] == "restart" {
		if args[1] == goHelper && runner.failGoHelperRun {
			runner.failGoHelperRun = false
			return errors.New("injected Go helper start failure")
		}
		runner.active[args[1]] = true
	}
	if len(args) == 2 && args[0] == "stop" {
		runner.active[args[1]] = false
	}
	if len(args) > 1 && args[0] == "disable" && runner.stopGoOnDisable {
		runner.active[goHelper] = false
		runner.stopGoOnDisable = false
	}
	return nil
}

func testSwitcher(t *testing.T, runner *fakeRunner, initial Mode) (*Switcher, Config) {
	t.Helper()
	directory := t.TempDir()
	config := Config{
		StateFile:     filepath.Join(directory, "etc", "ups-pi-node", "runtime"),
		NginxLink:     filepath.Join(directory, "etc", "nginx", "snippets", "runtime.conf"),
		PythonSnippet: filepath.Join(directory, "etc", "nginx", "snippets", "python.conf"),
		GoSnippet:     filepath.Join(directory, "etc", "nginx", "snippets", "go.conf"),
		GoWebSocket:   filepath.Join(directory, "run", "web.sock"),
		LocalUnitDir:  filepath.Join(directory, "etc", "systemd", "system"),
		Systemctl:     "systemctl", Nginx: "nginx", Curl: "curl",
		HealthTries: 1,
	}
	if err := os.MkdirAll(filepath.Dir(config.PythonSnippet), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{config.PythonSnippet, config.GoSnippet} {
		if err := os.WriteFile(path, []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(config.StateFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.LocalUnitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.StateFile, []byte(initial+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	switcher, err := New(config, runner)
	if err != nil {
		t.Fatal(err)
	}
	return switcher, config
}

func TestApplyGoHandsHardwareOffBeforeStartingGoHelper(t *testing.T) {
	runner := &fakeRunner{active: map[string]bool{pythonHelper: true, pythonWeb: true}}
	switcher, config := testSwitcher(t, runner, ModePython)
	if err := switcher.Apply(context.Background(), ModeGo); err != nil {
		t.Fatal(err)
	}
	assertModeAndLink(t, config, ModeGo, config.GoSnippet)
	if !runner.active[goHelper] || !runner.active[goWeb] || runner.active[pythonHelper] || runner.active[pythonWeb] {
		t.Fatalf("unexpected active units: %#v", runner.active)
	}
	stopPython := commandIndex(runner.commands, "systemctl", "stop", pythonHelper)
	startGo := commandIndex(runner.commands, "systemctl", "restart", goHelper)
	if stopPython < 0 || startGo < 0 || stopPython >= startGo {
		t.Fatalf("unsafe helper hand-off order: %#v", runner.commands)
	}
}

func TestApplyPythonHandsHardwareBackBeforeNginxSwitch(t *testing.T) {
	runner := &fakeRunner{active: map[string]bool{goHelper: true, goWeb: true}}
	switcher, config := testSwitcher(t, runner, ModeGo)
	if err := switcher.Apply(context.Background(), ModePython); err != nil {
		t.Fatal(err)
	}
	assertModeAndLink(t, config, ModePython, config.PythonSnippet)
	stopGo := commandIndex(runner.commands, "systemctl", "stop", goHelper)
	startPython := commandIndex(runner.commands, "systemctl", "restart", pythonHelper)
	if stopGo < 0 || startPython < 0 || stopGo >= startPython {
		t.Fatalf("unsafe helper rollback order: %#v", runner.commands)
	}
}

func TestFailedGoHelperStartRestoresPythonRuntime(t *testing.T) {
	runner := &fakeRunner{
		active:          map[string]bool{pythonHelper: true, pythonWeb: true},
		failGoHelperRun: true,
	}
	switcher, config := testSwitcher(t, runner, ModePython)
	err := switcher.Apply(context.Background(), ModeGo)
	if err == nil || !strings.Contains(err.Error(), "restored python runtime") {
		t.Fatalf("Apply error = %v", err)
	}
	assertModeAndLink(t, config, ModePython, config.PythonSnippet)
	if !runner.active[pythonHelper] || !runner.active[pythonWeb] || runner.active[goHelper] {
		t.Fatalf("Python runtime was not restored: %#v", runner.active)
	}
}

func TestGoHelperStoppingAtEndOfTransactionRestoresPython(t *testing.T) {
	runner := &fakeRunner{
		active:          map[string]bool{pythonHelper: true, pythonWeb: true},
		stopGoOnDisable: true,
	}
	switcher, config := testSwitcher(t, runner, ModePython)
	err := switcher.Apply(context.Background(), ModeGo)
	if err == nil || !strings.Contains(err.Error(), "selected helper") ||
		!strings.Contains(err.Error(), "restored python runtime") {
		t.Fatalf("Apply error = %v", err)
	}
	assertModeAndLink(t, config, ModePython, config.PythonSnippet)
	if !runner.active[pythonHelper] || !runner.active[pythonWeb] || runner.active[goHelper] {
		t.Fatalf("Python runtime was not restored: %#v", runner.active)
	}
}

func TestStatusReportsSelectedServices(t *testing.T) {
	runner := &fakeRunner{active: map[string]bool{goHelper: true, goWeb: true}}
	switcher, _ := testSwitcher(t, runner, ModeGo)
	status, err := switcher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != "runtime=go helper_active=true web_active=true" {
		t.Fatalf("status = %q", status)
	}
}

func TestApplyGoRefusesUnverifiedLocalUnitOverride(t *testing.T) {
	runner := &fakeRunner{active: map[string]bool{pythonHelper: true, pythonWeb: true}}
	switcher, config := testSwitcher(t, runner, ModePython)
	override := filepath.Join(config.LocalUnitDir, goHelper)
	if err := os.WriteFile(override, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := switcher.Apply(context.Background(), ModeGo)
	if err == nil || !strings.Contains(err.Error(), "unverified local systemd override") {
		t.Fatalf("Apply error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran before override rejection: %#v", runner.commands)
	}
	assertModeOnly(t, config, ModePython)
}

func assertModeAndLink(t *testing.T, config Config, mode Mode, target string) {
	t.Helper()
	assertModeOnly(t, config, mode)
	actualTarget, err := os.Readlink(config.NginxLink)
	if err != nil {
		t.Fatal(err)
	}
	if actualTarget != target {
		t.Fatalf("nginx target = %q, want %q", actualTarget, target)
	}
}

func assertModeOnly(t *testing.T, config Config, mode Mode) {
	t.Helper()
	payload, err := os.ReadFile(config.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(mode)+"\n" {
		t.Fatalf("runtime state = %q", payload)
	}
}

func commandIndex(commands []recordedCommand, program string, args ...string) int {
	for index, command := range commands {
		if command.program == program && reflect.DeepEqual(command.args, args) {
			return index
		}
	}
	return -1
}
