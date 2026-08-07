package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"ups-pi-node/internal/deployruntime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ups-pi-node-runtime:", err)
		os.Exit(1)
	}
}

func run() error {
	var timeout time.Duration
	flag.DurationVar(&timeout, "timeout", 60*time.Second, "maximum hand-off duration")
	flag.Parse()
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root")
	}
	switcher, err := deployruntime.New(deployruntime.DefaultConfig(), deployruntime.OSRunner{})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if flag.NArg() == 1 && flag.Arg(0) == "status" {
		status, err := switcher.Status(ctx)
		if err == nil {
			fmt.Println(status)
		}
		return err
	}
	if flag.NArg() != 2 || flag.Arg(0) != "apply" {
		return fmt.Errorf("usage: ups-pi-node-runtime [--timeout 60s] status | apply {python|go}")
	}
	mode, err := deployruntime.ParseMode(flag.Arg(1))
	if err != nil {
		return err
	}
	if err := switcher.Apply(ctx, mode); err != nil {
		return err
	}
	status, err := switcher.Status(ctx)
	if err == nil {
		fmt.Println(status)
	}
	return err
}
