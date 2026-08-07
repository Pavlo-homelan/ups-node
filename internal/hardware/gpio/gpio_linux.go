//go:build linux

// Package gpio provides the Linux GPIO character-device implementation used by
// the Go hardware controller.
package gpio

import (
	"fmt"

	"github.com/warthog618/go-gpiocdev"
)

type Backend struct {
	relay1 *gpiocdev.Line
	relay2 *gpiocdev.Line
	ac     *gpiocdev.Line
}

func Open(chip string, relay1Offset int, relay2Offset int, acOffset int) (*Backend, error) {
	relay1, err := gpiocdev.RequestLine(
		chip,
		relay1Offset,
		gpiocdev.AsOutput(1),
		gpiocdev.WithConsumer("ups-pi-node-relay-1"),
	)
	if err != nil {
		return nil, fmt.Errorf("request relay 1 GPIO%d: %w", relay1Offset, err)
	}
	relay2, err := gpiocdev.RequestLine(
		chip,
		relay2Offset,
		gpiocdev.AsOutput(1),
		gpiocdev.WithConsumer("ups-pi-node-relay-2"),
	)
	if err != nil {
		relay1.Close()
		return nil, fmt.Errorf("request relay 2 GPIO%d: %w", relay2Offset, err)
	}
	ac, err := gpiocdev.RequestLine(
		chip,
		acOffset,
		gpiocdev.AsInput,
		gpiocdev.WithPullUp,
		gpiocdev.WithConsumer("ups-pi-node-ac-detect"),
	)
	if err != nil {
		relay2.Close()
		relay1.Close()
		return nil, fmt.Errorf("request AC detect GPIO%d: %w", acOffset, err)
	}
	return &Backend{relay1: relay1, relay2: relay2, ac: ac}, nil
}

func (backend *Backend) ACOK() (bool, error) {
	value, err := backend.ac.Value()
	if err != nil {
		return false, fmt.Errorf("read AC detect GPIO: %w", err)
	}
	return value == 0, nil
}

func (backend *Backend) SetRelay1(high bool) error {
	return backend.relay1.SetValue(boolValue(high))
}

func (backend *Backend) SetRelay2(high bool) error {
	return backend.relay2.SetValue(boolValue(high))
}

func (backend *Backend) Close() error {
	var firstError error
	for _, line := range []*gpiocdev.Line{backend.ac, backend.relay2, backend.relay1} {
		if line == nil {
			continue
		}
		if err := line.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}

func boolValue(value bool) int {
	if value {
		return 1
	}
	return 0
}
