//go:build linux

package runtime

import (
	"context"
	"errors"
	"testing"

	"ups-pi-node/internal/hardware/control"
	"ups-pi-node/internal/hardware/ina3221"
)

type fakeGPIO struct {
	acOK   bool
	relay1 []bool
	relay2 []bool
}

func (gpio *fakeGPIO) ACOK() (bool, error) { return gpio.acOK, nil }
func (gpio *fakeGPIO) SetRelay1(value bool) error {
	gpio.relay1 = append(gpio.relay1, value)
	return nil
}
func (gpio *fakeGPIO) SetRelay2(value bool) error {
	gpio.relay2 = append(gpio.relay2, value)
	return nil
}
func (gpio *fakeGPIO) Close() error { return nil }

type fakeSensor struct {
	measurements map[int]ina3221.Measurement
	err          error
}

func (sensor *fakeSensor) Read(channel int, _ float64) (ina3221.Measurement, error) {
	if sensor.err != nil {
		return ina3221.Measurement{}, sensor.err
	}
	return sensor.measurements[channel], nil
}
func (sensor *fakeSensor) Close() error { return nil }

type fakeDisplay struct {
	snapshots []control.Snapshot
	err       error
}

func (display *fakeDisplay) Render(snapshot control.Snapshot) error {
	display.snapshots = append(display.snapshots, snapshot)
	return display.err
}
func (display *fakeDisplay) Close() error { return nil }

type fakeFactory struct {
	gpio    GPIO
	sensor  Sensor
	display Display
}

func (factory fakeFactory) OpenGPIO(Config) (GPIO, error)     { return factory.gpio, nil }
func (factory fakeFactory) OpenSensor(Config) (Sensor, error) { return factory.sensor, nil }
func (factory fakeFactory) OpenDisplay(Config) (Display, error) {
	return factory.display, nil
}

func hardwareTestConfig() Config {
	config := DefaultConfig()
	config.Backend = "ina3221"
	return config
}

func TestPollPublishesCompatibleStatusAndRenders(t *testing.T) {
	gpio := &fakeGPIO{acOK: true}
	display := &fakeDisplay{}
	sensor := &fakeSensor{measurements: map[int]ina3221.Measurement{
		1: {VoltageV: 12.1, CurrentMA: 149.333},
		2: {VoltageV: 18.96, CurrentMA: 757.333},
	}}
	controller, err := newController(
		hardwareTestConfig(), fakeFactory{gpio: gpio, sensor: sensor, display: display}, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	controller.gpio = gpio
	controller.sensor = sensor
	controller.display = display
	if err := controller.pollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := controller.Status()
	if status.State != "CHARGE" || !status.AC || status.OutputChannel != 2 {
		t.Fatalf("status = %#v", status)
	}
	if status.OutputVoltageV != 18.96 || status.OutputCurrentMA != 757.333 {
		t.Fatalf("output status = %#v", status)
	}
	if len(gpio.relay1) != 1 || !gpio.relay1[0] || len(gpio.relay2) != 1 || gpio.relay2[0] {
		t.Fatalf("relay writes = %#v / %#v", gpio.relay1, gpio.relay2)
	}
	if len(display.snapshots) != 1 || display.snapshots[0].State != "CHARGE" {
		t.Fatalf("display snapshots = %#v", display.snapshots)
	}
}

func TestSensorFailureProducesCompatibleErrorSnapshot(t *testing.T) {
	controller, err := newController(hardwareTestConfig(), fakeFactory{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller.gpio = &fakeGPIO{acOK: true}
	controller.sensor = &fakeSensor{err: errors.New("i2c failed")}
	err = controller.pollOnce(context.Background())
	if err == nil {
		t.Fatal("sensor failure was ignored")
	}
	controller.recordHardwareError(err)
	status := controller.Status()
	if status.State != "ERROR" || status.HardwareError != "i2c failed" || status.BatteryStatus != "Critical" {
		t.Fatalf("error status = %#v", status)
	}
}

func TestDisabledHardwareRouterReturnsErrorInsteadOfPanicking(t *testing.T) {
	_, err := Router(nil).Handle(context.Background(), "ups.status", nil)
	if err == nil || err.Error() != "UPS hardware controller is not enabled" {
		t.Fatalf("disabled router error = %v", err)
	}
}
