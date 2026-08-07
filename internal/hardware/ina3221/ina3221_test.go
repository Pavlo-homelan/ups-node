package ina3221

import (
	"math"
	"testing"
)

type fakeBus struct {
	registers map[byte]uint16
	writes    map[byte]uint16
	closed    bool
}

func (bus *fakeBus) ReadWord(register byte) (uint16, error) {
	return bus.registers[register], nil
}

func (bus *fakeBus) WriteWord(register byte, value uint16) error {
	if bus.writes == nil {
		bus.writes = make(map[byte]uint16)
	}
	bus.writes[register] = value
	return nil
}

func (bus *fakeBus) Close() error {
	bus.closed = true
	return nil
}

func TestChannelOneFL2CFullScale(t *testing.T) {
	bus := &fakeBus{registers: map[byte]uint16{
		0x01: 0x3a98, // 1875 * 40 uV = 75 mV
		0x02: 0x2ee0, // 1500 * 8 mV = 12 V
	}}
	sensor, err := New(bus, []int{1}, 16)
	if err != nil {
		t.Fatal(err)
	}

	measurement, err := sensor.Read(1, 0.00375)
	if err != nil {
		t.Fatal(err)
	}
	if bus.writes[configRegister] != 0x4527 {
		t.Fatalf("configuration = 0x%04x, want 0x4527", bus.writes[configRegister])
	}
	closeEnough(t, measurement.VoltageV, 12.0)
	closeEnough(t, measurement.CurrentMA, 20000.0)
}

func TestNegativeShuntDirection(t *testing.T) {
	bus := &fakeBus{registers: map[byte]uint16{
		0x05: 0xe890, // -750 * 40 uV = -30 mV
		0x06: 0x2580, // 1200 * 8 mV = 9.6 V
	}}
	sensor, err := New(bus, []int{3}, 16)
	if err != nil {
		t.Fatal(err)
	}

	measurement, err := sensor.Read(3, 0.00375)
	if err != nil {
		t.Fatal(err)
	}
	if bus.writes[configRegister] != 0x1527 {
		t.Fatalf("configuration = 0x%04x, want 0x1527", bus.writes[configRegister])
	}
	closeEnough(t, measurement.VoltageV, 9.6)
	closeEnough(t, measurement.CurrentMA, -8000.0)
}

func TestBatteryAndOutputChannelsUseIndependentShunts(t *testing.T) {
	bus := &fakeBus{registers: map[byte]uint16{
		0x01: 0x02f0,
		0x02: 0x2ee0,
		0x03: 0x1770,
		0x04: 0x4a38,
	}}
	sensor, err := New(bus, []int{1, 2}, 16)
	if err != nil {
		t.Fatal(err)
	}

	battery, err := sensor.Read(1, 0.00375)
	if err != nil {
		t.Fatal(err)
	}
	output, err := sensor.Read(2, 0.00375)
	if err != nil {
		t.Fatal(err)
	}
	if bus.writes[configRegister] != 0x6527 {
		t.Fatalf("configuration = 0x%04x, want 0x6527", bus.writes[configRegister])
	}
	closeEnough(t, battery.VoltageV, 12.0)
	closeEnough(t, battery.CurrentMA, 1002.6666667)
	closeEnough(t, output.VoltageV, 19.0)
	closeEnough(t, output.CurrentMA, 8000.0)
}

func TestValidationAndClose(t *testing.T) {
	bus := &fakeBus{}
	if _, err := New(bus, []int{0}, 16); err == nil {
		t.Fatal("invalid channel accepted")
	}
	if _, err := New(bus, []int{1}, 2); err == nil {
		t.Fatal("invalid averaging accepted")
	}
	sensor, err := New(bus, []int{1}, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sensor.Read(1, 0); err == nil {
		t.Fatal("invalid shunt accepted")
	}
	if err := sensor.Close(); err != nil || !bus.closed {
		t.Fatalf("Close() error = %v, closed = %v", err, bus.closed)
	}
}

func TestAttachDoesNotReconfigureSensor(t *testing.T) {
	bus := &fakeBus{}
	sensor, err := Attach(bus, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(bus.writes) != 0 {
		t.Fatalf("Attach() wrote sensor registers: %#v", bus.writes)
	}
	if actual := sensor.EnabledChannels(); len(actual) != 2 || actual[0] != 1 || actual[1] != 2 {
		t.Fatalf("enabled channels = %v", actual)
	}
}

func closeEnough(t *testing.T, actual float64, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-6 {
		t.Fatalf("value = %.9f, want %.9f", actual, expected)
	}
}
