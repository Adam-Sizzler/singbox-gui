//go:build windows

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type apiError struct {
	Error string `json:"error"`
}

type logsResponse struct {
	Entries []logEntry `json:"entries"`
	LastID  int64      `json:"last_id"`
}

type profileRequest struct {
	Name string `json:"name"`
}

func (a *App) startUIServer() error {
	a.uiSrvMu.Lock()
	defer a.uiSrvMu.Unlock()

	if a.uiServer != nil && a.uiBaseURL != "" {
		return nil
	}

	sub, err := fs.Sub(uiAssets, "web/ui")
	if err != nil {
		return err
	}
	staticFS := http.FileServer(http.FS(sub))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", a.handleAPIState)
	mux.HandleFunc("/api/profile/new", a.handleAPIProfileNew)
	mux.HandleFunc("/api/profile/delete", a.handleAPIProfileDelete)
	mux.HandleFunc("/api/action/check-config", a.handleAPICheckConfig)
	mux.HandleFunc("/api/action/start-stop", a.handleAPIStartStop)
	mux.HandleFunc("/api/action/copy-logs", a.handleAPICopyLogs)
	mux.HandleFunc("/api/action/update-app", a.handleAPIUpdateApp)
	mux.HandleFunc("/api/logs", a.handleAPILogs)

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		staticFS.ServeHTTP(w, r)
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	a.uiServer = srv
	a.uiBaseURL = "http://" + ln.Addr().String()

	if err := waitForHTTPReady(a.uiBaseURL+"/", 3*time.Second); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()

		a.uiServer = nil
		a.uiBaseURL = ""
		return fmt.Errorf("ui server is not ready: %w", err)
	}
	return nil
}

func (a *App) stopUIServer() {
	a.uiSrvMu.Lock()
	srv := a.uiServer
	a.uiServer = nil
	a.uiBaseURL = ""
	a.uiSrvMu.Unlock()

	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (a *App) handleAPIState(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.snapshotState())
		return
	case http.MethodPost:
		var patch StatePatch
		if err := decodeJSONBody(r, &patch); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
			return
		}
		if err := a.applyStatePatch(patch); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, a.snapshotState())
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAPIProfileNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req profileRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if err := a.createProfile(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.snapshotState())
}

func (a *App) handleAPIProfileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req profileRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if err := a.deleteProfile(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.snapshotState())
}

func (a *App) handleAPIStartStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.toggleStartStop(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.snapshotState())
}

func (a *App) handleAPICheckConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.checkConfigAction(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, a.snapshotState())
}

func (a *App) handleAPICopyLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.copyLogsToClipboard(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAPIUpdateApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := a.updateApplicationAction(); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	fromID := int64(0)
	if s := strings.TrimSpace(r.URL.Query().Get("from")); s != "" {
		if parsed, err := strconv.ParseInt(s, 10, 64); err == nil && parsed >= 0 {
			fromID = parsed
		}
	}
	entries, lastID := a.logsSince(fromID)
	writeJSON(w, http.StatusOK, logsResponse{Entries: entries, LastID: lastID})
}

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("пустое тело запроса")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func waitForHTTPReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 300 * time.Millisecond}
	var lastErr error

	for {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			// Any HTTP response means listener+handler pipeline is alive.
			return nil
		}
		lastErr = err

		if time.Now().After(deadline) {
			if lastErr == nil {
				return errors.New("timeout waiting for UI server")
			}
			return lastErr
		}
		time.Sleep(60 * time.Millisecond)
	}
}
