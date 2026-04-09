//go:build windows

package app

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func (a *App) debugf(format string, args ...any) {
	if a == nil {
		return
	}
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	if msg == "" {
		return
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)

	a.debugMu.Lock()
	defer a.debugMu.Unlock()

	path := strings.TrimSpace(a.debugLogPath)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func (a *App) setUICloseRequested(v bool) {
	a.uiCloseMu.Lock()
	a.uiCloseRequested = v
	a.uiCloseMu.Unlock()
}

func (a *App) isUICloseRequested() bool {
	a.uiCloseMu.Lock()
	v := a.uiCloseRequested
	a.uiCloseMu.Unlock()
	return v
}
