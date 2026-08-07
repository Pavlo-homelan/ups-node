package networkmanager

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	busName              = "org.freedesktop.NetworkManager"
	managerPath          = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	settingsPath         = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")
	rootPath             = dbus.ObjectPath("/")
	managerInterface     = "org.freedesktop.NetworkManager"
	settingsInterface    = "org.freedesktop.NetworkManager.Settings"
	connectionInterface  = "org.freedesktop.NetworkManager.Settings.Connection"
	deviceInterface      = "org.freedesktop.NetworkManager.Device"
	wirelessInterface    = "org.freedesktop.NetworkManager.Device.Wireless"
	accessPointInterface = "org.freedesktop.NetworkManager.AccessPoint"
	activeInterface      = "org.freedesktop.NetworkManager.Connection.Active"
	ip4ConfigInterface   = "org.freedesktop.NetworkManager.IP4Config"
	propertiesInterface  = "org.freedesktop.DBus.Properties"
)

type objectProvider interface {
	Object(string, dbus.ObjectPath) dbus.BusObject
}

type Client struct {
	bus   objectProvider
	close func() error
}

func ConnectSystemBus() (*Client, error) {
	connection, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system D-Bus: %w", err)
	}
	return &Client{bus: connection, close: connection.Close}, nil
}

func (client *Client) Close() error {
	if client == nil || client.close == nil {
		return nil
	}
	return client.close()
}

func (client *Client) devicePath(ctx context.Context, interfaceName string) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	err := client.bus.Object(busName, managerPath).
		CallWithContext(ctx, managerInterface+".GetDeviceByIpIface", 0, interfaceName).
		Store(&path)
	if err != nil {
		return "", fmt.Errorf("find Wi-Fi interface %q: %w", interfaceName, err)
	}
	return path, nil
}

func (client *Client) property(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (dbus.Variant, error) {
	var value dbus.Variant
	err := client.bus.Object(busName, path).
		CallWithContext(ctx, propertiesInterface+".Get", 0, interfaceName, propertyName).
		Store(&value)
	if err != nil {
		return dbus.Variant{}, fmt.Errorf("read NetworkManager property %s.%s: %w", interfaceName, propertyName, err)
	}
	return value, nil
}

func (client *Client) activate(
	ctx context.Context,
	connectionPath dbus.ObjectPath,
	devicePath dbus.ObjectPath,
) (dbus.ObjectPath, error) {
	var activePath dbus.ObjectPath
	err := client.bus.Object(busName, managerPath).
		CallWithContext(
			ctx,
			managerInterface+".ActivateConnection",
			0,
			connectionPath,
			devicePath,
			rootPath,
		).
		Store(&activePath)
	if err != nil {
		return "", fmt.Errorf("activate NetworkManager connection: %w", err)
	}
	return activePath, nil
}
