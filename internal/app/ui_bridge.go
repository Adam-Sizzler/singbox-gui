//go:build windows

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const webViewBridgeAPICallBinding = "__sbApiCall"

type logsResponse struct {
	Entries []logEntry `json:"entries"`
	LastID  int64      `json:"last_id"`
}

type profileRequest struct {
	Name string `json:"name"`
}

type uiBridgeRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

func (a *App) bindUIBridge() error {
	if a.web == nil {
		return fmt.Errorf("webview is not initialized")
	}
	return a.web.Bind(webViewBridgeAPICallBinding, func(req uiBridgeRequest) (any, error) {
		return a.handleUIBridgeCall(req)
	})
}

func (a *App) handleUIBridgeCall(req uiBridgeRequest) (any, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		return nil, fmt.Errorf("пустой метод")
	}

	rawPath := strings.TrimSpace(req.Path)
	if rawPath == "" {
		return nil, fmt.Errorf("пустой путь")
	}

	parsedPath, err := url.Parse(rawPath)
	if err != nil {
		return nil, fmt.Errorf("некорректный путь: %w", err)
	}
	routePath := strings.TrimSpace(parsedPath.Path)
	if routePath == "" {
		routePath = "/"
	}
	switch method {
	case "GET":
		switch routePath {
		case "/api/state":
			return a.snapshotState(), nil
		case "/api/logs":
			fromID := int64(0)
			if s := strings.TrimSpace(parsedPath.Query().Get("from")); s != "" {
				if parsed, err := strconv.ParseInt(s, 10, 64); err == nil && parsed >= 0 {
					fromID = parsed
				}
			}
			entries, lastID := a.logsSince(fromID)
			return logsResponse{Entries: entries, LastID: lastID}, nil
		}
	case "POST":
		switch routePath {
		case "/api/state":
			var patch StatePatch
			if err := decodeBridgeBody(req.Body, &patch); err != nil {
				return nil, err
			}
			if err := a.applyStatePatch(patch); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/profile/new":
			var profileReq profileRequest
			if err := decodeBridgeBody(req.Body, &profileReq); err != nil {
				return nil, err
			}
			if err := a.createProfile(profileReq.Name); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/profile/delete":
			var profileReq profileRequest
			if err := decodeBridgeBody(req.Body, &profileReq); err != nil {
				return nil, err
			}
			if err := a.deleteProfile(profileReq.Name); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/profile/rename":
			var profileReq profileRequest
			if err := decodeBridgeBody(req.Body, &profileReq); err != nil {
				return nil, err
			}
			if err := a.renameProfile(profileReq.Name); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/selector/select":
			var selectorReq selectorRequest
			if err := decodeBridgeBody(req.Body, &selectorReq); err != nil {
				return nil, err
			}
			if err := a.setSelectorOutbound(selectorReq.Selector, selectorReq.Outbound); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/action/start-stop":
			if err := a.toggleStartStop(); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/action/check-config":
			if err := a.checkConfigAction(); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/action/refresh-config":
			if err := a.refreshConfigAction(); err != nil {
				return nil, err
			}
			return a.snapshotState(), nil
		case "/api/action/copy-logs":
			if err := a.copyLogsToClipboard(); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		case "/api/action/update-app":
			if err := a.updateApplicationAction(); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		}
	}

	return nil, fmt.Errorf("неподдерживаемый API вызов: %s %s", method, routePath)
}

func decodeBridgeBody(raw json.RawMessage, v any) error {
	body := bytes.TrimSpace(raw)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		body = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}
