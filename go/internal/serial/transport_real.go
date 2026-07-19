//go:build !test

// transport_real.go — opens a real serial port with go.bug.st/serial.
//
// This file is excluded from the test build (tag `test`) so unit
// tests don't need the go.bug.st/serial dependency or a real
// /dev/ttyACM0. The production daemon (built without `-tags test`)
// links this and calls OpenSerialPort.
//
// The returned Port is a *serial.Port, which implements
// io.ReadWriteCloser. The Transport wraps it.

package serial

import (
	"fmt"

	"go.bug.st/serial"
)

// OpenSerialPort opens a real serial port at the given device path
// and baud rate. The caller owns the returned Port; pass it to
// NewTransport and call Close when done.
func OpenSerialPort(device string, baud int) (Port, error) {
	mode := &serial.Mode{
		BaudRate: baud,
	}
	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s @ %d: %w", device, baud, err)
	}
	return port, nil
}
