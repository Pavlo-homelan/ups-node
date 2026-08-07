package networkmanager

import (
	"context"
	"fmt"

	"ups-pi-node/internal/helper"
)

func Router(client *Client) helper.Router {
	return helper.Router{
		"wifi.status": func(ctx context.Context, _ string, params map[string]any) (any, error) {
			interfaceName, err := requiredString(params, "interface")
			if err != nil {
				return nil, err
			}
			hotspot, err := requiredString(params, "hotspot_connection_name")
			if err != nil {
				return nil, err
			}
			portalMode, _ := params["portal_mode"].(string)
			return client.Status(ctx, interfaceName, hotspot, portalMode)
		},
		"wifi.scan": func(ctx context.Context, _ string, params map[string]any) (any, error) {
			interfaceName, err := requiredString(params, "interface")
			if err != nil {
				return nil, err
			}
			return client.Scan(ctx, interfaceName)
		},
		"wifi.connect": func(ctx context.Context, _ string, params map[string]any) (any, error) {
			interfaceName, err := requiredString(params, "interface")
			if err != nil {
				return nil, err
			}
			ssid, err := requiredString(params, "ssid")
			if err != nil {
				return nil, err
			}
			password, _ := params["password"].(string)
			hidden, _ := params["hidden"].(bool)
			ipv6Mode, _ := params["ipv6"].(string)
			return client.Connect(ctx, interfaceName, ssid, password, hidden, ipv6Mode)
		},
	}
}

func requiredString(params map[string]any, name string) (string, error) {
	value, ok := params[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("required parameter %q is missing", name)
	}
	return value, nil
}
