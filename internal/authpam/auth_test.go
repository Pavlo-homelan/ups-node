package authpam

import (
	"context"
	"testing"
)

type fakeCommand struct {
	called   int
	program  string
	service  string
	username string
	password string
	result   bool
	err      error
}

func (command *fakeCommand) Authenticate(
	_ context.Context,
	program string,
	service string,
	username string,
	password string,
) (bool, error) {
	command.called++
	command.program = program
	command.service = service
	command.username = username
	command.password = password
	return command.result, command.err
}

func TestAuthenticatorUsesFixedPAMServiceAndStdinCommandBoundary(t *testing.T) {
	runner := &fakeCommand{result: true}
	authenticator := New("/usr/bin/pamtester", "ups-pi-node")
	authenticator.command = runner
	authenticated, err := authenticator.Authenticate(context.Background(), "admin", "secret", "ups-pi-node")
	if err != nil || !authenticated {
		t.Fatalf("authenticated=%v error=%v", authenticated, err)
	}
	if runner.called != 1 || runner.username != "admin" || runner.password != "secret" ||
		runner.service != "ups-pi-node" || runner.program != "/usr/bin/pamtester" {
		t.Fatalf("runner = %#v", runner)
	}
}

func TestAuthenticatorRejectsAlternateServiceAndInvalidInputsWithoutRunningPAM(t *testing.T) {
	runner := &fakeCommand{result: true}
	authenticator := New("/usr/bin/pamtester", "ups-pi-node")
	authenticator.command = runner
	if _, err := authenticator.Authenticate(context.Background(), "admin", "secret", "login"); err == nil {
		t.Fatal("alternate PAM service was accepted")
	}
	for _, input := range []struct{ username, password string }{
		{"bad user", "secret"}, {"admin", ""}, {"admin", "line\nbreak"},
	} {
		authenticated, err := authenticator.Authenticate(context.Background(), input.username, input.password, "")
		if err != nil || authenticated {
			t.Fatalf("invalid input authenticated=%v error=%v", authenticated, err)
		}
	}
	if runner.called != 0 {
		t.Fatalf("PAM command called %d times", runner.called)
	}
}

func TestRouterReturnsOnlyAuthenticationBoolean(t *testing.T) {
	runner := &fakeCommand{result: false}
	authenticator := New("pamtester", "ups-pi-node")
	authenticator.command = runner
	result, err := Router(authenticator).Handle(context.Background(), "auth.pam", map[string]any{
		"username": "admin", "password": "wrong",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(map[string]bool)
	if !ok || response["authenticated"] {
		t.Fatalf("response = %#v", result)
	}
}
