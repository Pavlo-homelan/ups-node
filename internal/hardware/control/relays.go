package control

import (
	"context"
	"fmt"
	"time"
)

type RelayOutput interface {
	SetRelay1(high bool) error
	SetRelay2(high bool) error
}

type WaitFunc func(context.Context, time.Duration) error

func ApplyRelays(
	ctx context.Context,
	output RelayOutput,
	decision Decision,
	wait WaitFunc,
) error {
	if decision.Relay2First {
		if err := output.SetRelay2(decision.Relay2High); err != nil {
			return fmt.Errorf("set relay 2: %w", err)
		}
		if wait == nil {
			wait = Wait
		}
		if err := wait(ctx, decision.SwitchDelay); err != nil {
			return err
		}
		if err := output.SetRelay1(decision.Relay1High); err != nil {
			return fmt.Errorf("set relay 1: %w", err)
		}
		return nil
	}

	if err := output.SetRelay1(decision.Relay1High); err != nil {
		return fmt.Errorf("set relay 1: %w", err)
	}
	if err := output.SetRelay2(decision.Relay2High); err != nil {
		return fmt.Errorf("set relay 2: %w", err)
	}
	return nil
}

func Wait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
