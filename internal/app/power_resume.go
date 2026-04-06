//go:build windows

package app

import "time"

const (
	powerWatchTick       = 3 * time.Second
	powerResumeThreshold = 20 * time.Second
)

func (a *App) startPowerResumeWatcher() {
	if a.powerWatchStop != nil {
		return
	}
	stop := make(chan struct{})
	a.powerWatchStop = stop

	go func() {
		ticker := time.NewTicker(powerWatchTick)
		defer ticker.Stop()

		prev := time.Now()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				gap := now.Sub(prev)
				prev = now
				if gap <= powerWatchTick+powerResumeThreshold {
					continue
				}
				a.recoverCoreAfterResume()
			}
		}
	}()
}

func (a *App) stopPowerResumeWatcher() {
	if a.powerWatchStop == nil {
		return
	}
	close(a.powerWatchStop)
	a.powerWatchStop = nil
}

func (a *App) recoverCoreAfterResume() {
	if !a.coreDesiredRunningSnapshot() || a.isProcessRunning() {
		return
	}
	a.log("Обнаружен выход системы из сна, восстанавливаю sing-box")

	go func() {
		err := a.withRunningAction(func() error {
			if !a.coreDesiredRunningSnapshot() || a.isProcessRunning() {
				return nil
			}
			return a.startPipeline()
		})
		if err != nil {
			a.log("WARN: не удалось восстановить sing-box после сна: %v", err)
			return
		}
		if a.isProcessRunning() {
			a.log("sing-box успешно восстановлен после сна")
		}
	}()
}
