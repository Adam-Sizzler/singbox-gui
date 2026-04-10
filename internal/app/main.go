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

	app := newApp(workDir)
	app.debugf("================================================================================")
	app.debugf("startup: launch pid=%d startedAt=%s", os.Getpid(), time.Now().Format(time.RFC3339Nano))
	startupStepN := 0
	startupStep := func(format string, args ...any) {
		startupStepN++
		app.debugf("startup[%02d]: %s", startupStepN, fmt.Sprintf(format, args...))
	}

	startupStep("args=%q", args)
	startupStep("workDir=%s", workDir)

	startupImport := findImportURIArg(args)
	startupStep("startupImport=%q", startupImport)
	app.startupImport = startupImport

	startupStep("probing running instance")
	if notifyRunningInstance(workDir, startupImport) {
		startupStep("activation sent to existing instance; exiting current process")
		return
	}
	startupStep("no running instance handled activation")

	startupStep("checking administrator privileges")
	if !isRunningAsAdmin() {
		startupStep("administrator privileges are missing")
		showError("Admin rights required", "Приложение должно быть запущено с правами администратора.")
		return
	}
	startupStep("administrator privileges confirmed")

	startupStep("registering sing-box URI protocol handler")
	if err := ensureSingBoxProtocolRegistration(); err != nil {
		app.protoRegWarn = err.Error()
		startupStep("protocol registration warning: %v", err)
	} else {
		startupStep("protocol registration completed")
	}

	startupStep("loading config: %s", app.configPath)
	cfg, err := loadOrCreateConfig(app.configPath)
	if err != nil {
		startupStep("failed to load config: %v", err)
		showError("Config error", "Не удалось прочитать config.yaml:\n"+err.Error())
		return
	}
	normalizeConfigProfiles(&cfg)
	applyImportURIToConfig(&cfg, app.startupImport)
	startupStep("config loaded: profiles=%d current=%q", len(cfg.Profiles), cfg.CurrentProfile)

	startupStep("saving normalized config")
	if err := saveConfig(app.configPath, cfg); err != nil {
		startupStep("failed to save config: %v", err)
		showError("Config error", "Не удалось сохранить config.yaml:\n"+err.Error())
		return
	}
	app.setConfig(cfg)
	startupStep("config saved and applied")

	startupStep("starting instance IPC listener")
	if err := app.startInstanceIPC(); err != nil {
		startupStep("failed to start instance IPC: %v", err)
		if errors.Is(err, errInstanceAlreadyRunning) {
			if notifyRunningInstance(workDir, app.startupImport) {
				startupStep("existing instance accepted activation after IPC conflict; exiting")
			} else {
				startupStep("existing instance detected, activation signal failed")
			}
			return
		}
		app.log("WARN: не удалось запустить instance IPC: %v", err)
	} else {
		startupStep("instance IPC listener started")
	}
	defer func() {
		startupStep("stopping instance IPC listener")
		app.stopInstanceIPC()
	}()

	startupStep("starting UI loop")
	if err := app.runUI(); err != nil {
		startupStep("UI loop returned error: %v", err)
		app.debugf("fatal: runUI returned error: %v", err)
		showError("UI error", err.Error())
		return
	}
	startupStep("UI loop finished without error")
}

func newApp(workDir string) *App {
	return &App{
		workDir:     workDir,
		configPath:  filepath.Join(workDir, configFileName),
		singBoxPath: filepath.Join(workDir, singboxExeName),
		logEntries:  make([]logEntry, 0, maxLogLines),
	}
}
