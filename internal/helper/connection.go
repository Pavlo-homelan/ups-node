package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
)

type ConnectionServer struct {
	Handler Handler
	Logger  *slog.Logger
}

func (server ConnectionServer) ServeConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()

	response := server.safeResponse(ctx, connection)
	if err := json.NewEncoder(connection).Encode(response); err != nil && server.Logger != nil {
		server.Logger.Warn("write helper response", "error", err)
	}
}

func (server ConnectionServer) safeResponse(ctx context.Context, reader io.Reader) (response Response) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = failure("internal helper error")
			if server.Logger != nil {
				server.Logger.Error("helper handler panic", "panic", recovered)
			}
		}
	}()
	return server.response(ctx, reader)
}

func (server ConnectionServer) response(ctx context.Context, reader io.Reader) Response {
	payload, err := io.ReadAll(io.LimitReader(reader, MaxRequestBytes+1))
	if err != nil {
		return failure("failed to read helper request")
	}
	if len(payload) > MaxRequestBytes {
		return failure("helper request is too large")
	}

	request, err := DecodeRequest(payload)
	if err != nil {
		return failure(err.Error())
	}
	if server.Handler == nil {
		return failure("helper has no request handler")
	}

	data, err := server.Handler.Handle(ctx, request.Action, request.Params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return failure("helper request was canceled")
		}
		return failure(err.Error())
	}
	return Response{Success: true, Data: data}
}

func failure(message string) Response {
	return Response{Success: false, Error: message}
}
