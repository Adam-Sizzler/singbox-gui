//go:build windows

package app

import (
	"strings"
	"time"
)

func (a *App) startAutoUpdateScheduler() {
	a.autoUpdateMu.Lock()
	if a.autoUpdateStop != nil {
		a.autoUpdateMu.Unlock()
		return
	}
	stop := make(chan struct{})
	wake := make(chan struct{}, 1)
	a.autoUpdateStop = stop
	a.autoUpdateWake = wake
	a.autoUpdateMu.Unlock()

	go a.autoUpdateLoop(stop, wake)
	a.triggerAutoUpdateReconfigure()
}

func (a *App) stopAutoUpdateScheduler() {
	a.autoUpdateMu.Lock()
	stop := a.autoUpdateStop
	a.autoUpdateStop = nil
	a.autoUpdateWake = nil
	a.autoUpdateMu.Unlock()

	if stop != nil {
		close(stop)
	}
}

func (a *App) triggerAutoUpdateReconfigure() {
	a.autoUpdateMu.Lock()
	wake := a.autoUpdateWake
	a.autoUpdateMu.Unlock()

	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (a *App) autoUpdateLoop(stop <-chan struct{}, wake <-chan struct{}) {
	var timer *time.Timer
	var timerC <-chan time.Time

	resetTimer := func(delay time.Duration) {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer = nil
			timerC = nil
		}
		if delay <= 0 {
			return
		}
		timer = time.NewTimer(delay)
		timerC = timer.C
	}

	resetTimer(a.autoUpdateDelay())
	for {
		select {
		case <-stop:
			if timer != nil {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
			return
		case <-wake:
			resetTimer(a.autoUpdateDelay())
		case <-timerC:
			a.runAutoUpdateOnce()
			resetTimer(a.autoUpdateDelay())
		}
	}
}

func (a *App) autoUpdateDelay() time.Duration {
	cfg := a.getConfigSnapshot()
	hours := normalizeAutoUpdateHours(cfg.AutoUpdateHours)
	if hours <= 0 {
		return 0
	}
	return time.Duration(hours) * time.Hour
}

func (a *App) runAutoUpdateOnce() {
	cfg := a.getConfigSnapshot()
	hours := normalizeAutoUpdateHours(cfg.AutoUpdateHours)
	if hours <= 0 {
		return
	}

	active := activeProfileFromConfig(cfg)
	profileName := strings.TrimSpace(active.Name)
	if profileName == "" {
		profileName = "default"
	}

	resolvedConfigURL, _, err := resolveSubscriptionInput(active.URL)
	if err != nil {
		a.log("WARN: автообновление профиля %s пропущено: %v", profileName, err)
		return
	}
	if strings.TrimSpace(resolvedConfigURL) == "" {
		return
	}

	updated, err := a.refreshRuntimeConfigFromURL(resolvedConfigURL)
	if err != nil {
		a.log("WARN: автообновление профиля %s: %v", profileName, err)
		return
	}
	if updated {
		a.log("Автообновление: обновлён %s (профиль: %s)", runtimeCfgName, profileName)
	}
}
