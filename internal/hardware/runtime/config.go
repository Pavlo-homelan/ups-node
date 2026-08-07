package runtime

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains only the privileged hardware settings. Web, authentication,
// and Wi-Fi configuration stay outside the hardware controller.
type Config struct {
	Backend                 string
	ControlInterval         time.Duration
	GPIOChip                string
	Relay1Pin               int
	Relay2Pin               int
	ACDetectPin             int
	I2CBus                  int
	I2CAddress              uint8
	BatteryChannel          int
	OutputChannel           int
	AveragingSamples        int
	BatteryShuntOhms        float64
	OutputShuntOhms         float64
	CurrentPolarity         float64
	OutputCurrentPolarity   float64
	BatteryEmptyVoltage     float64
	BatteryFullVoltage      float64
	CurrentCutoffMA         float64
	CurrentNoiseFloorMA     float64
	LowVoltageShutdownAfter time.Duration
	SwitchDelay             time.Duration
	DisplayEnabled          bool
	SPIDevice               string
	DisplayDCPin            int
	DisplayResetPin         int
	DisplayBacklightPin     int
	DisplaySpeedHz          int
}

func DefaultConfig() Config {
	return Config{
		Backend:                 "mock",
		ControlInterval:         time.Second,
		GPIOChip:                "/dev/gpiochip0",
		Relay1Pin:               27,
		Relay2Pin:               22,
		ACDetectPin:             17,
		I2CBus:                  1,
		I2CAddress:              0x40,
		BatteryChannel:          1,
		OutputChannel:           2,
		AveragingSamples:        16,
		BatteryShuntOhms:        0.00375,
		OutputShuntOhms:         0.00375,
		CurrentPolarity:         1,
		OutputCurrentPolarity:   1,
		BatteryEmptyVoltage:     9.3,
		BatteryFullVoltage:      12.6,
		CurrentCutoffMA:         150,
		CurrentNoiseFloorMA:     30,
		LowVoltageShutdownAfter: 20 * time.Second,
		SwitchDelay:             50 * time.Millisecond,
		DisplayEnabled:          true,
		SPIDevice:               "/dev/spidev0.0",
		DisplayDCPin:            24,
		DisplayResetPin:         25,
		DisplayBacklightPin:     18,
		DisplaySpeedHz:          0,
	}
}

// LoadConfig reads the subset of the existing Python INI file used by the Go
// hardware owner. Unknown sections and keys are deliberately ignored so a
// phased package upgrade can use one configuration file.
func LoadConfig(path string) (Config, error) {
	config := DefaultConfig()
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open hardware config %s: %w", path, err)
	}
	defer file.Close()

	values := make(map[string]string)
	section := ""
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return Config{}, fmt.Errorf("parse hardware config %s:%d", path, lineNumber)
		}
		values[section+"."+strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read hardware config %s: %w", path, err)
	}

	config.Backend = strings.ToLower(textValue(values, "ups.backend", config.Backend))
	config.GPIOChip = textValue(values, "gpio.gpiochip", config.GPIOChip)
	config.Relay1Pin, err = intValue(values, "gpio.relay_1_pin", config.Relay1Pin)
	if err != nil {
		return Config{}, err
	}
	config.Relay2Pin, err = intValue(values, "gpio.relay_2_pin", config.Relay2Pin)
	if err != nil {
		return Config{}, err
	}
	config.ACDetectPin, err = intValue(values, "gpio.ac_detect_pin", config.ACDetectPin)
	if err != nil {
		return Config{}, err
	}
	config.I2CBus, err = intValue(values, "ups.i2c_bus", config.I2CBus)
	if err != nil {
		return Config{}, err
	}
	address, err := integerValue(values, "ups.i2c_address", uint64(config.I2CAddress), 7)
	if err != nil {
		return Config{}, err
	}
	config.I2CAddress = uint8(address)
	config.BatteryChannel, err = intValue(values, "ups.ina3221_channel", config.BatteryChannel)
	if err != nil {
		return Config{}, err
	}
	config.OutputChannel, err = intValue(values, "ups.ina3221_output_channel", config.OutputChannel)
	if err != nil {
		return Config{}, err
	}
	config.AveragingSamples, err = intValue(values, "ups.ina3221_averaging", config.AveragingSamples)
	if err != nil {
		return Config{}, err
	}

	for key, target := range map[string]*float64{
		"ups.shunt_ohms":                   &config.BatteryShuntOhms,
		"ups.output_shunt_ohms":            &config.OutputShuntOhms,
		"ups.current_polarity":             &config.CurrentPolarity,
		"ups.output_current_polarity":      &config.OutputCurrentPolarity,
		"ups.battery_empty_voltage":        &config.BatteryEmptyVoltage,
		"ups.battery_full_voltage":         &config.BatteryFullVoltage,
		"ups.current_cutoff_ma":            &config.CurrentCutoffMA,
		"ups.current_noise_floor_ma":       &config.CurrentNoiseFloorMA,
		"ups.control_interval":             nil,
		"ups.low_voltage_shutdown_seconds": nil,
		"ups.switch_delay_seconds":         nil,
	} {
		if target == nil {
			continue
		}
		*target, err = floatValue(values, key, *target)
		if err != nil {
			return Config{}, err
		}
	}
	interval, err := durationSeconds(values, "ups.control_interval", config.ControlInterval)
	if err != nil {
		return Config{}, err
	}
	config.ControlInterval = interval
	config.LowVoltageShutdownAfter, err = durationSeconds(
		values, "ups.low_voltage_shutdown_seconds", config.LowVoltageShutdownAfter,
	)
	if err != nil {
		return Config{}, err
	}
	config.SwitchDelay, err = durationSeconds(values, "ups.switch_delay_seconds", config.SwitchDelay)
	if err != nil {
		return Config{}, err
	}

	config.DisplayEnabled, err = boolValue(values, "display.enabled", config.DisplayEnabled)
	if err != nil {
		return Config{}, err
	}
	spiPort, err := intValue(values, "display.spi_port", 0)
	if err != nil {
		return Config{}, err
	}
	spiDevice, err := intValue(values, "display.spi_device", 0)
	if err != nil {
		return Config{}, err
	}
	config.SPIDevice = fmt.Sprintf("/dev/spidev%d.%d", spiPort, spiDevice)
	config.DisplayDCPin, err = intValue(values, "display.dc_pin", config.DisplayDCPin)
	if err != nil {
		return Config{}, err
	}
	config.DisplayResetPin, err = intValue(values, "display.rst_pin", config.DisplayResetPin)
	if err != nil {
		return Config{}, err
	}
	config.DisplayBacklightPin, err = optionalIntValue(
		values, "display.backlight_pin", config.DisplayBacklightPin,
	)
	if err != nil {
		return Config{}, err
	}
	config.DisplaySpeedHz, err = intValue(values, "display.bus_speed_hz", config.DisplaySpeedHz)
	if err != nil {
		return Config{}, err
	}
	return config, config.Validate()
}

func (config Config) Validate() error {
	if config.Backend != "ina3221" && config.Backend != "hardware" {
		return fmt.Errorf("Go hardware owner requires ups.backend=ina3221 or hardware")
	}
	if config.ControlInterval < 200*time.Millisecond {
		return fmt.Errorf("ups.control_interval must be at least 0.2 seconds")
	}
	if config.BatteryChannel < 1 || config.BatteryChannel > 3 ||
		config.OutputChannel < 1 || config.OutputChannel > 3 {
		return fmt.Errorf("INA3221 channels must be 1, 2, or 3")
	}
	if config.BatteryShuntOhms <= 0 || config.OutputShuntOhms <= 0 {
		return fmt.Errorf("INA3221 shunt resistances must be greater than zero")
	}
	if config.BatteryFullVoltage <= config.BatteryEmptyVoltage {
		return fmt.Errorf("battery_full_voltage must be greater than battery_empty_voltage")
	}
	if config.CurrentPolarity == 0 || config.OutputCurrentPolarity == 0 {
		return fmt.Errorf("current polarity must not be zero")
	}
	if config.LowVoltageShutdownAfter <= 0 || config.SwitchDelay < 0 {
		return fmt.Errorf("UPS shutdown and switch durations are invalid")
	}
	if config.I2CBus < 0 || config.DisplaySpeedHz < 0 {
		return fmt.Errorf("hardware bus values are invalid")
	}
	return nil
}

func textValue(values map[string]string, key string, fallback string) string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return value
	}
	return fallback
}

func intValue(values map[string]string, key string, fallback int) (int, error) {
	value, ok := values[key]
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 0, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return int(parsed), nil
}

func optionalIntValue(values map[string]string, key string, fallback int) (int, error) {
	value, ok := values[key]
	if !ok {
		return fallback, nil
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || normalized == "none" || normalized == "off" || normalized == "false" || normalized == "no" || normalized == "-1" {
		return -1, nil
	}
	return intValue(values, key, fallback)
}

func integerValue(values map[string]string, key string, fallback uint64, bits int) (uint64, error) {
	value, ok := values[key]
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 0, bits)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func floatValue(values map[string]string, key string, fallback float64) (float64, error) {
	value, ok := values[key]
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func durationSeconds(values map[string]string, key string, fallback time.Duration) (time.Duration, error) {
	seconds, err := floatValue(values, key, fallback.Seconds())
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func boolValue(values map[string]string, key string, fallback bool) (bool, error) {
	value, ok := values[key]
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("parse %s: expected true or false", key)
	}
}
