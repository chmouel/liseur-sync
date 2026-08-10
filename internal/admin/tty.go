package admin

import (
	"os"

	"golang.org/x/term"
)

// readNoEcho reads a line from a terminal with echo disabled.
func readNoEcho(tty *os.File) (string, error) {
	b, err := term.ReadPassword(int(tty.Fd()))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
