//go:build linux

package display

import (
	"fmt"
	"image"
	"os"
	"time"

	"github.com/warthog618/go-gpiocdev"
	"golang.org/x/sys/unix"
)

const (
	spiIOCWriteMode         = 0x40016b01
	spiIOCWriteBitsPerWord  = 0x40016b03
	spiIOCWriteMaximumSpeed = 0x40046b04
	maximumSPIWrite         = 4096
)

type ST7735Config struct {
	DevicePath       string
	GPIOChip         string
	DCPin            int
	ResetPin         int
	BacklightPin     int
	SpeedHz          int
	HorizontalOffset int
	VerticalOffset   int
}

type ST7735 struct {
	spi       *os.File
	dc        *gpiocdev.Line
	reset     *gpiocdev.Line
	backlight *gpiocdev.Line
	config    ST7735Config
}

func OpenST7735(config ST7735Config) (*ST7735, error) {
	if config.DevicePath == "" {
		config.DevicePath = "/dev/spidev0.0"
	}
	if config.GPIOChip == "" {
		config.GPIOChip = "/dev/gpiochip0"
	}
	if config.SpeedHz <= 0 {
		config.SpeedHz = 8_000_000
	}
	device := &ST7735{config: config}
	var err error
	device.spi, err = os.OpenFile(config.DevicePath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open ST7735 SPI device: %w", err)
	}
	if err := device.configureSPI(); err != nil {
		device.Close()
		return nil, err
	}
	device.dc, err = gpiocdev.RequestLine(
		config.GPIOChip, config.DCPin, gpiocdev.AsOutput(1), gpiocdev.WithConsumer("ups-pi-node-st7735-dc"),
	)
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("request ST7735 DC GPIO%d: %w", config.DCPin, err)
	}
	device.reset, err = gpiocdev.RequestLine(
		config.GPIOChip, config.ResetPin, gpiocdev.AsOutput(1), gpiocdev.WithConsumer("ups-pi-node-st7735-reset"),
	)
	if err != nil {
		device.Close()
		return nil, fmt.Errorf("request ST7735 reset GPIO%d: %w", config.ResetPin, err)
	}
	if config.BacklightPin >= 0 {
		device.backlight, err = gpiocdev.RequestLine(
			config.GPIOChip,
			config.BacklightPin,
			gpiocdev.AsOutput(0),
			gpiocdev.WithConsumer("ups-pi-node-st7735-backlight"),
		)
		if err != nil {
			device.Close()
			return nil, fmt.Errorf("request ST7735 backlight GPIO%d: %w", config.BacklightPin, err)
		}
	}
	if err := device.initialize(); err != nil {
		device.Close()
		return nil, err
	}
	return device, nil
}

func (device *ST7735) configureSPI() error {
	fd := int(device.spi.Fd())
	for _, setting := range []struct {
		request uint
		value   int
		name    string
	}{
		{spiIOCWriteMode, 0, "mode"},
		{spiIOCWriteBitsPerWord, 8, "bits per word"},
		{spiIOCWriteMaximumSpeed, device.config.SpeedHz, "maximum speed"},
	} {
		if err := unix.IoctlSetPointerInt(fd, setting.request, setting.value); err != nil {
			return fmt.Errorf("configure SPI %s: %w", setting.name, err)
		}
	}
	return nil
}

func (device *ST7735) initialize() error {
	if err := device.reset.SetValue(0); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := device.reset.SetValue(1); err != nil {
		return err
	}
	time.Sleep(120 * time.Millisecond)

	sequence := []struct {
		command byte
		data    []byte
		wait    time.Duration
	}{
		{0x01, nil, 150 * time.Millisecond},
		{0x11, nil, 500 * time.Millisecond},
		{0xb1, []byte{0x01, 0x2c, 0x2d}, 0},
		{0xb2, []byte{0x01, 0x2c, 0x2d}, 0},
		{0xb3, []byte{0x01, 0x2c, 0x2d, 0x01, 0x2c, 0x2d}, 0},
		{0xb4, []byte{0x07}, 0},
		{0xc0, []byte{0xa2, 0x02, 0x84}, 0},
		{0xc1, []byte{0xc5}, 0},
		{0xc2, []byte{0x0a, 0x00}, 0},
		{0xc3, []byte{0x8a, 0x2a}, 0},
		{0xc4, []byte{0x8a, 0xee}, 0},
		{0xc5, []byte{0x0e}, 0},
		{0x36, []byte{0x60}, 0},
		{0x20, nil, 0},
		{0x3a, []byte{0x06}, 0},
		{0x13, nil, 0},
		{0xe0, []byte{0x0f, 0x1a, 0x0f, 0x18, 0x2f, 0x28, 0x20, 0x22, 0x1f, 0x1b, 0x23, 0x37, 0x00, 0x07, 0x02, 0x10}, 0},
		{0xe1, []byte{0x0f, 0x1b, 0x0f, 0x17, 0x33, 0x2c, 0x29, 0x2e, 0x30, 0x30, 0x39, 0x3f, 0x00, 0x07, 0x03, 0x10}, 0},
	}
	for _, item := range sequence {
		if err := device.command(item.command, item.data...); err != nil {
			return fmt.Errorf("initialize ST7735 command 0x%02x: %w", item.command, err)
		}
		if item.wait > 0 {
			time.Sleep(item.wait)
		}
	}
	black := image.NewRGBA(image.Rect(0, 0, PortraitWidth, PortraitHeight))
	if err := device.DrawPortrait(black); err != nil {
		return err
	}
	// luma.lcd clears controller RAM before its base device.show() sends
	// DISPLAYON. Keep the panel dark until the first complete frame is loaded.
	if err := device.command(0x29); err != nil {
		return fmt.Errorf("enable ST7735 display: %w", err)
	}
	if device.backlight != nil {
		if err := device.backlight.SetValue(1); err != nil {
			return fmt.Errorf("enable ST7735 backlight: %w", err)
		}
	}
	return nil
}

func (device *ST7735) DrawPortrait(frame image.Image) error {
	if frame.Bounds().Dx() != PortraitWidth || frame.Bounds().Dy() != PortraitHeight {
		return fmt.Errorf("ST7735 portrait frame must be %dx%d", PortraitWidth, PortraitHeight)
	}
	physical := RotatePortraitToPhysical(frame)
	left := device.config.HorizontalOffset
	top := device.config.VerticalOffset
	right := left + physical.Bounds().Dx() - 1
	bottom := top + physical.Bounds().Dy() - 1
	if err := device.command(0x2a, byte(left>>8), byte(left), byte(right>>8), byte(right)); err != nil {
		return err
	}
	if err := device.command(0x2b, byte(top>>8), byte(top), byte(bottom>>8), byte(bottom)); err != nil {
		return err
	}
	if err := device.command(0x2c); err != nil {
		return err
	}
	if err := device.dc.SetValue(1); err != nil {
		return err
	}
	return device.writeChunks(EncodeRGB666(physical))
}

func (device *ST7735) command(command byte, data ...byte) error {
	if err := device.dc.SetValue(0); err != nil {
		return err
	}
	if _, err := device.spi.Write([]byte{command}); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := device.dc.SetValue(1); err != nil {
		return err
	}
	return device.writeChunks(data)
}

func (device *ST7735) writeChunks(data []byte) error {
	for len(data) > 0 {
		length := len(data)
		if length > maximumSPIWrite {
			length = maximumSPIWrite
		}
		written, err := device.spi.Write(data[:length])
		if err != nil {
			return err
		}
		if written != length {
			return fmt.Errorf("short ST7735 SPI write: %d of %d bytes", written, length)
		}
		data = data[length:]
	}
	return nil
}

func (device *ST7735) Close() error {
	var firstError error
	if device.spi != nil && device.dc != nil {
		if err := device.command(0x28); err != nil {
			firstError = err
		}
	}
	if device.backlight != nil {
		_ = device.backlight.SetValue(0)
		if err := device.backlight.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	if device.reset != nil {
		if err := device.reset.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	if device.dc != nil {
		if err := device.dc.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	if device.spi != nil {
		if err := device.spi.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}
