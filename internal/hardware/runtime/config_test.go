package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigUsesExistingPythonSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.conf")
	contents := `[unrelated]
value = preserved

[gpio]
relay_1_pin = 5
relay_2_pin = 6
ac_detect_pin = 13
gpiochip = /dev/gpiochip4

[ups]
backend = ina3221
control_interval = 0.25
i2c_bus = 3
i2c_address = 0x41
ina3221_channel = 2
ina3221_output_channel = 3
ina3221_averaging = 64
shunt_ohms = 0.01
output_shunt_ohms = 0.02
current_polarity = -1
output_current_polarity = -1
battery_empty_voltage = 10
battery_full_voltage = 13
current_cutoff_ma = 100
current_noise_floor_ma = 5
low_voltage_shutdown_seconds = 7.5
switch_delay_seconds = 0.08

[display]
enabled = false
spi_port = 2
spi_device = 1
dc_pin = 19
rst_pin = 20
backlight_pin = off
bus_speed_hz = 4000000
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.ControlInterval != 250*time.Millisecond || config.SwitchDelay != 80*time.Millisecond {
		t.Fatalf("durations = %v / %v", config.ControlInterval, config.SwitchDelay)
	}
	if config.I2CAddress != 0x41 || config.BatteryChannel != 2 || config.OutputChannel != 3 {
		t.Fatalf("INA config = %#v", config)
	}
	if config.CurrentPolarity != -1 || config.OutputShuntOhms != 0.02 {
		t.Fatalf("current config = %#v", config)
	}
	if config.DisplayEnabled || config.SPIDevice != "/dev/spidev2.1" || config.DisplayBacklightPin != -1 {
		t.Fatalf("display config = %#v", config)
	}
}

func TestConfigRejectsUnsafeOrNonsensicalHardwareValues(t *testing.T) {
	config := DefaultConfig()
	config.Backend = "ina3221"
	config.ControlInterval = 100 * time.Millisecond
	if err := config.Validate(); err == nil {
		t.Fatal("short control interval was accepted")
	}
	config.ControlInterval = time.Second
	config.BatteryShuntOhms = 0
	if err := config.Validate(); err == nil {
		t.Fatal("zero shunt resistance was accepted")
	}
}
