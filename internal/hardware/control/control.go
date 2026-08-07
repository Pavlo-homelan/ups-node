// Package control contains the UPS relay and shutdown policy independently
// from GPIO, I2C, display, and process execution.
package control

import (
	"math"
	"strconv"
	"time"

	"ups-pi-node/internal/hardware/ina3221"
)

type Config struct {
	BatteryEmptyVoltage     float64
	BatteryFullVoltage      float64
	CurrentCutoffMA         float64
	CurrentNoiseFloorMA     float64
	CurrentPolarity         float64
	OutputCurrentPolarity   float64
	LowVoltageShutdownAfter time.Duration
	SwitchDelay             time.Duration
}

type Input struct {
	ACOK          bool
	Battery       ina3221.Measurement
	Output        ina3221.Measurement
	Elapsed       time.Duration
	ACSensorPin   int
	OutputChannel int
}

type Relay struct {
	Channel  int    `json:"channel"`
	Name     string `json:"name"`
	Position string `json:"position"`
	Role     string `json:"role"`
	Detail   string `json:"detail"`
}

type Snapshot struct {
	Backend          string  `json:"backend"`
	VoltageV         float64 `json:"v"`
	CurrentAbsMA     float64 `json:"i"`
	CurrentMA        float64 `json:"current_ma"`
	PowerW           float64 `json:"power_w"`
	OutputChannel    int     `json:"output_channel"`
	OutputVoltageV   float64 `json:"output_voltage_v"`
	OutputCurrentMA  float64 `json:"output_current_ma"`
	OutputPowerW     float64 `json:"output_power_w"`
	State            string  `json:"state"`
	Color            string  `json:"color"`
	AC               bool    `json:"ac"`
	Percent          int     `json:"percent"`
	BatteryStatus    string  `json:"battery_status"`
	BatteryDirection string  `json:"battery_direction"`
	LoadSource       string  `json:"load_source"`
	BatteryRoute     string  `json:"battery_route"`
	ACSensorPin      string  `json:"ac_sensor_pin"`
	Relays           []Relay `json:"relays"`
}

type Decision struct {
	Relay1High      bool
	Relay2High      bool
	Relay2First     bool
	SwitchDelay     time.Duration
	RequestShutdown bool
	Snapshot        Snapshot
}

type Controller struct {
	config             Config
	chargeFinished     bool
	lowVoltageDuration time.Duration
	shutdownRequested  bool
}

func New(config Config) *Controller {
	return &Controller{config: config}
}

func (controller *Controller) Step(input Input) Decision {
	state := "CHARGE"
	color := "yellow"
	relay1High := true
	relay2High := false
	relay2First := false

	if input.ACOK {
		if input.Battery.VoltageV >= controller.config.BatteryFullVoltage-0.05 &&
			math.Abs(input.Battery.CurrentMA) < controller.config.CurrentCutoffMA {
			controller.chargeFinished = true
		}
		if controller.chargeFinished {
			relay2High = true
			state = "CHARGED"
			color = "green"
		}
		if input.Battery.VoltageV < controller.config.BatteryFullVoltage-0.4 {
			controller.chargeFinished = false
		}
	} else {
		relay1High = false
		relay2High = true
		relay2First = true
		state = "DISCHARGE"
		color = "red"
		controller.chargeFinished = false
	}

	requestShutdown := false
	if input.Battery.VoltageV < controller.config.BatteryEmptyVoltage && !input.ACOK {
		elapsed := input.Elapsed
		if elapsed <= 0 {
			elapsed = time.Second
		}
		controller.lowVoltageDuration += elapsed
		if controller.lowVoltageDuration >= controller.config.LowVoltageShutdownAfter &&
			!controller.shutdownRequested {
			controller.shutdownRequested = true
			requestShutdown = true
		}
	} else {
		controller.lowVoltageDuration = 0
	}

	current := applyCurrentPolicy(
		input.Battery.CurrentMA,
		controller.config.CurrentPolarity,
		controller.config.CurrentNoiseFloorMA,
	)
	outputCurrent := applyCurrentPolicy(
		input.Output.CurrentMA,
		controller.config.OutputCurrentPolarity,
		controller.config.CurrentNoiseFloorMA,
	)
	percent := batteryPercent(
		input.Battery.VoltageV,
		controller.config.BatteryEmptyVoltage,
		controller.config.BatteryFullVoltage,
	)
	loadSource := "UPS output to load"
	batteryRoute := "Battery routed to charger"
	if !input.ACOK {
		loadSource = "Battery to load"
		batteryRoute = "Battery routed to load"
	}

	return Decision{
		Relay1High:      relay1High,
		Relay2High:      relay2High,
		Relay2First:     relay2First,
		SwitchDelay:     controller.config.SwitchDelay,
		RequestShutdown: requestShutdown,
		Snapshot: Snapshot{
			Backend:          "ina3221",
			VoltageV:         input.Battery.VoltageV,
			CurrentAbsMA:     math.Abs(current),
			CurrentMA:        current,
			PowerW:           input.Battery.VoltageV * current / 1000,
			OutputChannel:    input.OutputChannel,
			OutputVoltageV:   input.Output.VoltageV,
			OutputCurrentMA:  outputCurrent,
			OutputPowerW:     input.Output.VoltageV * outputCurrent / 1000,
			State:            state,
			Color:            color,
			AC:               input.ACOK,
			Percent:          percent,
			BatteryStatus:    batteryStatus(percent),
			BatteryDirection: batteryDirection(state),
			LoadSource:       loadSource,
			BatteryRoute:     batteryRoute,
			ACSensorPin:      intString(input.ACSensorPin),
			Relays:           relayStates(input.ACOK, state),
		},
	}
}

func applyCurrentPolicy(raw float64, polarity float64, noiseFloor float64) float64 {
	measured := raw * polarity
	if math.Abs(measured) <= noiseFloor {
		return 0
	}
	return measured
}

func batteryPercent(voltage float64, empty float64, full float64) int {
	span := full - empty
	if span <= 0 {
		return 0
	}
	percent := int((voltage - empty) / span * 100)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func batteryStatus(percent int) string {
	switch {
	case percent >= 95:
		return "Full"
	case percent >= 70:
		return "High"
	case percent >= 40:
		return "Medium"
	case percent >= 15:
		return "Low"
	default:
		return "Critical"
	}
}

func batteryDirection(state string) string {
	switch state {
	case "DISCHARGE":
		return "Discharging"
	case "CHARGE":
		return "Charging"
	default:
		return "Idle"
	}
}

func relayStates(acOK bool, state string) []Relay {
	relay1Position := "BATTERY"
	if acOK {
		relay1Position = "UPS"
	}
	relay2Position := "LOAD"
	if state == "CHARGED" {
		relay2Position = "CHARGED"
	} else if acOK {
		relay2Position = "CHARGE"
	}
	return []Relay{
		{1, "Relay 1", relay1Position, "Selects load source", "HIGH on AC power, LOW on battery backup."},
		{2, "Relay 2", relay2Position, "Battery charge/load route", "LOW while charging, HIGH when charged or discharging."},
	}
}

func intString(value int) string {
	return strconv.Itoa(value)
}
