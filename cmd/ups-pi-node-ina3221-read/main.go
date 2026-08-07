package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"ups-pi-node/internal/hardware/ina3221"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var busNumber int
	var addressText string
	var batteryChannel int
	var outputChannel int
	var averaging int
	var batteryShunt float64
	var outputShunt float64
	var settle time.Duration
	var configure bool
	flag.IntVar(&busNumber, "bus", 1, "I2C bus number")
	flag.StringVar(&addressText, "address", "0x40", "INA3221 I2C address")
	flag.IntVar(&batteryChannel, "battery-channel", 1, "battery channel")
	flag.IntVar(&outputChannel, "output-channel", 2, "output channel")
	flag.IntVar(&averaging, "averaging", 16, "ADC averaging samples")
	flag.Float64Var(&batteryShunt, "battery-shunt", 0.00375, "battery shunt resistance in ohms")
	flag.Float64Var(&outputShunt, "output-shunt", 0.00375, "output shunt resistance in ohms")
	flag.DurationVar(&settle, "settle", 150*time.Millisecond, "settling time after sensor configuration")
	flag.BoolVar(&configure, "configure", false, "configure the sensor before reading (requires a single owner)")
	flag.Parse()

	address, err := strconv.ParseUint(addressText, 0, 7)
	if err != nil {
		return fmt.Errorf("parse INA3221 address: %w", err)
	}
	bus, err := ina3221.OpenLinuxBus(busNumber, uint8(address))
	if err != nil {
		return err
	}
	sensor, err := ina3221.Attach(bus, []int{batteryChannel, outputChannel})
	if err != nil {
		bus.Close()
		return err
	}
	defer sensor.Close()
	if configure {
		if err := sensor.Configure(averaging); err != nil {
			return err
		}
	}
	if configure && settle > 0 {
		time.Sleep(settle)
	}

	battery, err := sensor.Read(batteryChannel, batteryShunt)
	if err != nil {
		return err
	}
	output, err := sensor.Read(outputChannel, outputShunt)
	if err != nil {
		return err
	}
	result := struct {
		Battery ina3221.Measurement `json:"battery"`
		Output  ina3221.Measurement `json:"output"`
	}{Battery: battery, Output: output}
	return json.NewEncoder(os.Stdout).Encode(result)
}
