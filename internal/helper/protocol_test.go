package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestConnectionProtocol(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		handler Handler
		wantOK  bool
		wantErr string
	}{
		{
			name:    "compatible request and response",
			payload: `{"action":"wifi.status","params":{"interface":"wlan0"}}`,
			handler: Router{
				"wifi.status": func(_ context.Context, _ string, params map[string]any) (any, error) {
					return map[string]any{"state": "connected", "interface": params["interface"]}, nil
				},
			},
			wantOK: true,
		},
		{
			name:    "missing params defaults to object",
			payload: `{"action":"ups.status"}`,
			handler: Router{
				"ups.status": func(_ context.Context, _ string, params map[string]any) (any, error) {
					if params == nil {
						t.Fatal("params must not be nil")
					}
					return map[string]any{"v": 12.6}, nil
				},
			},
			wantOK: true,
		},
		{
			name:    "malformed JSON",
			payload: `{"action":`,
			handler: Router{},
			wantErr: "malformed JSON",
		},
		{
			name:    "params must be object",
			payload: `{"action":"wifi.scan","params":[]}`,
			handler: Router{},
			wantErr: "params must be an object",
		},
		{
			name:    "unknown action is rejected",
			payload: `{"action":"system.shell","params":{}}`,
			handler: Router{},
			wantErr: "unsupported helper action",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := exchange(t, test.payload, test.handler)
			if response.Success != test.wantOK {
				t.Fatalf("success = %v, want %v; error = %q", response.Success, test.wantOK, response.Error)
			}
			if test.wantErr != "" && !strings.Contains(response.Error, test.wantErr) {
				t.Fatalf("error = %q, want substring %q", response.Error, test.wantErr)
			}
		})
	}
}

func TestOversizedRequestIsRejected(t *testing.T) {
	response := exchange(t, strings.Repeat("x", MaxRequestBytes+1), Router{})
	if response.Success || !strings.Contains(response.Error, "too large") {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestConnectionServerContainsHandlerPanic(t *testing.T) {
	server := ConnectionServer{Handler: HandlerFunc(func(context.Context, string, map[string]any) (any, error) {
		panic("broken backend")
	})}
	response := server.safeResponse(
		context.Background(),
		bytes.NewReader([]byte(`{"action":"test","params":{}}`)),
	)
	if response.Success || response.Error != "internal helper error" {
		t.Fatalf("panic response = %#v", response)
	}
}

func exchange(t *testing.T, payload string, handler Handler) Response {
	t.Helper()
	server := ConnectionServer{Handler: handler}
	connection := newMemoryConnection(payload)
	server.ServeConnection(context.Background(), connection)

	var response Response
	if err := json.NewDecoder(&connection.response).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

type memoryConnection struct {
	request  *bytes.Reader
	response bytes.Buffer
}

func newMemoryConnection(payload string) *memoryConnection {
	return &memoryConnection{request: bytes.NewReader([]byte(payload))}
}

func (connection *memoryConnection) Read(buffer []byte) (int, error) {
	return connection.request.Read(buffer)
}

func (connection *memoryConnection) Write(buffer []byte) (int, error) {
	return connection.response.Write(buffer)
}

func (connection *memoryConnection) Close() error                     { return nil }
func (connection *memoryConnection) LocalAddr() net.Addr              { return memoryAddress("local") }
func (connection *memoryConnection) RemoteAddr() net.Addr             { return memoryAddress("remote") }
func (connection *memoryConnection) SetDeadline(time.Time) error      { return nil }
func (connection *memoryConnection) SetReadDeadline(time.Time) error  { return nil }
func (connection *memoryConnection) SetWriteDeadline(time.Time) error { return nil }

type memoryAddress string

func (address memoryAddress) Network() string { return "memory" }
func (address memoryAddress) String() string  { return string(address) }
