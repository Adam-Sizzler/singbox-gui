//go:build windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lxn/win"
)

const (
	instanceServerAddr        = "127.0.0.1:36127"
	instanceServerBaseURL     = "http://" + instanceServerAddr
	instanceServerPingPath    = "/instance/ping"
	instanceServerActivateURL = "/instance/activate"
	instanceServerAppName     = "singbox-gui"
)

type instancePingResponse struct {
	App string `json:"app"`
}

type instanceActivateRequest struct {
	ImportURI string `json:"import_uri"`
}

func (a *App) startInstanceServer() error {
	a.instanceSrvMu.Lock()
	defer a.instanceSrvMu.Unlock()

	if a.instanceSrv != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc(instanceServerPingPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, instancePingResponse{App: instanceServerAppName})
	})
	mux.HandleFunc(instanceServerActivateURL, a.handleInstanceActivate)

	ln, err := net.Listen("tcp", instanceServerAddr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 4 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	a.instanceSrv = srv
	return nil
}

func (a *App) stopInstanceServer() {
	a.instanceSrvMu.Lock()
	srv := a.instanceSrv
	a.instanceSrv = nil
	a.instanceSrvMu.Unlock()

	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (a *App) handleInstanceActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req instanceActivateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}

	if err := a.applyImportURI(req.ImportURI); err != nil {
		a.log("ERROR: import URI не применен: %v", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
		return
	}

	a.focusMainWindow()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	if a.mw == nil {
		return
	}

	a.mw.Synchronize(func() {
		if a.mw == nil {
			return
		}
		hwnd := a.mw.Handle()
		if hwnd == 0 {
			return
		}
		win.ShowWindow(hwnd, win.SW_RESTORE)
		win.ShowWindow(hwnd, win.SW_SHOW)
		win.BringWindowToTop(hwnd)
		win.SetForegroundWindow(hwnd)
	})
}

func notifyRunningInstance(importURI string) bool {
	trimmed := strings.TrimSpace(importURI)
	if trimmed == "" {
		return false
	}

	client := &http.Client{Timeout: 600 * time.Millisecond}
	if !instanceServerAlive(client) {
		return false
	}

	body, err := json.Marshal(instanceActivateRequest{ImportURI: trimmed})
	if err != nil {
		return false
	}

	req, err := http.NewRequest(http.MethodPost, instanceServerBaseURL+instanceServerActivateURL, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func instanceServerAlive(client *http.Client) bool {
	resp, err := client.Get(instanceServerBaseURL + instanceServerPingPath)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var ping instancePingResponse
	if err := json.NewDecoder(resp.Body).Decode(&ping); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ping.App), instanceServerAppName)
}
