package networkmanager

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestConnectUpdatesCanonicalProfileWithoutCreatingDuplicate(t *testing.T) {
	profilePath := dbus.ObjectPath("/profiles/managed")
	devicePath := dbus.ObjectPath("/devices/wlan0")
	activePath := dbus.ObjectPath("/active/1")
	settings, err := buildConnectionSettings(
		"wlan0",
		managedConnectionName("MyRouter"),
		"MyRouter",
		"old-password",
		false,
		"auto",
	)
	if err != nil {
		t.Fatal(err)
	}
	settings["ipv6"]["addresses"] = dbus.MakeVariant([][]dbus.Variant{})
	settings["ipv6"]["dns"] = dbus.MakeVariant([][]dbus.Variant{})

	var methods []string
	var updated connectionSettings
	bus := fakeBus{call: func(path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		methods = append(methods, method)
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			return successfulCall(devicePath)
		case settingsInterface + ".ListConnections":
			return successfulCall([]dbus.ObjectPath{profilePath})
		case connectionInterface + ".GetSettings":
			return successfulCall(settings)
		case connectionInterface + ".Update2":
			updated = args[0].(connectionSettings)
			return successfulCall(map[string]dbus.Variant{})
		case managerInterface + ".ActivateConnection":
			return successfulCall(activePath)
		default:
			return failedCall(errors.New("unexpected D-Bus method: " + method))
		}
	}}
	client := &Client{bus: bus}

	if _, err := client.Connect(context.Background(), "wlan0", "MyRouter", "new-password", false, "disabled"); err != nil {
		t.Fatal(err)
	}
	if updated == nil {
		t.Fatal("existing profile was not updated")
	}
	if actual := variantString(updated["802-11-wireless-security"]["psk"]); actual != "new-password" {
		t.Fatalf("updated PSK = %q", actual)
	}
	if actual := variantString(updated["ipv6"]["method"]); actual != "ignore" {
		t.Fatalf("updated ipv6.method = %q", actual)
	}
	if actual := updated["connection"]["autoconnect-priority"].Value(); actual != managedAutoconnectPriority {
		t.Fatalf("updated autoconnect-priority = %#v", actual)
	}
	if actual := updated["802-11-wireless"]["powersave"].Value(); actual != networkManagerPowerSaveDisable {
		t.Fatalf("updated powersave = %#v", actual)
	}
	if _, exists := updated["ipv6"]["addresses"]; exists {
		t.Fatal("legacy ipv6.addresses was sent back to NetworkManager")
	}
	if _, exists := updated["ipv6"]["dns"]; exists {
		t.Fatal("legacy ipv6.dns was sent back to NetworkManager")
	}
	for _, method := range methods {
		if method == settingsInterface+".AddConnection2" || method == connectionInterface+".Delete" {
			t.Fatalf("same-SSID update used forbidden method %s", method)
		}
	}
}

func TestConnectCreatesOnlyCanonicalProfileWhenMissing(t *testing.T) {
	devicePath := dbus.ObjectPath("/devices/wlan0")
	profilePath := dbus.ObjectPath("/profiles/new")
	activePath := dbus.ObjectPath("/active/1")
	addCalls := 0
	bus := fakeBus{call: func(_ dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			return successfulCall(devicePath)
		case settingsInterface + ".ListConnections":
			return successfulCall([]dbus.ObjectPath{})
		case settingsInterface + ".AddConnection2":
			addCalls++
			settings := args[0].(connectionSettings)
			if actual := variantString(settings["connection"]["id"]); actual != managedConnectionName("MyRouter") {
				t.Fatalf("profile id = %q", actual)
			}
			return successfulCall(profilePath, map[string]dbus.Variant{})
		case managerInterface + ".ActivateConnection":
			return successfulCall(activePath)
		default:
			return failedCall(errors.New("unexpected D-Bus method: " + method))
		}
	}}
	client := &Client{bus: bus}

	if _, err := client.Connect(context.Background(), "wlan0", "MyRouter", "password", false, "disabled"); err != nil {
		t.Fatal(err)
	}
	if addCalls != 1 {
		t.Fatalf("AddConnection2 calls = %d, want 1", addCalls)
	}
}

func TestConnectDeletesDuplicateSameSSIDProfiles(t *testing.T) {
	canonicalPath := dbus.ObjectPath("/profiles/canonical")
	duplicatePath := dbus.ObjectPath("/profiles/duplicate")
	otherPath := dbus.ObjectPath("/profiles/other")
	devicePath := dbus.ObjectPath("/devices/wlan0")
	activePath := dbus.ObjectPath("/active/1")

	canonical, err := buildConnectionSettings(
		"wlan0",
		managedConnectionName("MyRouter"),
		"MyRouter",
		"secret",
		false,
		"disabled",
	)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := buildConnectionSettings("wlan0", "MyRouter", "MyRouter", "secret", false, "disabled")
	if err != nil {
		t.Fatal(err)
	}
	other, err := buildConnectionSettings("wlan0", "Office", "Office", "secret", false, "disabled")
	if err != nil {
		t.Fatal(err)
	}

	deleted := []dbus.ObjectPath{}
	bus := fakeBus{call: func(path dbus.ObjectPath, method string, args ...any) *dbus.Call {
		switch method {
		case managerInterface + ".GetDeviceByIpIface":
			return successfulCall(devicePath)
		case settingsInterface + ".ListConnections":
			return successfulCall([]dbus.ObjectPath{canonicalPath, duplicatePath, otherPath})
		case connectionInterface + ".GetSettings":
			switch path {
			case canonicalPath:
				return successfulCall(canonical)
			case duplicatePath:
				return successfulCall(duplicate)
			case otherPath:
				return successfulCall(other)
			default:
				return failedCall(errors.New("unexpected profile path"))
			}
		case connectionInterface + ".Update2":
			return successfulCall(map[string]dbus.Variant{})
		case connectionInterface + ".Delete":
			deleted = append(deleted, path)
			return successfulCall()
		case managerInterface + ".ActivateConnection":
			return successfulCall(activePath)
		default:
			return failedCall(errors.New("unexpected D-Bus method: " + method))
		}
	}}
	client := &Client{bus: bus}

	if _, err := client.Connect(context.Background(), "wlan0", "MyRouter", "", false, "disabled"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != duplicatePath {
		t.Fatalf("deleted profiles = %#v, want only %s", deleted, duplicatePath)
	}
}

func TestRandomUUIDFormat(t *testing.T) {
	uuid, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(uuid) {
		t.Fatalf("invalid UUID %q", uuid)
	}
}

type fakeBus struct {
	call func(dbus.ObjectPath, string, ...any) *dbus.Call
}

func (bus fakeBus) Object(destination string, path dbus.ObjectPath) dbus.BusObject {
	return fakeBusObject{destination: destination, path: path, call: bus.call}
}

type fakeBusObject struct {
	destination string
	path        dbus.ObjectPath
	call        func(dbus.ObjectPath, string, ...any) *dbus.Call
}

func (object fakeBusObject) Call(method string, _ dbus.Flags, args ...any) *dbus.Call {
	return object.call(object.path, method, args...)
}

func (object fakeBusObject) CallWithContext(
	_ context.Context,
	method string,
	_ dbus.Flags,
	args ...any,
) *dbus.Call {
	return object.call(object.path, method, args...)
}

func (object fakeBusObject) Go(method string, flags dbus.Flags, channel chan *dbus.Call, args ...any) *dbus.Call {
	return object.GoWithContext(context.Background(), method, flags, channel, args...)
}

func (object fakeBusObject) GoWithContext(
	ctx context.Context,
	method string,
	flags dbus.Flags,
	channel chan *dbus.Call,
	args ...any,
) *dbus.Call {
	call := object.CallWithContext(ctx, method, flags, args...)
	if channel != nil {
		channel <- call
	}
	return call
}

func (object fakeBusObject) AddMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call {
	return failedCall(errors.New("not implemented"))
}

func (object fakeBusObject) RemoveMatchSignal(string, string, ...dbus.MatchOption) *dbus.Call {
	return failedCall(errors.New("not implemented"))
}

func (object fakeBusObject) GetProperty(string) (dbus.Variant, error) {
	return dbus.Variant{}, errors.New("not implemented")
}

func (object fakeBusObject) StoreProperty(string, any) error { return errors.New("not implemented") }
func (object fakeBusObject) SetProperty(string, any) error   { return errors.New("not implemented") }
func (object fakeBusObject) Destination() string             { return object.destination }
func (object fakeBusObject) Path() dbus.ObjectPath           { return object.path }

func successfulCall(body ...any) *dbus.Call {
	return &dbus.Call{Body: body}
}

func failedCall(err error) *dbus.Call {
	return &dbus.Call{Err: err}
}
