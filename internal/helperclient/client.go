// Package helperclient is the unprivileged client for the version 1 JSON Unix
// protocol shared by the Python and Go helpers.
package helperclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"ups-pi-node/internal/helper"
)

const MaxResponseBytes = 1024 * 1024

type Caller interface {
	Call(context.Context, string, map[string]any, any) error
}

type Client struct {
	SocketPath string
	Timeout    time.Duration
	dial       func(context.Context) (net.Conn, error)
}

func (client Client) Call(
	ctx context.Context,
	action string,
	params map[string]any,
	target any,
) error {
	if client.SocketPath == "" {
		return fmt.Errorf("helper socket path is empty")
	}
	if params == nil {
		params = map[string]any{}
	}
	payload, err := json.Marshal(helper.Request{Action: action, Params: params})
	if err != nil {
		return fmt.Errorf("encode helper request: %w", err)
	}

	connection, err := client.connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to system helper: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(client.timeout())
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set helper deadline: %w", err)
	}
	if _, err := connection.Write(payload); err != nil {
		return fmt.Errorf("write helper request: %w", err)
	}
	unixConnection, ok := connection.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("helper connection is not a Unix stream")
	}
	if err := unixConnection.CloseWrite(); err != nil {
		return fmt.Errorf("finish helper request: %w", err)
	}

	responsePayload, err := io.ReadAll(io.LimitReader(connection, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read helper response: %w", err)
	}
	if len(responsePayload) > MaxResponseBytes {
		return fmt.Errorf("system helper response is too large")
	}
	var response struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return fmt.Errorf("decode helper response: %w", err)
	}
	if !response.Success {
		if response.Error == "" {
			response.Error = "system helper returned an error"
		}
		return fmt.Errorf("%s", response.Error)
	}
	if target == nil || len(response.Data) == 0 || string(response.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Data, target); err != nil {
		return fmt.Errorf("decode helper data for %s: %w", action, err)
	}
	return nil
}

func (client Client) connect(ctx context.Context) (net.Conn, error) {
	if client.dial != nil {
		return client.dial(ctx)
	}
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "unix", client.SocketPath)
}

func (client Client) timeout() time.Duration {
	if client.Timeout <= 0 {
		return 25 * time.Second
	}
	return client.Timeout
}
