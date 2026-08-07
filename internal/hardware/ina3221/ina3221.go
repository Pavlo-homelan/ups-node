// Package ina3221 implements register-level voltage and shunt-current reads
// without depending on a Python SMBus runtime.
package ina3221

import (
	"fmt"
	"sort"
)

const (
	configRegister       byte    = 0x00
	shuntVoltageLSBVolts float64 = 40e-6
	busVoltageLSBVolts   float64 = 8e-3
)

var averagingBits = map[int]uint16{
	1:    0b000,
	4:    0b001,
	16:   0b010,
	64:   0b011,
	128:  0b100,
	256:  0b101,
	512:  0b110,
	1024: 0b111,
}

type RegisterBus interface {
	ReadWord(register byte) (uint16, error)
	WriteWord(register byte, value uint16) error
	Close() error
}

type Measurement struct {
	VoltageV  float64 `json:"voltage_v"`
	CurrentMA float64 `json:"current_ma"`
}

type Sensor struct {
	bus             RegisterBus
	enabledChannels map[int]struct{}
}

func New(bus RegisterBus, channels []int, averagingSamples int) (*Sensor, error) {
	sensor, err := Attach(bus, channels)
	if err != nil {
		return nil, err
	}
	if err := sensor.Configure(averagingSamples); err != nil {
		return nil, err
	}
	return sensor, nil
}

// Attach creates a reader for a sensor configured by the active controller.
// It is intended for diagnostics during a phased migration; production has one
// owner which calls Configure once during startup.
func Attach(bus RegisterBus, channels []int) (*Sensor, error) {
	if bus == nil {
		return nil, fmt.Errorf("INA3221 register bus is required")
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("at least one INA3221 channel is required")
	}
	enabled := make(map[int]struct{}, len(channels))
	for _, channel := range channels {
		if channel < 1 || channel > 3 {
			return nil, fmt.Errorf("INA3221 channel must be 1, 2, or 3")
		}
		enabled[channel] = struct{}{}
	}

	return &Sensor{bus: bus, enabledChannels: enabled}, nil
}

func (sensor *Sensor) Configure(averagingSamples int) error {
	averaging, ok := averagingBits[averagingSamples]
	if !ok {
		return fmt.Errorf("unsupported INA3221 averaging sample count %d", averagingSamples)
	}
	channelEnable := uint16(0)
	for channel := range sensor.enabledChannels {
		channelEnable |= 1 << (15 - channel)
	}
	// Match the verified Python configuration: selected channels, 1.1 ms
	// conversion times, continuous shunt+bus measurements.
	configuration := channelEnable | averaging<<9 | 0x0127
	if err := sensor.bus.WriteWord(configRegister, configuration); err != nil {
		return fmt.Errorf("configure INA3221: %w", err)
	}
	return nil
}

func (sensor *Sensor) Read(channel int, shuntOhms float64) (Measurement, error) {
	if _, ok := sensor.enabledChannels[channel]; !ok {
		return Measurement{}, fmt.Errorf("INA3221 channel %d is not enabled", channel)
	}
	if shuntOhms <= 0 {
		return Measurement{}, fmt.Errorf("INA3221 shunt resistance must be greater than zero")
	}

	shuntRegister, busRegister, err := registersForChannel(channel)
	if err != nil {
		return Measurement{}, err
	}
	shuntRaw, err := sensor.bus.ReadWord(shuntRegister)
	if err != nil {
		return Measurement{}, fmt.Errorf("read INA3221 channel %d shunt voltage: %w", channel, err)
	}
	busRaw, err := sensor.bus.ReadWord(busRegister)
	if err != nil {
		return Measurement{}, fmt.Errorf("read INA3221 channel %d bus voltage: %w", channel, err)
	}

	shuntSteps := int32(int16(shuntRaw)) >> 3
	busSteps := uint32(busRaw) >> 3
	shuntVoltage := float64(shuntSteps) * shuntVoltageLSBVolts
	return Measurement{
		VoltageV:  float64(busSteps) * busVoltageLSBVolts,
		CurrentMA: (shuntVoltage / shuntOhms) * 1000,
	}, nil
}

func (sensor *Sensor) EnabledChannels() []int {
	channels := make([]int, 0, len(sensor.enabledChannels))
	for channel := range sensor.enabledChannels {
		channels = append(channels, channel)
	}
	sort.Ints(channels)
	return channels
}

func (sensor *Sensor) Close() error {
	if sensor == nil || sensor.bus == nil {
		return nil
	}
	return sensor.bus.Close()
}

func registersForChannel(channel int) (byte, byte, error) {
	if channel < 1 || channel > 3 {
		return 0, 0, fmt.Errorf("INA3221 channel must be 1, 2, or 3")
	}
	shunt := byte(0x01 + (channel-1)*2)
	return shunt, shunt + 1, nil
}
