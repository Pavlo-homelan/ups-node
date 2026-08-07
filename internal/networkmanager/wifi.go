package networkmanager

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	deviceStateUnavailable  uint32 = 20
	deviceStateDisconnected uint32 = 30
	deviceStateActivated    uint32 = 100
	deviceStateDeactivating uint32 = 110
	deviceStateFailed       uint32 = 120

	accessPointPrivacy uint32 = 0x1
)

func (client *Client) Status(
	ctx context.Context,
	interfaceName string,
	hotspotConnection string,
	configuredPortalMode string,
) (Status, error) {
	devicePath, err := client.devicePath(ctx, interfaceName)
	if err != nil {
		return Status{}, err
	}

	stateVariant, err := client.property(ctx, devicePath, deviceInterface, "State")
	if err != nil {
		return Status{}, err
	}
	var rawState uint32
	if err := stateVariant.Store(&rawState); err != nil {
		return Status{}, fmt.Errorf("decode Wi-Fi device state: %w", err)
	}
	state := deviceStateName(rawState)

	connection, err := client.activeConnectionID(ctx, devicePath)
	if err != nil {
		return Status{}, err
	}
	ipAddress, err := client.ipv4Address(ctx, devicePath)
	if err != nil {
		return Status{}, err
	}

	portalMode := configuredPortalMode
	if portalMode != "ap" && portalMode != "client" {
		if connection != nil && *connection == hotspotConnection {
			portalMode = "ap"
		} else if state == "connected" {
			portalMode = "client"
		} else {
			portalMode = "ap"
		}
	}

	var connectedSSID *string
	if portalMode == "client" {
		connectedSSID = connection
	}
	return Status{
		PortalMode:    portalMode,
		ConnectedSSID: connectedSSID,
		Connection:    connection,
		IPAddress:     ipAddress,
		State:         state,
	}, nil
}

func (client *Client) activeConnectionID(
	ctx context.Context,
	devicePath dbus.ObjectPath,
) (*string, error) {
	variant, err := client.property(ctx, devicePath, deviceInterface, "ActiveConnection")
	if err != nil {
		return nil, err
	}
	var activePath dbus.ObjectPath
	if err := variant.Store(&activePath); err != nil {
		return nil, fmt.Errorf("decode active connection path: %w", err)
	}
	if activePath == "" || activePath == rootPath {
		return nil, nil
	}

	variant, err = client.property(ctx, activePath, activeInterface, "Id")
	if err != nil {
		return nil, err
	}
	var connectionID string
	if err := variant.Store(&connectionID); err != nil {
		return nil, fmt.Errorf("decode active connection id: %w", err)
	}
	return &connectionID, nil
}

func (client *Client) ipv4Address(
	ctx context.Context,
	devicePath dbus.ObjectPath,
) (*string, error) {
	variant, err := client.property(ctx, devicePath, deviceInterface, "Ip4Config")
	if err != nil {
		return nil, err
	}
	var configPath dbus.ObjectPath
	if err := variant.Store(&configPath); err != nil {
		return nil, fmt.Errorf("decode IPv4 config path: %w", err)
	}
	if configPath == "" || configPath == rootPath {
		return nil, nil
	}

	variant, err = client.property(ctx, configPath, ip4ConfigInterface, "AddressData")
	if err != nil {
		return nil, err
	}
	var addresses []map[string]dbus.Variant
	if err := variant.Store(&addresses); err != nil {
		return nil, fmt.Errorf("decode IPv4 addresses: %w", err)
	}
	for _, address := range addresses {
		value, ok := address["address"]
		if !ok {
			continue
		}
		var text string
		if err := value.Store(&text); err == nil && text != "" {
			return &text, nil
		}
	}
	return nil, nil
}

func (client *Client) Scan(ctx context.Context, interfaceName string) (ScanResult, error) {
	devicePath, err := client.devicePath(ctx, interfaceName)
	if err != nil {
		return ScanResult{}, err
	}

	lastScan := client.lastScan(ctx, devicePath)
	call := client.bus.Object(busName, devicePath).CallWithContext(
		ctx,
		wirelessInterface+".RequestScan",
		0,
		map[string]dbus.Variant{},
	)
	if call.Err != nil {
		return ScanResult{}, fmt.Errorf("request Wi-Fi scan: %w", call.Err)
	}
	client.waitForScan(ctx, devicePath, lastScan)

	var accessPoints []dbus.ObjectPath
	err = client.bus.Object(busName, devicePath).
		CallWithContext(ctx, wirelessInterface+".GetAllAccessPoints", 0).
		Store(&accessPoints)
	if err != nil {
		return ScanResult{}, fmt.Errorf("list Wi-Fi access points: %w", err)
	}

	activePath := rootPath
	if variant, propertyErr := client.property(ctx, devicePath, wirelessInterface, "ActiveAccessPoint"); propertyErr == nil {
		_ = variant.Store(&activePath)
	}
	discovered := make(map[string]Network)
	for _, accessPointPath := range accessPoints {
		network, readErr := client.readAccessPoint(ctx, accessPointPath, accessPointPath == activePath)
		if readErr != nil {
			continue
		}
		existing, exists := discovered[network.SSID]
		if !exists || network.Connected || network.Signal > existing.Signal {
			discovered[network.SSID] = network
		}
	}

	networks := make([]Network, 0, len(discovered))
	for _, network := range discovered {
		networks = append(networks, network)
	}
	sort.Slice(networks, func(left int, right int) bool {
		if networks[left].Connected != networks[right].Connected {
			return networks[left].Connected
		}
		if networks[left].Signal != networks[right].Signal {
			return networks[left].Signal > networks[right].Signal
		}
		return networks[left].SSID < networks[right].SSID
	})
	return ScanResult{Networks: networks}, nil
}

func (client *Client) lastScan(ctx context.Context, devicePath dbus.ObjectPath) int64 {
	variant, err := client.property(ctx, devicePath, wirelessInterface, "LastScan")
	if err != nil {
		return -1
	}
	var value int64
	if err := variant.Store(&value); err != nil {
		return -1
	}
	return value
}

func (client *Client) waitForScan(ctx context.Context, devicePath dbus.ObjectPath, previous int64) {
	deadline := time.NewTimer(8 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			if current := client.lastScan(ctx, devicePath); current >= 0 && current != previous {
				return
			}
		}
	}
}

func (client *Client) readAccessPoint(
	ctx context.Context,
	path dbus.ObjectPath,
	connected bool,
) (Network, error) {
	ssidVariant, err := client.property(ctx, path, accessPointInterface, "Ssid")
	if err != nil {
		return Network{}, err
	}
	var ssidBytes []byte
	if err := ssidVariant.Store(&ssidBytes); err != nil {
		return Network{}, fmt.Errorf("decode access point SSID: %w", err)
	}
	ssid := string(ssidBytes)
	if ssid == "" {
		ssid = "Скрытая сеть"
	}

	strengthVariant, err := client.property(ctx, path, accessPointInterface, "Strength")
	if err != nil {
		return Network{}, err
	}
	var strength uint8
	if err := strengthVariant.Store(&strength); err != nil {
		return Network{}, fmt.Errorf("decode access point signal: %w", err)
	}
	flags := client.uint32Property(ctx, path, accessPointInterface, "Flags")
	wpa := client.uint32Property(ctx, path, accessPointInterface, "WpaFlags")
	rsn := client.uint32Property(ctx, path, accessPointInterface, "RsnFlags")

	return Network{
		SSID:      ssid,
		Signal:    strength,
		Security:  securityName(flags, wpa, rsn),
		Connected: connected,
	}, nil
}

func (client *Client) uint32Property(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	name string,
) uint32 {
	variant, err := client.property(ctx, path, interfaceName, name)
	if err != nil {
		return 0
	}
	var value uint32
	_ = variant.Store(&value)
	return value
}

func deviceStateName(state uint32) string {
	switch {
	case state == 0:
		return "unknown"
	case state <= deviceStateUnavailable:
		return "unavailable"
	case state == deviceStateDisconnected || state == deviceStateFailed:
		return "disconnected"
	case state == deviceStateActivated:
		return "connected"
	case state > deviceStateDisconnected && state <= deviceStateDeactivating:
		return "connecting"
	default:
		return "unknown"
	}
}

func securityName(flags uint32, wpa uint32, rsn uint32) string {
	switch {
	case rsn != 0 && wpa != 0:
		return "WPA WPA2"
	case rsn != 0:
		return "WPA2"
	case wpa != 0:
		return "WPA"
	case flags&accessPointPrivacy != 0:
		return "WEP"
	default:
		return "Open"
	}
}
