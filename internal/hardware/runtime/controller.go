//go:build linux

// Package runtime owns the privileged UPS hardware control loop. It is kept
// behind an explicit helper flag during the shadow migration so Python and Go
// never request the same GPIO or SPI lines concurrently.
package runtime

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"ups-pi-node/internal/hardware/control"
	"ups-pi-node/internal/hardware/display"
	"ups-pi-node/internal/hardware/gpio"
	"ups-pi-node/internal/hardware/ina3221"
)

const hardwareRetryDelay = 5 * time.Second

type GPIO interface {
	control.RelayOutput
	ACOK() (bool, error)
	Close() error
}

type Sensor interface {
	Read(channel int, shuntOhms float64) (ina3221.Measurement, error)
	Close() error
}

type Display interface {
	Render(control.Snapshot) error
	Close() error
}

type Factory interface {
	OpenGPIO(Config) (GPIO, error)
	OpenSensor(Config) (Sensor, error)
	OpenDisplay(Config) (Display, error)
}

type Status struct {
	control.Snapshot
	HardwareError string `json:"hardware_error,omitempty"`
	DisplayError  string `json:"display_error,omitempty"`
}

type ShutdownFunc func(context.Context) error

type Controller struct {
	config   Config
	factory  Factory
	shutdown ShutdownFunc
	logger   *slog.Logger

	statusMu sync.RWMutex
	status   Status

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}

	gpio    GPIO
	sensor  Sensor
	display Display
	policy  *control.Controller
}

func NewController(config Config, logger *slog.Logger) (*Controller, error) {
	return newController(config, linuxFactory{}, requestSystemShutdown, logger)
}

func newController(
	config Config,
	factory Factory,
	shutdown ShutdownFunc,
	logger *slog.Logger,
) (*Controller, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("hardware factory is required")
	}
	if shutdown == nil {
		shutdown = func(context.Context) error { return nil }
	}
	if logger == nil {
		logger = slog.Default()
	}
	controller := &Controller{
		config: config, factory: factory, shutdown: shutdown, logger: logger,
		policy: control.New(control.Config{
			BatteryEmptyVoltage:     config.BatteryEmptyVoltage,
			BatteryFullVoltage:      config.BatteryFullVoltage,
			CurrentCutoffMA:         config.CurrentCutoffMA,
			CurrentNoiseFloorMA:     config.CurrentNoiseFloorMA,
			CurrentPolarity:         config.CurrentPolarity,
			OutputCurrentPolarity:   config.OutputCurrentPolarity,
			LowVoltageShutdownAfter: config.LowVoltageShutdownAfter,
			SwitchDelay:             config.SwitchDelay,
		}),
	}
	controller.status = controller.fallbackStatus("INIT", "white", "")
	return controller, nil
}

func (controller *Controller) Start(parent context.Context) error {
	controller.lifecycleMu.Lock()
	defer controller.lifecycleMu.Unlock()
	if controller.cancel != nil {
		return fmt.Errorf("hardware controller is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	controller.cancel = cancel
	controller.done = make(chan struct{})
	go controller.run(ctx)
	return nil
}

func (controller *Controller) Close() error {
	controller.lifecycleMu.Lock()
	cancel := controller.cancel
	done := controller.done
	controller.lifecycleMu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (controller *Controller) Status() Status {
	controller.statusMu.RLock()
	defer controller.statusMu.RUnlock()
	status := controller.status
	status.Relays = append([]control.Relay(nil), status.Relays...)
	return status
}

func (controller *Controller) run(ctx context.Context) {
	defer close(controller.done)
	defer controller.closeAll()

	nextDisplayAttempt := time.Time{}
	for {
		if err := controller.ensureCore(); err != nil {
			controller.recordHardwareError(err)
			controller.closeCore()
			if !wait(ctx, hardwareRetryDelay) {
				return
			}
			continue
		}

		if controller.config.DisplayEnabled && controller.display == nil && time.Now().After(nextDisplayAttempt) {
			opened, err := controller.factory.OpenDisplay(controller.config)
			if err != nil {
				controller.recordDisplayError(err)
				nextDisplayAttempt = time.Now().Add(hardwareRetryDelay)
			} else {
				controller.display = opened
				controller.recordDisplayError(nil)
			}
		}

		if err := controller.pollOnce(ctx); err != nil {
			controller.recordHardwareError(err)
			controller.closeCore()
			if !wait(ctx, hardwareRetryDelay) {
				return
			}
			continue
		}
		if !wait(ctx, controller.config.ControlInterval) {
			return
		}
	}
}

func (controller *Controller) ensureCore() error {
	if controller.gpio == nil {
		opened, err := controller.factory.OpenGPIO(controller.config)
		if err != nil {
			return err
		}
		controller.gpio = opened
	}
	if controller.sensor == nil {
		opened, err := controller.factory.OpenSensor(controller.config)
		if err != nil {
			return err
		}
		controller.sensor = opened
	}
	return nil
}

func (controller *Controller) pollOnce(ctx context.Context) error {
	if controller.gpio == nil || controller.sensor == nil {
		return fmt.Errorf("UPS core devices are not open")
	}
	acOK, err := controller.gpio.ACOK()
	if err != nil {
		return err
	}
	battery, err := controller.sensor.Read(controller.config.BatteryChannel, controller.config.BatteryShuntOhms)
	if err != nil {
		return err
	}
	output, err := controller.sensor.Read(controller.config.OutputChannel, controller.config.OutputShuntOhms)
	if err != nil {
		return err
	}
	elapsed := controller.config.ControlInterval
	if elapsed < time.Second {
		elapsed = time.Second
	}
	decision := controller.policy.Step(control.Input{
		ACOK: acOK, Battery: battery, Output: output, Elapsed: elapsed,
		ACSensorPin: controller.config.ACDetectPin, OutputChannel: controller.config.OutputChannel,
	})
	if err := control.ApplyRelays(ctx, controller.gpio, decision, control.Wait); err != nil {
		return err
	}
	if decision.RequestShutdown {
		if err := controller.shutdown(ctx); err != nil {
			controller.logger.Error("request low-voltage shutdown", "error", err)
		}
	}

	status := Status{Snapshot: decision.Snapshot}
	if controller.display != nil {
		if err := controller.display.Render(decision.Snapshot); err != nil {
			status.DisplayError = err.Error()
			controller.display.Close()
			controller.display = nil
		}
	}
	controller.statusMu.Lock()
	if status.DisplayError == "" {
		status.DisplayError = controller.status.DisplayError
	}
	status.HardwareError = ""
	controller.status = status
	controller.statusMu.Unlock()
	return nil
}

func (controller *Controller) recordHardwareError(err error) {
	controller.logger.Error("UPS hardware cycle", "error", err)
	status := controller.fallbackStatus("ERROR", "red", err.Error())
	controller.statusMu.Lock()
	status.DisplayError = controller.status.DisplayError
	controller.status = status
	controller.statusMu.Unlock()
	if controller.display != nil {
		if displayErr := controller.display.Render(status.Snapshot); displayErr != nil {
			controller.recordDisplayError(displayErr)
			controller.display.Close()
			controller.display = nil
		}
	}
}

func (controller *Controller) recordDisplayError(err error) {
	controller.statusMu.Lock()
	if err == nil {
		controller.status.DisplayError = ""
	} else {
		controller.status.DisplayError = err.Error()
		controller.logger.Warn("UPS display", "error", err)
	}
	controller.statusMu.Unlock()
}

func (controller *Controller) fallbackStatus(state string, color string, hardwareError string) Status {
	return Status{
		Snapshot: control.Snapshot{
			Backend: controller.config.Backend, OutputChannel: controller.config.OutputChannel,
			State: state, Color: color, BatteryStatus: fallbackBatteryStatus(state),
			BatteryDirection: "Idle", LoadSource: "Unknown", BatteryRoute: "Unknown",
			ACSensorPin: fmt.Sprintf("%d", controller.config.ACDetectPin),
			Relays: []control.Relay{
				{Channel: 1, Name: "Relay 1", Position: "BATTERY", Role: "Selects load source", Detail: "HIGH on AC power, LOW on battery backup."},
				{Channel: 2, Name: "Relay 2", Position: "LOAD", Role: "Battery charge/load route", Detail: "LOW while charging, HIGH when charged or discharging."},
			},
		},
		HardwareError: hardwareError,
	}
}

func (controller *Controller) closeCore() {
	if controller.sensor != nil {
		controller.sensor.Close()
		controller.sensor = nil
	}
	if controller.gpio != nil {
		controller.gpio.Close()
		controller.gpio = nil
	}
}

func (controller *Controller) closeAll() {
	controller.closeCore()
	if controller.display != nil {
		controller.display.Close()
		controller.display = nil
	}
}

func fallbackBatteryStatus(state string) string {
	if state == "ERROR" {
		return "Critical"
	}
	return "Unknown"
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func requestSystemShutdown(ctx context.Context) error {
	command := exec.CommandContext(ctx, "/usr/sbin/shutdown", "-h", "now")
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

type linuxFactory struct{}

func (linuxFactory) OpenGPIO(config Config) (GPIO, error) {
	return gpio.Open(config.GPIOChip, config.Relay1Pin, config.Relay2Pin, config.ACDetectPin)
}

func (linuxFactory) OpenSensor(config Config) (Sensor, error) {
	bus, err := ina3221.OpenLinuxBus(config.I2CBus, config.I2CAddress)
	if err != nil {
		return nil, err
	}
	sensor, err := ina3221.New(
		bus, []int{config.BatteryChannel, config.OutputChannel}, config.AveragingSamples,
	)
	if err != nil {
		bus.Close()
		return nil, err
	}
	return sensor, nil
}

func (linuxFactory) OpenDisplay(config Config) (Display, error) {
	renderer, err := display.NewRenderer(display.DefaultFontPaths())
	if err != nil {
		return nil, err
	}
	device, err := display.OpenST7735(display.ST7735Config{
		DevicePath: config.SPIDevice, GPIOChip: config.GPIOChip,
		DCPin: config.DisplayDCPin, ResetPin: config.DisplayResetPin,
		BacklightPin: config.DisplayBacklightPin, SpeedHz: config.DisplaySpeedHz,
	})
	if err != nil {
		renderer.Close()
		return nil, err
	}
	return &renderedDisplay{renderer: renderer, device: device}, nil
}

type portraitDevice interface {
	DrawPortrait(image.Image) error
	Close() error
}

type renderedDisplay struct {
	renderer *display.Renderer
	device   portraitDevice
}

func (target *renderedDisplay) Render(snapshot control.Snapshot) error {
	return target.device.DrawPortrait(target.renderer.Render(snapshot))
}

func (target *renderedDisplay) Close() error {
	deviceErr := target.device.Close()
	rendererErr := target.renderer.Close()
	if deviceErr != nil {
		return deviceErr
	}
	return rendererErr
}
