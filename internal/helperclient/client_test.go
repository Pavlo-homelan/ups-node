package helperclient

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

type halfClosePipe struct{ net.Conn }

func (halfClosePipe) CloseWrite() error { return nil }

func pipeClient(server func(net.Conn)) Client {
	return Client{
		SocketPath: "test.sock",
		Timeout:    time.Second,
		dial: func(context.Context) (net.Conn, error) {
			client, peer := net.Pipe()
			go server(peer)
			return halfClosePipe{client}, nil
		},
	}
}

func TestClientUsesPythonCompatibleHalfCloseProtocol(t *testing.T) {
	done := make(chan error, 1)
	client := pipeClient(func(connection net.Conn) {
		defer connection.Close()
		var request map[string]any
		if decodeErr := json.NewDecoder(connection).Decode(&request); decodeErr != nil {
			done <- decodeErr
			return
		}
		done <- json.NewEncoder(connection).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"state": "connected"},
		})
	})

	var response struct {
		State string `json:"state"`
	}
	if err := client.Call(context.Background(), "wifi.status", map[string]any{"interface": "wlan0"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.State != "connected" {
		t.Fatalf("response = %#v", response)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientReturnsHelperErrorWithoutDecodingData(t *testing.T) {
	client := pipeClient(func(connection net.Conn) {
		defer connection.Close()
		_ = json.NewDecoder(connection).Decode(&map[string]any{})
		_ = json.NewEncoder(connection).Encode(map[string]any{
			"success": false, "error": "denied",
		})
	})
	if err := client.Call(context.Background(), "auth.pam", nil, nil); err == nil || err.Error() != "denied" {
		t.Fatalf("error = %v", err)
	}
}
