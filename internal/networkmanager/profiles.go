package networkmanager

import (
	"context"
	"crypto/rand"
	"crypto/sha1" // Compatibility with profile names created by the Python helper.
	"fmt"
	"strings"
	"unicode"

	"github.com/godbus/dbus/v5"
)

const settingsToDisk uint32 = 0x1

type connectionSettings map[string]map[string]dbus.Variant

func (client *Client) Connect(
	ctx context.Context,
	interfaceName string,
	ssid string,
	password string,
	hidden bool,
	ipv6Mode string,
) (ConnectResult, error) {
	if ssid == "" {
		return ConnectResult{}, fmt.Errorf("SSID must not be empty")
	}
	devicePath, err := client.devicePath(ctx, interfaceName)
	if err != nil {
		return ConnectResult{}, err
	}

	connectionID := managedConnectionName(ssid)
	profilePath, existing, err := client.findConnection(ctx, connectionID)
	if err != nil {
		return ConnectResult{}, err
	}

	if existing == nil {
		settings, buildErr := buildConnectionSettings(interfaceName, connectionID, ssid, password, hidden, ipv6Mode)
		if buildErr != nil {
			return ConnectResult{}, buildErr
		}
		profilePath, err = client.addConnection(ctx, settings)
		if err != nil {
			return ConnectResult{}, err
		}
	} else {
		if err := updateConnectionSettings(existing, interfaceName, connectionID, ssid, password, hidden, ipv6Mode); err != nil {
			return ConnectResult{}, err
		}
		if err := client.updateConnection(ctx, profilePath, existing); err != nil {
			return ConnectResult{}, err
		}
	}

	if _, err := client.activate(ctx, profilePath, devicePath); err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{
		Message: fmt.Sprintf("NetworkManager activation started for network %q.", ssid),
	}, nil
}

func (client *Client) findConnection(
	ctx context.Context,
	connectionID string,
) (dbus.ObjectPath, connectionSettings, error) {
	var paths []dbus.ObjectPath
	err := client.bus.Object(busName, settingsPath).
		CallWithContext(ctx, settingsInterface+".ListConnections", 0).
		Store(&paths)
	if err != nil {
		return "", nil, fmt.Errorf("list NetworkManager profiles: %w", err)
	}
	for _, path := range paths {
		settings, readErr := client.getSettings(ctx, path)
		if readErr != nil {
			continue
		}
		connection := settings["connection"]
		if variantString(connection["id"]) == connectionID &&
			variantString(connection["type"]) == "802-11-wireless" {
			return path, settings, nil
		}
	}
	return "", nil, nil
}

func (client *Client) getSettings(
	ctx context.Context,
	path dbus.ObjectPath,
) (connectionSettings, error) {
	settings := make(connectionSettings)
	err := client.bus.Object(busName, path).
		CallWithContext(ctx, connectionInterface+".GetSettings", 0).
		Store(&settings)
	if err != nil {
		return nil, fmt.Errorf("read NetworkManager profile: %w", err)
	}
	return settings, nil
}

func (client *Client) addConnection(
	ctx context.Context,
	settings connectionSettings,
) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	var result map[string]dbus.Variant
	err := client.bus.Object(busName, settingsPath).
		CallWithContext(
			ctx,
			settingsInterface+".AddConnection2",
			0,
			settings,
			settingsToDisk,
			map[string]dbus.Variant{},
		).
		Store(&path, &result)
	if err != nil {
		return "", fmt.Errorf("create NetworkManager profile: %w", err)
	}
	return path, nil
}

func (client *Client) updateConnection(
	ctx context.Context,
	path dbus.ObjectPath,
	settings connectionSettings,
) error {
	var result map[string]dbus.Variant
	err := client.bus.Object(busName, path).
		CallWithContext(
			ctx,
			connectionInterface+".Update2",
			0,
			settings,
			settingsToDisk,
			map[string]dbus.Variant{},
		).
		Store(&result)
	if err != nil {
		return fmt.Errorf("update NetworkManager profile: %w", err)
	}
	return nil
}

func buildConnectionSettings(
	interfaceName string,
	connectionID string,
	ssid string,
	password string,
	hidden bool,
	ipv6Mode string,
) (connectionSettings, error) {
	ipv6Method, err := networkManagerIPv6Method(ipv6Mode)
	if err != nil {
		return nil, err
	}
	uuid, err := randomUUID()
	if err != nil {
		return nil, fmt.Errorf("generate NetworkManager profile UUID: %w", err)
	}
	settings := connectionSettings{
		"connection": {
			"id":             dbus.MakeVariant(connectionID),
			"uuid":           dbus.MakeVariant(uuid),
			"type":           dbus.MakeVariant("802-11-wireless"),
			"interface-name": dbus.MakeVariant(interfaceName),
			"autoconnect":    dbus.MakeVariant(true),
		},
		"802-11-wireless": {
			"ssid":   dbus.MakeVariant([]byte(ssid)),
			"mode":   dbus.MakeVariant("infrastructure"),
			"hidden": dbus.MakeVariant(hidden),
		},
		"ipv4": {"method": dbus.MakeVariant("auto")},
		"ipv6": {"method": dbus.MakeVariant(ipv6Method)},
	}
	if password != "" {
		settings["802-11-wireless-security"] = securitySettings(password)
	}
	return settings, nil
}

func updateConnectionSettings(
	settings connectionSettings,
	interfaceName string,
	connectionID string,
	ssid string,
	password string,
	hidden bool,
	ipv6Mode string,
) error {
	ipv6Method, err := networkManagerIPv6Method(ipv6Mode)
	if err != nil {
		return err
	}
	if settings["connection"] == nil {
		settings["connection"] = make(map[string]dbus.Variant)
	}
	settings["connection"]["id"] = dbus.MakeVariant(connectionID)
	settings["connection"]["type"] = dbus.MakeVariant("802-11-wireless")
	settings["connection"]["interface-name"] = dbus.MakeVariant(interfaceName)
	settings["connection"]["autoconnect"] = dbus.MakeVariant(true)

	if settings["802-11-wireless"] == nil {
		settings["802-11-wireless"] = make(map[string]dbus.Variant)
	}
	settings["802-11-wireless"]["ssid"] = dbus.MakeVariant([]byte(ssid))
	settings["802-11-wireless"]["mode"] = dbus.MakeVariant("infrastructure")
	settings["802-11-wireless"]["hidden"] = dbus.MakeVariant(hidden)
	if password != "" {
		settings["802-11-wireless-security"] = securitySettings(password)
	}
	settings["ipv4"] = map[string]dbus.Variant{"method": dbus.MakeVariant("auto")}
	settings["ipv6"] = map[string]dbus.Variant{"method": dbus.MakeVariant(ipv6Method)}
	return nil
}

func securitySettings(password string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"key-mgmt": dbus.MakeVariant("wpa-psk"),
		"psk":      dbus.MakeVariant(password),
	}
}

func networkManagerIPv6Method(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "disabled", "disable", "off", "false", "no", "0", "ignore":
		return "ignore", nil
	case "auto", "enabled", "enable", "on", "true", "yes", "1":
		return "auto", nil
	default:
		return "", fmt.Errorf("unsupported Wi-Fi IPv6 mode %q", mode)
	}
}

func managedConnectionName(ssid string) string {
	digest := fmt.Sprintf("%x", sha1.Sum([]byte(ssid)))[:8]
	visible := make([]rune, 0, len(ssid))
	for _, character := range ssid {
		if unicode.IsLetter(character) || unicode.IsNumber(character) || strings.ContainsRune("._-", character) {
			visible = append(visible, character)
		} else {
			visible = append(visible, '-')
		}
	}
	nameRunes := []rune(strings.Trim(string(visible), "-"))
	if len(nameRunes) > 24 {
		nameRunes = nameRunes[:24]
	}
	name := string(nameRunes)
	if name == "" {
		name = "network"
	}
	return fmt.Sprintf("ups-pi-node-%s-%s", name, digest)
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}

func variantString(variant dbus.Variant) string {
	value, _ := variant.Value().(string)
	return value
}
