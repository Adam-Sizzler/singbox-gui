//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

func Run(args []string) {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("[%s] panic: %v\n\n%s", time.Now().Format("2006-01-02 15:04:05"), r, debug.Stack())
			fallbackPath := filepath.Join(os.TempDir(), "singbox-wrapper-fatal.log")
			_ = os.WriteFile(fallbackPath, []byte(msg), 0600)
			showError("Fatal panic", msg)
		}
	}()

	hideConsoleWindow()

	workDir, err := executableDir()
	if err != nil {
		showError("Startup error", "Не удалось определить рабочую директорию:\n"+err.Error())
		return
	}

	app := newApp(workDir)

	startupImport := findImportURIArg(args)
	app.startupImport = startupImport

	if notifyRunningInstance(startupImport) {
		return
	}

	if !isRunningAsAdmin() {
		showError("Admin rights required", "Приложение должно быть запущено с правами администратора.")
		return
	}

	if err := ensureSingBoxProtocolRegistration(); err != nil {
		app.protoRegWarn = err.Error()
	}

	cfg, err := loadOrCreateConfig(app.configPath)
	if err != nil {
		showError("Config error", "Не удалось прочитать config.yaml:\n"+err.Error())
		return
	}
	normalizeConfigProfiles(&cfg)
	applyImportURIToConfig(&cfg, app.startupImport)

	if err := saveConfig(app.configPath, cfg); err != nil {
		showError("Config error", "Не удалось сохранить config.yaml:\n"+err.Error())
		return
	}
	app.setConfig(cfg)

	if err := app.startInstanceIPC(); err != nil {
		if errors.Is(err, errInstanceAlreadyRunning) {
			notifyRunningInstance(app.startupImport)
			return
		}
		app.log("WARN: не удалось запустить instance IPC: %v", err)
	}
	defer func() {
		app.stopInstanceIPC()
	}()

	if err := app.runUI(); err != nil {
		showError("UI error", err.Error())
		return
	}
}

func newApp(workDir string) *App {
	return &App{
		workDir:     workDir,
		configPath:  filepath.Join(workDir, configFileName),
		singBoxPath: filepath.Join(workDir, singboxExeName),
		logEntries:  make([]logEntry, 0, maxLogLines),
	}
}
