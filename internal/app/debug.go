//go:build windows

package app

func (a *App) debugf(format string, args ...any) {
	_ = a
	_ = format
	_ = args
	// Debug file logging is disabled in production builds.
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
