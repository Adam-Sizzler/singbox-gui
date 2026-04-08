//go:build windows

package app

import (
	"path/filepath"
)

func Run(args []string) {
	hideConsoleWindow()

	startupImport := findImportURIArg(args)
	if notifyRunningInstance(startupImport) {
		return
	}

	if !isRunningAsAdmin() {
		showError("Admin rights required", "Приложение должно быть запущено с правами администратора.")
		return
	}

	workDir, err := executableDir()
	if err != nil {
		showError("Startup error", "Не удалось определить рабочую директорию:\n"+err.Error())
		return
	}

	app := newApp(workDir)
	app.startupImport = startupImport

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
	if err := app.startInstanceServer(); err != nil {
		if notifyRunningInstance(app.startupImport) {
			return
		}
		app.log("WARN: не удалось запустить instance-server: %v", err)
	}
	defer app.stopInstanceServer()

	if err := app.runUI(); err != nil {
		showError("UI error", err.Error())
	}
}

func newApp(workDir string) *App {
	return &App{
		workDir:     workDir,
		configPath:  filepath.Join(workDir, configFileName),
		singBoxPath: filepath.Join(workDir, singboxExeName),
		runtimeCfg:  filepath.Join(workDir, runtimeCfgName),
		logEntries:  make([]logEntry, 0, maxLogLines),
	}
}
