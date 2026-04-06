//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

var (
	kernel32DLL           = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole     = kernel32DLL.NewProc("AttachConsole")
	procFreeConsole       = kernel32DLL.NewProc("FreeConsole")
	procGenerateCtrlEvent = kernel32DLL.NewProc("GenerateConsoleCtrlEvent")
	procSetCtrlHandler    = kernel32DLL.NewProc("SetConsoleCtrlHandler")
)

func (a *App) isProcessRunning() bool {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	return a.proc != nil && a.proc.Process != nil
}

func (a *App) setCoreDesiredRunning(v bool) {
	a.coreDesiredMu.Lock()
	a.coreDesiredRunning = v
	a.coreDesiredMu.Unlock()
}

func (a *App) coreDesiredRunningSnapshot() bool {
	a.coreDesiredMu.Lock()
	defer a.coreDesiredMu.Unlock()
	return a.coreDesiredRunning
}

func (a *App) processUptimeSeconds() int64 {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if a.proc == nil || a.proc.Process == nil || a.procStartedAt.IsZero() {
		return 0
	}
	seconds := int64(time.Since(a.procStartedAt).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (a *App) toggleStartStop() error {
	return a.withRunningAction(func() error {
		if a.isProcessRunning() {
			a.setCoreDesiredRunning(false)
			a.stopProcess()
			return nil
		}
		a.setCoreDesiredRunning(true)
		if err := a.startPipeline(); err != nil {
			a.setCoreDesiredRunning(false)
			return err
		}
		return nil
	})
}

func (a *App) checkConfigAction() error {
	return a.withRunningAction(func() error {
		cfg := a.getConfigSnapshot()
		if err := validateConfig(cfg); err != nil {
			return err
		}

		active := activeProfileFromConfig(cfg)
		profileName := strings.TrimSpace(active.Name)
		if profileName == "" {
			profileName = "default"
		}

		resolvedConfigURL, _, err := resolveSubscriptionInput(active.URL)
		if err != nil {
			return err
		}

		if strings.TrimSpace(resolvedConfigURL) == "" {
			if err := a.ensureLocalRuntimeConfig(); err != nil {
				return err
			}
			a.log("Проверка конфигурации OK: локальный %s валиден (профиль: %s)", runtimeCfgName, profileName)
			return nil
		}

		if err := validateRemoteRuntimeConfig(resolvedConfigURL); err != nil {
			return err
		}
		a.log("Проверка конфигурации OK: URL доступен и JSON валиден (профиль: %s)", profileName)
		return nil
	})
}

func (a *App) withRunningAction(fn func() error) error {
	a.runMu.Lock()
	if a.runningAction {
		a.runMu.Unlock()
		return errors.New("операция уже выполняется")
	}
	a.runningAction = true
	a.runMu.Unlock()
	defer func() {
		a.runMu.Lock()
		a.runningAction = false
		a.runMu.Unlock()
	}()
	return fn()
}

func (a *App) startPipeline() error {
	if !isRunningAsAdmin() {
		return errors.New("приложение запущено без прав администратора")
	}

	cfg := a.getConfigSnapshot()
	if err := validateConfig(cfg); err != nil {
		return err
	}
	active := activeProfileFromConfig(cfg)
	resolvedConfigURL, _, err := resolveSubscriptionInput(active.URL)
	if err != nil {
		return err
	}

	if active.Name != "" {
		a.log("Профиль: %s", active.Name)
	}
	if err := saveConfig(a.configPath, cfg); err != nil {
		return fmt.Errorf("не удалось сохранить %s: %w", configFileName, err)
	}
	a.log("Сохранён %s", configFileName)

	resolvedVersion, err := resolveVersion(active.Version)
	if err != nil {
		return fmt.Errorf("не удалось определить версию sing-box: %w", err)
	}
	if err := a.ensureSingBox(resolvedVersion); err != nil {
		return err
	}

	if strings.TrimSpace(resolvedConfigURL) == "" {
		if err := a.ensureLocalRuntimeConfig(); err != nil {
			return err
		}
		a.log("URL не задан, использую локальный %s", runtimeCfgName)
	} else {
		updated, err := a.refreshRuntimeConfigFromURL(resolvedConfigURL)
		if err != nil {
			return err
		}
		if updated {
			a.log("Скачан и обновлён %s", runtimeCfgName)
		} else {
			a.log("%s уже актуален", runtimeCfgName)
		}
	}

	a.stopProcess()
	if err := a.startProcess(); err != nil {
		return err
	}

	a.log("sing-box запущен")
	a.setCoreDesiredRunning(true)
	return nil
}

func (a *App) ensureLocalRuntimeConfig() error {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return ensureLocalRuntimeConfig(a.runtimeCfg)
}

func (a *App) refreshRuntimeConfigFromURL(url string) (bool, error) {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return downloadRuntimeConfig(url, a.runtimeCfg)
}

func (a *App) ensureSingBox(targetVersion string) error {
	installedVersion, err := detectSingBoxVersion(a.singBoxPath)
	if err != nil {
		return fmt.Errorf("не удалось проверить установленную версию sing-box: %w", err)
	}
	if installedVersion == targetVersion {
		a.log("Найдена подходящая версия sing-box: %s", installedVersion)
		return nil
	}

	a.log("Требуется sing-box %s (текущая: %s)", targetVersion, emptyIf(installedVersion, "не найден"))
	if err := downloadAndInstallSingBox(targetVersion, a.singBoxPath); err != nil {
		return err
	}
	a.log("Установлен sing-box %s", targetVersion)
	return nil
}

func (a *App) startProcess() error {
	if _, err := os.Stat(a.singBoxPath); err != nil {
		return fmt.Errorf("не найден %s", singboxExeName)
	}
	if _, err := os.Stat(a.runtimeCfg); err != nil {
		return fmt.Errorf("не найден %s", runtimeCfgName)
	}

	cmd := exec.Command(a.singBoxPath, "run", "-c", a.runtimeCfg)
	cmd.Dir = a.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup}
	cfg := a.getConfigSnapshot()
	envOverrides := normalizeSingboxEnv(cfg.SingboxEnv)
	if len(envOverrides) > 0 {
		env := os.Environ()
		for key, value := range envOverrides {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	a.procMu.Lock()
	a.proc = cmd
	a.procStopRequested = false
	a.procWaitDone = done
	a.procStartedAt = time.Now()
	a.procMu.Unlock()

	go a.pipeLogs(stdout)
	go a.pipeLogs(stderr)

	go func(proc *exec.Cmd, waitDone chan struct{}) {
		err := proc.Wait()
		close(waitDone)

		a.procMu.Lock()
		wasStop := a.procStopRequested
		if a.proc == proc {
			a.proc = nil
			a.procStopRequested = false
			a.procWaitDone = nil
			a.procStartedAt = time.Time{}
		}
		a.procMu.Unlock()

		if err != nil {
			if !wasStop {
				a.log("WARN: sing-box завершился с ошибкой: %v", err)
			}
			return
		}
		if !wasStop {
			a.log("sing-box завершился")
		}
	}(cmd, done)

	return nil
}

func (a *App) stopProcess() {
	a.procMu.Lock()
	proc := a.proc
	waitDone := a.procWaitDone
	if proc == nil || proc.Process == nil {
		a.procMu.Unlock()
		return
	}
	a.procStopRequested = true
	pid := proc.Process.Pid
	a.procMu.Unlock()

	a.log("Остановка sing-box (pid=%d)", pid)

	graceful := tryGracefulProcessStop(pid, proc.Process)
	if graceful && waitDone != nil {
		if waitForProcessExit(waitDone, gracefulStopTimeout) {
			a.log("sing-box остановлен")
			return
		}
		a.log("WARN: таймаут мягкой остановки, применяю принудительное завершение")
	}

	if err := proc.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		a.log("WARN: не удалось завершить процесс: %v", err)
	}

	if waitDone != nil {
		_ = waitForProcessExit(waitDone, forceStopTimeout)
	}
	a.log("sing-box остановлен")
}

func waitForProcessExit(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func tryGracefulProcessStop(pid int, proc *os.Process) bool {
	if pid <= 0 || proc == nil {
		return false
	}
	if err := sendCtrlBreakToProcessGroup(pid); err != nil {
		return false
	}
	return true
}

func sendCtrlBreakToProcessGroup(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := kernel32DLL.Load(); err != nil {
		return err
	}
	if err := procAttachConsole.Find(); err != nil {
		return err
	}
	if err := procFreeConsole.Find(); err != nil {
		return err
	}
	if err := procGenerateCtrlEvent.Find(); err != nil {
		return err
	}
	if err := procSetCtrlHandler.Find(); err != nil {
		return err
	}

	_, _, _ = procFreeConsole.Call()
	if ret, _, callErr := procAttachConsole.Call(uintptr(pid)); ret == 0 {
		return normalizeWinProcErr("AttachConsole", callErr)
	}
	defer procFreeConsole.Call()

	if ret, _, callErr := procSetCtrlHandler.Call(0, 1); ret == 0 {
		return normalizeWinProcErr("SetConsoleCtrlHandler(add)", callErr)
	}
	defer procSetCtrlHandler.Call(0, 0)

	if ret, _, callErr := procGenerateCtrlEvent.Call(uintptr(ctrlBreakEvent), uintptr(pid)); ret == 0 {
		return normalizeWinProcErr("GenerateConsoleCtrlEvent", callErr)
	}

	time.Sleep(120 * time.Millisecond)
	return nil
}

func normalizeWinProcErr(api string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", api)
	}
	return fmt.Errorf("%s: %w", api, err)
}

func emptyIf(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func commandWithTimeout(bin string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
	return cmd.CombinedOutput()
}
