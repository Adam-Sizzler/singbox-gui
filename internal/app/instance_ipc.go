//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	instanceMutexName     = `Local\singbox-gui-instance-mutex`
	instanceActivateName  = `Local\singbox-gui-instance-activate`
	instancePayloadFile   = ".instance-activate.json"
	instanceWaitTimeoutMS = uint32(350)
	instanceShutdownWait  = 2 * time.Second
)

var errInstanceAlreadyRunning = errors.New("instance already running")

type instanceActivateRequest struct {
	ImportURI string `json:"import_uri"`
}

func (a *App) startInstanceIPC() error {
	a.instanceIPCMu.Lock()
	defer a.instanceIPCMu.Unlock()

	if a.instanceMutex != 0 {
		return nil
	}

	mutexName, err := windows.UTF16PtrFromString(instanceMutexName)
	if err != nil {
		return err
	}
	mutexHandle, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		if mutexHandle != 0 {
			_ = windows.CloseHandle(mutexHandle)
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return errInstanceAlreadyRunning
		}
		return err
	}

	eventName, err := windows.UTF16PtrFromString(instanceActivateName)
	if err != nil {
		_ = windows.CloseHandle(mutexHandle)
		return err
	}
	eventHandle, err := windows.CreateEvent(nil, 0, 0, eventName)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(mutexHandle)
		if eventHandle != 0 {
			_ = windows.CloseHandle(eventHandle)
		}
		return err
	}
	if eventHandle == 0 {
		_ = windows.CloseHandle(mutexHandle)
		return windows.ERROR_INVALID_HANDLE
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})

	a.instanceMutex = mutexHandle
	a.instanceEvent = eventHandle
	a.instanceStop = stopCh
	a.instanceDone = doneCh

	_ = os.Remove(a.instancePayloadPath())
	go a.instanceIPCEventLoop(eventHandle, stopCh, doneCh)
	return nil
}

func (a *App) stopInstanceIPC() {
	a.instanceIPCMu.Lock()
	mutexHandle := a.instanceMutex
	eventHandle := a.instanceEvent
	stopCh := a.instanceStop
	doneCh := a.instanceDone

	a.instanceMutex = 0
	a.instanceEvent = 0
	a.instanceStop = nil
	a.instanceDone = nil
	a.instanceIPCMu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(instanceShutdownWait):
		}
	}

	if eventHandle != 0 {
		_ = windows.CloseHandle(eventHandle)
	}
	if mutexHandle != 0 {
		_ = windows.CloseHandle(mutexHandle)
	}
	_ = os.Remove(a.instancePayloadPath())
}

func (a *App) instanceIPCEventLoop(event windows.Handle, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	for {
		select {
		case <-stop:
			return
		default:
		}

		state, err := windows.WaitForSingleObject(event, instanceWaitTimeoutMS)
		if err != nil {
			return
		}

		switch state {
		case windows.WAIT_OBJECT_0:
			a.handleInstanceActivateSignal()
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return
		}
	}
}

func (a *App) handleInstanceActivateSignal() {
	payload, ok, err := readInstanceActivateRequest(a.instancePayloadPath())
	if err != nil {
		a.log("ERROR: ошибка чтения instance IPC payload: %v", err)
		return
	}
	if !ok {
		return
	}

	if err := a.applyImportURI(payload.ImportURI); err != nil {
		a.log("ERROR: import URI не применен: %v", err)
		return
	}
	a.focusMainWindow()
}

func (a *App) applyImportURI(rawImport string) error {
	importURI := strings.TrimSpace(rawImport)
	if importURI == "" {
		return nil
	}

	cfg := a.getConfigSnapshot()
	applyImportURIToConfig(&cfg, importURI)
	if err := a.persistConfig(cfg); err != nil {
		return err
	}
	profileName := strings.TrimSpace(cfg.CurrentProfile)
	if profileName == "" {
		profileName = activeProfileFromConfig(cfg).Name
	}
	if profileName == "" {
		profileName = "default"
	}
	a.log("Получен import URI, профиль обновлен: %s", profileName)
	return nil
}

func (a *App) focusMainWindow() {
	if a.web == nil {
		return
	}

	a.web.Dispatch(func() {
		a.showMainWindowFromTray()
	})
}

func notifyRunningInstance(workDir, importURI string) bool {
	payloadPath := instancePayloadPath(workDir)
	payload := instanceActivateRequest{ImportURI: strings.TrimSpace(importURI)}
	if err := writeInstanceActivateRequest(payloadPath, payload); err != nil {
		return false
	}

	eventName, err := windows.UTF16PtrFromString(instanceActivateName)
	if err != nil {
		_ = os.Remove(payloadPath)
		return false
	}
	eventHandle, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eventName)
	if err != nil {
		_ = os.Remove(payloadPath)
		return false
	}
	defer windows.CloseHandle(eventHandle)

	if err := windows.SetEvent(eventHandle); err != nil {
		_ = os.Remove(payloadPath)
		return false
	}
	return true
}

func instancePayloadPath(workDir string) string {
	_ = workDir
	return filepath.Join(os.TempDir(), instancePayloadFile)
}

func (a *App) instancePayloadPath() string {
	return instancePayloadPath(a.workDir)
}

func writeInstanceActivateRequest(path string, req instanceActivateRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readInstanceActivateRequest(path string) (instanceActivateRequest, bool, error) {
	var req instanceActivateRequest
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return req, false, nil
		}
		return req, false, err
	}
	_ = os.Remove(path)

	if len(data) == 0 {
		return req, true, nil
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, false, err
	}
	return req, true, nil
}
