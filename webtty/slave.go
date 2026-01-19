package webtty

import (
	"io"
)

// Slave represents a PTY slave, typically it's a local command.
type Slave interface {
	io.ReadWriter

	// WindowTitleVariables returns any values that can be used to fill out
	// the title of a terminal.
	WindowTitleVariables() map[string]interface{}

	// ResizeTerminal sets a new size of the terminal.
	ResizeTerminal(columns int, rows int) error

	// GetWorkingDir returns the current working directory of the PTY slave process.
	// This is used for file uploads to save files to the correct directory.
	GetWorkingDir() (string, error)
}
