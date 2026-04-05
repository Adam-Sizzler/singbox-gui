//go:build windows

package main

import (
	"os"

	"singbox-gui-client/internal/app"
)

func main() {
	app.Run(os.Args[1:])
}
