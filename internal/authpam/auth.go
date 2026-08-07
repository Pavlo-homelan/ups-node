// Package authpam exposes a narrow PAM authentication action without linking
// the long-running Go helper to cgo. The root-owned pamtester executable reads
// the password from stdin; credentials never appear in argv or logs.
package authpam

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"ups-pi-node/internal/helper"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type Command interface {
	Authenticate(context.Context, string, string, string, string) (bool, error)
}

type Authenticator struct {
	Program string
	Service string
	command Command
}

func New(program string, service string) *Authenticator {
	if program == "" {
		program = "/usr/bin/pamtester"
	}
	if service == "" {
		service = "ups-pi-node"
	}
	return &Authenticator{Program: program, Service: service, command: commandRunner{}}
}

func (authenticator *Authenticator) Authenticate(
	ctx context.Context,
	username string,
	password string,
	service string,
) (bool, error) {
	if authenticator == nil {
		return false, fmt.Errorf("PAM authenticator is not configured")
	}
	if service == "" {
		service = authenticator.Service
	}
	if service != authenticator.Service {
		return false, fmt.Errorf("PAM service %q is not allowed", service)
	}
	if !usernamePattern.MatchString(username) || password == "" || len(password) > 4096 ||
		strings.ContainsAny(password, "\r\n") {
		return false, nil
	}
	return authenticator.command.Authenticate(ctx, authenticator.Program, service, username, password)
}

func Router(authenticator *Authenticator) helper.Router {
	return helper.Router{
		"auth.pam": func(ctx context.Context, _ string, params map[string]any) (any, error) {
			username, _ := params["username"].(string)
			password, _ := params["password"].(string)
			service, _ := params["service"].(string)
			authenticated, err := authenticator.Authenticate(ctx, username, password, service)
			if err != nil {
				return nil, err
			}
			return map[string]bool{"authenticated": authenticated}, nil
		},
	}
}

type commandRunner struct{}

func (commandRunner) Authenticate(
	ctx context.Context,
	program string,
	service string,
	username string,
	password string,
) (bool, error) {
	command := exec.CommandContext(ctx, program, service, username, "authenticate", "acct_mgmt")
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	command.Stdin = strings.NewReader(password + "\n")
	// PAM module messages are intentionally discarded: a module must not be
	// able to reflect credentials or account details into helper responses.
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return false, nil
		}
		return false, fmt.Errorf("start PAM authentication backend: %w", err)
	}
	return true, nil
}
