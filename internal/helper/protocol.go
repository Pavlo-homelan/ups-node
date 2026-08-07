// Package helper implements the version 1 JSON protocol shared by the Python
// application and the phased Go replacement.
package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const MaxRequestBytes = 1024 * 1024

var (
	ErrInvalidRequest    = errors.New("invalid helper request")
	ErrUnsupportedAction = errors.New("unsupported helper action")
)

type Request struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

type Response struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Handler interface {
	Handle(context.Context, string, map[string]any) (any, error)
}

type HandlerFunc func(context.Context, string, map[string]any) (any, error)

func (function HandlerFunc) Handle(ctx context.Context, action string, params map[string]any) (any, error) {
	return function(ctx, action, params)
}

// Router is an explicit action allow-list.
type Router map[string]HandlerFunc

func (router Router) Handle(ctx context.Context, action string, params map[string]any) (any, error) {
	handler, ok := router[action]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAction, action)
	}
	return handler(ctx, action, params)
}

func DecodeRequest(payload []byte) (Request, error) {
	var raw struct {
		Action any             `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Request{}, fmt.Errorf("%w: malformed JSON: %v", ErrInvalidRequest, err)
	}

	action, ok := raw.Action.(string)
	if !ok || action == "" {
		return Request{}, fmt.Errorf("%w: action must be a non-empty string", ErrInvalidRequest)
	}

	params := make(map[string]any)
	if len(raw.Params) > 0 && string(raw.Params) != "null" {
		if err := json.Unmarshal(raw.Params, &params); err != nil {
			return Request{}, fmt.Errorf("%w: params must be an object", ErrInvalidRequest)
		}
	}
	return Request{Action: action, Params: params}, nil
}
