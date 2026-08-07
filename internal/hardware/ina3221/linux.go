//go:build linux

package ina3221

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	i2cSlave         = 0x0703
	i2cSMBus         = 0x0720
	i2cSMBusWrite    = 0
	i2cSMBusRead     = 1
	i2cSMBusWordData = 3
)

type i2cSMBusRequest struct {
	ReadWrite uint8
	Command   uint8
	_         uint16
	Size      uint32
	Data      uintptr
}

type LinuxBus struct {
	file *os.File
	mu   sync.Mutex
}

func OpenLinuxBus(busNumber int, address uint8) (*LinuxBus, error) {
	if busNumber < 0 || busNumber > 255 {
		return nil, fmt.Errorf("invalid I2C bus number %d", busNumber)
	}
	if address > 0x7f {
		return nil, fmt.Errorf("invalid 7-bit I2C address 0x%x", address)
	}
	path := fmt.Sprintf("/dev/i2c-%d", busNumber)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := unix.IoctlSetInt(int(file.Fd()), i2cSlave, int(address)); err != nil {
		file.Close()
		return nil, fmt.Errorf("select I2C address 0x%x on %s: %w", address, path, err)
	}
	return &LinuxBus{file: file}, nil
}

func (bus *LinuxBus) ReadWord(register byte) (uint16, error) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	data := [34]byte{}
	if err := bus.smbus(i2cSMBusRead, register, &data); err != nil {
		return 0, err
	}
	// SMBus word data is little-endian, while INA3221 registers are big-endian.
	return bits.ReverseBytes16(binary.LittleEndian.Uint16(data[:2])), nil
}

func (bus *LinuxBus) WriteWord(register byte, value uint16) error {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	data := [34]byte{}
	binary.LittleEndian.PutUint16(data[:2], bits.ReverseBytes16(value))
	return bus.smbus(i2cSMBusWrite, register, &data)
}

func (bus *LinuxBus) smbus(readWrite uint8, command byte, data *[34]byte) error {
	var pinner runtime.Pinner
	pinner.Pin(data)
	defer pinner.Unpin()

	request := i2cSMBusRequest{
		ReadWrite: readWrite,
		Command:   command,
		Size:      i2cSMBusWordData,
		Data:      uintptr(unsafe.Pointer(&data[0])),
	}
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		bus.file.Fd(),
		uintptr(i2cSMBus),
		uintptr(unsafe.Pointer(&request)),
	)
	runtime.KeepAlive(data)
	runtime.KeepAlive(request)
	if errno != 0 {
		return errno
	}
	return nil
}

func (bus *LinuxBus) Close() error {
	if bus == nil || bus.file == nil {
		return nil
	}
	return bus.file.Close()
}
