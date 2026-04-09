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
			fallbackPath := filepath.Join(os.TempDir(), "singbox-gui-fatal.log")
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

	startupImport := findImportURIArg(args)
	if notifyRunningInstance(workDir, startupImport) {
		return
	}

	if !isRunningAsAdmin() {
		showError("Admin rights required", "Приложение должно быть запущено с правами администратора.")
		return
	}

	app := newApp(workDir)
	app.startupImport = startupImport
	app.debugf("startup: args=%q", args)
	app.debugf("startup: workDir=%s", workDir)
	app.debugf("startup: startupImport=%q", startupImport)

	if err := ensureSingBoxProtocolRegistration(); err != nil {
		app.protoRegWarn = err.Error()
		app.debugf("startup: protocol registration warning: %v", err)
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
		app.debugf("startup: failed to start instance IPC: %v", err)
		if errors.Is(err, errInstanceAlreadyRunning) {
			if notifyRunningInstance(workDir, app.startupImport) {
				app.debugf("startup: existing instance accepted activation, exiting")
			} else {
				app.debugf("startup: existing instance detected, activation signal failed")
			}
			return
		}
		app.log("WARN: не удалось запустить instance IPC: %v", err)
	}
	defer app.stopInstanceIPC()

	if err := app.runUI(); err != nil {
		app.debugf("fatal: runUI returned error: %v", err)
		showError("UI error", err.Error()+"\n\nDebug log:\n"+app.debugLogPath)
	}
}

func newApp(workDir string) *App {
	debugPath := filepath.Join(workDir, "singbox-gui-debug.log")
	return &App{
		workDir:      workDir,
		configPath:   filepath.Join(workDir, configFileName),
		singBoxPath:  filepath.Join(workDir, singboxExeName),
		debugLogPath: debugPath,
		logEntries:   make([]logEntry, 0, maxLogLines),
	}
}
