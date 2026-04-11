//go:build windows

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	selectorCacheTTL        = 2 * time.Second
	clashAPIRequestTimeout  = 2200 * time.Millisecond
	clashApplyRetryInterval = 350 * time.Millisecond
	clashApplyRetryMaxTries = 8
)

var clashAPIHTTPClient = &http.Client{Timeout: clashAPIRequestTimeout}

type SelectorGroupState struct {
	Name      string   `json:"name"`
	Current   string   `json:"current"`
	Options   []string `json:"options"`
	CanSwitch bool     `json:"can_switch"`
}

type selectorRequest struct {
	Selector string `json:"selector"`
	Outbound string `json:"outbound"`
}

type clashHTTPError struct {
	StatusCode int
	Message    string
}

func (e *clashHTTPError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("clash api вернул HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("clash api вернул HTTP %d: %s", e.StatusCode, e.Message)
}

func allocateLocalControllerAddr() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	addr := strings.TrimSpace(listener.Addr().String())
	if addr == "" {
		return "", errors.New("не удалось определить адрес clash api")
	}
	return addr, nil
}

func generateClashSecret() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func singBoxSupportsClashAPI(singboxPath string) (bool, error) {
	output, err := commandWithTimeout(singboxPath, 6*time.Second, "version")
	if err != nil {
		return false, err
	}

	lowerOutput := strings.ToLower(string(output))
	tagIndex := strings.Index(lowerOutput, "tags:")
	if tagIndex < 0 {
		return true, nil
	}

	tagLine := lowerOutput[tagIndex:]
	if newlineIndex := strings.IndexByte(tagLine, '\n'); newlineIndex >= 0 {
		tagLine = tagLine[:newlineIndex]
	}
	tagPayload := strings.TrimSpace(strings.TrimPrefix(tagLine, "tags:"))
	if tagPayload == "" {
		return false, nil
	}

	parts := strings.FieldsFunc(tagPayload, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t':
			return true
		default:
			return false
		}
	})
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), "with_clash_api") {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) ensureRuntimeConfigHasClashAPI(runtimeCfgPath, controller, secret string) error {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return ensureRuntimeConfigHasClashAPI(runtimeCfgPath, controller, secret)
}

func (a *App) stripRuntimeConfigClashAPI(runtimeCfgPath string) error {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return stripRuntimeConfigClashAPI(runtimeCfgPath)
}

func ensureRuntimeConfigHasClashAPI(path, controller, secret string) error {
	controller = strings.TrimSpace(controller)
	if controller == "" {
		return errors.New("пустой external_controller")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return fmt.Errorf("runtime-конфиг невалидный JSON: %w", err)
	}
	if root == nil {
		root = make(map[string]any)
	}

	experimental := ensureJSONObject(root, "experimental")
	clashAPI := ensureJSONObject(experimental, "clash_api")
	clashAPI["external_controller"] = controller
	clashAPI["secret"] = strings.TrimSpace(secret)

	updated, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return atomicWriteFile(path, updated, 0o644)
}

func stripRuntimeConfigClashAPI(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return fmt.Errorf("runtime-конфиг невалидный JSON: %w", err)
	}
	if root == nil {
		return nil
	}

	experimental, ok := root["experimental"].(map[string]any)
	if !ok || experimental == nil {
		return nil
	}
	if _, exists := experimental["clash_api"]; !exists {
		return nil
	}

	delete(experimental, "clash_api")
	if len(experimental) == 0 {
		delete(root, "experimental")
	}

	updated, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return atomicWriteFile(path, updated, 0o644)
}

func atomicWriteFile(path string, content []byte, fallbackPerm os.FileMode) error {
	perm := fallbackPerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Chmod(perm); err != nil {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func ensureJSONObject(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return map[string]any{}
	}
	if existing, ok := parent[key].(map[string]any); ok && existing != nil {
		return existing
	}
	newObject := make(map[string]any)
	parent[key] = newObject
	return newObject
}

func readSelectorGroupsFromRuntimeConfig(path string) ([]SelectorGroupState, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("runtime-конфиг невалидный JSON: %w", err)
	}
	groups := parseSelectorGroupsFromConfigRoot(root)
	return groups, nil
}

func parseSelectorGroupsFromConfigRoot(root map[string]any) []SelectorGroupState {
	if root == nil {
		return nil
	}

	outboundsRaw, ok := root["outbounds"].([]any)
	if !ok || len(outboundsRaw) == 0 {
		return nil
	}

	result := make([]SelectorGroupState, 0, len(outboundsRaw))
	for _, outboundRaw := range outboundsRaw {
		outbound, ok := outboundRaw.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(parseString(outbound["type"]), "selector") {
			continue
		}
		name := strings.TrimSpace(parseString(outbound["tag"]))
		if name == "" {
			continue
		}
		options := parseStringArray(outbound["outbounds"])
		if len(options) == 0 {
			continue
		}
		current := strings.TrimSpace(parseString(outbound["default"]))
		if !containsStringFold(options, current) {
			current = options[0]
		}
		result = append(result, SelectorGroupState{
			Name:    name,
			Current: current,
			Options: options,
		})
	}
	return result
}

func parseStringArray(raw any) []string {
	itemsRaw, ok := raw.([]any)
	if !ok || len(itemsRaw) == 0 {
		return nil
	}
	out := make([]string, 0, len(itemsRaw))
	for _, item := range itemsRaw {
		text := strings.TrimSpace(parseString(item))
		if text == "" {
			continue
		}
		if containsStringFold(out, text) {
			continue
		}
		out = append(out, text)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseString(raw any) string {
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func containsStringFold(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func cloneSelectorGroups(groups []SelectorGroupState) []SelectorGroupState {
	if len(groups) == 0 {
		return nil
	}
	cloned := make([]SelectorGroupState, len(groups))
	for i, group := range groups {
		cloned[i] = SelectorGroupState{
			Name:      group.Name,
			Current:   group.Current,
			Options:   append([]string(nil), group.Options...),
			CanSwitch: group.CanSwitch,
		}
	}
	return cloned
}

func (a *App) clashSessionSnapshot() (controller, secret, runtimeCfgPath string) {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	return a.clashController, a.clashSecret, a.clashRuntimeCfg
}

func (a *App) setClashSession(controller, secret, runtimeCfgPath string) {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	a.clashController = strings.TrimSpace(controller)
	a.clashSecret = strings.TrimSpace(secret)
	a.clashRuntimeCfg = strings.TrimSpace(runtimeCfgPath)
	a.clearSelectorCacheLocked()
}

func (a *App) resetClashSession() {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	a.clashController = ""
	a.clashSecret = ""
	a.clashRuntimeCfg = ""
	a.clearSelectorCacheLocked()
}

func (a *App) invalidateSelectorCache() {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	a.clearSelectorCacheLocked()
}

func (a *App) clearSelectorCacheLocked() {
	a.selectorCacheProfile = ""
	a.selectorCacheLive = false
	a.selectorCacheExpiresAt = time.Time{}
	a.selectorCacheGroups = nil
}

func (a *App) selectorCacheSnapshot(profileName string, now time.Time) ([]SelectorGroupState, bool, bool) {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	if strings.TrimSpace(profileName) == "" {
		return nil, false, false
	}
	if !strings.EqualFold(a.selectorCacheProfile, profileName) {
		return nil, false, false
	}
	if a.selectorCacheExpiresAt.IsZero() || now.After(a.selectorCacheExpiresAt) {
		return nil, false, false
	}
	return cloneSelectorGroups(a.selectorCacheGroups), a.selectorCacheLive, true
}

func (a *App) setSelectorCache(profileName string, groups []SelectorGroupState, live bool, now time.Time) {
	a.clashMu.Lock()
	defer a.clashMu.Unlock()
	a.selectorCacheProfile = strings.TrimSpace(profileName)
	a.selectorCacheLive = live
	a.selectorCacheExpiresAt = now.Add(selectorCacheTTL)
	a.selectorCacheGroups = cloneSelectorGroups(groups)
	for i := range a.selectorCacheGroups {
		a.selectorCacheGroups[i].CanSwitch = false
	}
}

func (a *App) selectorGroupsSnapshot(active ConfigProfile, running bool, busy bool) []SelectorGroupState {
	profileName := strings.TrimSpace(active.Name)
	if profileName == "" {
		profileName = "profile-1"
	}

	now := time.Now()
	cached, live, ok := a.selectorCacheSnapshot(profileName, now)
	if ok {
		canSwitch := !busy && (!running || live)
		for i := range cached {
			cached[i].CanSwitch = canSwitch
		}
		return cached
	}

	var (
		groups []SelectorGroupState
		err    error
	)
	live = false

	if running {
		groups, err = a.clashGetProxies()
		if err == nil && len(groups) > 0 {
			live = true
		}
	}

	if len(groups) == 0 {
		groups, _ = a.selectorGroupsFromRuntimeProfile(profileName, active.SelectorSelections)
	}

	a.setSelectorCache(profileName, groups, live, now)

	cloned := cloneSelectorGroups(groups)
	canSwitch := !busy && (!running || live)
	for i := range cloned {
		cloned[i].CanSwitch = canSwitch
	}
	return cloned
}

func (a *App) selectorGroupsFromRuntimeProfile(profileName string, savedSelections map[string]string) ([]SelectorGroupState, error) {
	runtimeCfgPath := a.runtimeConfigPathForProfile(profileName)
	candidates := []string{runtimeCfgPath}
	legacyPath := filepath.Join(a.workDir, legacyRuntimeCfgName)
	if !strings.EqualFold(strings.TrimSpace(legacyPath), strings.TrimSpace(runtimeCfgPath)) {
		candidates = append(candidates, legacyPath)
	}

	a.runtimeCfgMu.Lock()
	groups, _, err := readSelectorGroupsFromRuntimeCandidates(candidates)
	a.runtimeCfgMu.Unlock()
	if err != nil {
		return nil, err
	}

	applySelectorSelectionsToGroups(groups, savedSelections)
	return groups, nil
}

func readSelectorGroupsFromRuntimeCandidates(paths []string) ([]SelectorGroupState, string, error) {
	if len(paths) == 0 {
		return nil, "", os.ErrNotExist
	}

	seen := make(map[string]struct{}, len(paths))
	var firstErr error

	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		lower := strings.ToLower(path)
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}

		groups, err := readSelectorGroupsFromRuntimeConfig(path)
		if err == nil {
			return groups, path, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return nil, "", firstErr
	}
	return nil, "", os.ErrNotExist
}

func applySelectorSelectionsToGroups(groups []SelectorGroupState, selections map[string]string) {
	normalized := normalizeSelectorSelections(selections)
	if len(groups) == 0 || len(normalized) == 0 {
		return
	}
	for i := range groups {
		selected, ok := selectionForGroup(normalized, groups[i].Name)
		if !ok {
			continue
		}
		if option, ok := optionForGroup(groups[i], selected); ok {
			groups[i].Current = option
		}
	}
}

func selectionForGroup(selections map[string]string, groupName string) (string, bool) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return "", false
	}
	for key, value := range selections {
		if strings.EqualFold(strings.TrimSpace(key), groupName) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func findSelectorGroup(groups []SelectorGroupState, selector string) (SelectorGroupState, bool) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return SelectorGroupState{}, false
	}
	for _, group := range groups {
		if strings.EqualFold(group.Name, selector) {
			return group, true
		}
	}
	return SelectorGroupState{}, false
}

func optionForGroup(group SelectorGroupState, outbound string) (string, bool) {
	outbound = strings.TrimSpace(outbound)
	if outbound == "" {
		return "", false
	}
	for _, option := range group.Options {
		if strings.EqualFold(strings.TrimSpace(option), outbound) {
			return option, true
		}
	}
	return "", false
}

func (a *App) clashGetProxies() ([]SelectorGroupState, error) {
	var payload struct {
		Proxies map[string]json.RawMessage `json:"proxies"`
	}
	if err := a.clashAPIRequest(http.MethodGet, "/proxies", nil, &payload); err != nil {
		return nil, err
	}

	if len(payload.Proxies) == 0 {
		return nil, nil
	}

	groups := make([]SelectorGroupState, 0, len(payload.Proxies))
	for name, raw := range payload.Proxies {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if !strings.EqualFold(parseString(item["type"]), "selector") {
			continue
		}
		options := parseStringArray(item["all"])
		if len(options) == 0 {
			continue
		}
		current := strings.TrimSpace(parseString(item["now"]))
		if !containsStringFold(options, current) {
			current = options[0]
		}
		tag := strings.TrimSpace(name)
		if tag == "" {
			tag = strings.TrimSpace(parseString(item["name"]))
		}
		if tag == "" {
			continue
		}
		groups = append(groups, SelectorGroupState{
			Name:    tag,
			Current: current,
			Options: options,
		})
	}

	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups, nil
}

func (a *App) clashSwitchSelector(selectorTag, outboundTag string) error {
	type updateProxyRequest struct {
		Name string `json:"name"`
	}
	path := "/proxies/" + neturl.PathEscape(strings.TrimSpace(selectorTag))
	return a.clashAPIRequest(http.MethodPut, path, updateProxyRequest{Name: strings.TrimSpace(outboundTag)}, nil)
}

func (a *App) clashSwitchSelectorWithRetry(selectorTag, outboundTag string, maxTries int) error {
	if maxTries < 1 {
		maxTries = 1
	}
	var lastErr error
	for attempt := 0; attempt < maxTries; attempt++ {
		if err := a.clashSwitchSelector(selectorTag, outboundTag); err != nil {
			lastErr = err
			if !isRetryableClashError(err) || attempt+1 >= maxTries {
				return lastErr
			}
			time.Sleep(160 * time.Millisecond)
			continue
		}
		return nil
	}
	return lastErr
}

func (a *App) clashAPIRequest(method, route string, payload any, out any) error {
	controller, secret, _ := a.clashSessionSnapshot()
	controller = strings.TrimSpace(controller)
	if controller == "" {
		return errors.New("clash api не инициализирован")
	}
	secret = strings.TrimSpace(secret)

	endpoint := "http://" + controller + route
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := clashAPIHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &clashHTTPError{
			StatusCode: resp.StatusCode,
			Message:    parseClashAPIError(bodyBytes),
		}
	}

	if out == nil || len(bytes.TrimSpace(bodyBytes)) == 0 {
		return nil
	}

	if err := json.Unmarshal(bodyBytes, out); err != nil {
		return err
	}
	return nil
}

func parseClashAPIError(raw []byte) string {
	type errorBody struct {
		Error string `json:"error"`
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var parsed errorBody
	if err := json.Unmarshal(raw, &parsed); err == nil {
		if msg := strings.TrimSpace(parsed.Error); msg != "" {
			return msg
		}
	}
	if len(trimmed) > 200 {
		return trimmed[:200]
	}
	return trimmed
}

func isRetryableClashError(err error) bool {
	if err == nil {
		return false
	}

	var statusErr *clashHTTPError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode >= 500 || statusErr.StatusCode == http.StatusTooManyRequests
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		return true
	}

	return false
}

func (a *App) selectorGroupsForSelection(profile ConfigProfile, running bool) ([]SelectorGroupState, bool, error) {
	profileName := strings.TrimSpace(profile.Name)
	if profileName == "" {
		profileName = "profile-1"
	}

	if running {
		groups, err := a.clashGetProxies()
		if err == nil && len(groups) > 0 {
			return groups, true, nil
		}

		runtimeGroups, runtimeErr := a.selectorGroupsFromRuntimeProfile(profileName, profile.SelectorSelections)
		if runtimeErr != nil {
			if err != nil {
				return nil, false, err
			}
			return nil, false, runtimeErr
		}
		if err != nil {
			return runtimeGroups, false, err
		}
		return runtimeGroups, false, nil
	}

	groups, err := a.selectorGroupsFromRuntimeProfile(profileName, profile.SelectorSelections)
	return groups, false, err
}

func (a *App) setSelectorOutbound(selectorTag, outboundTag string) error {
	startedAt := time.Now()
	a.runMu.Lock()
	busy := a.runningAction
	a.runMu.Unlock()
	if busy {
		return errors.New("операция уже выполняется")
	}

	selectorTag = strings.TrimSpace(selectorTag)
	outboundTag = strings.TrimSpace(outboundTag)
	if selectorTag == "" {
		return errors.New("selector не указан")
	}
	if outboundTag == "" {
		return errors.New("outbound не указан")
	}

	cfg := a.getConfigSnapshot()
	idx := activeProfileIndex(&cfg)
	if idx < 0 || idx >= len(cfg.Profiles) {
		return errors.New("активный профиль не найден")
	}
	active := cfg.Profiles[idx]
	running := a.isProcessRunning()
	profileName := strings.TrimSpace(active.Name)
	if profileName == "" {
		profileName = "profile-1"
	}
	a.log("SELECTOR: запрос переключения profile=%q selector=%q outbound=%q running=%t", profileName, selectorTag, outboundTag, running)

	groups, live, sourceErr := a.selectorGroupsForSelection(active, running)
	if len(groups) == 0 && sourceErr != nil {
		a.log("WARN: SELECTOR: невозможно получить группы selector %q: %v", selectorTag, sourceErr)
		return sourceErr
	}
	group, ok := findSelectorGroup(groups, selectorTag)
	if !ok {
		err := fmt.Errorf("selector %q не найден", selectorTag)
		a.log("WARN: SELECTOR: %v", err)
		return err
	}
	resolvedOutbound, ok := optionForGroup(group, outboundTag)
	if !ok {
		err := fmt.Errorf("outbound %q не найден в selector %q", outboundTag, group.Name)
		a.log("WARN: SELECTOR: %v", err)
		return err
	}

	selections := normalizeSelectorSelections(cfg.Profiles[idx].SelectorSelections)
	if selections == nil {
		selections = make(map[string]string, 1)
	}
	selections[group.Name] = resolvedOutbound
	cfg.Profiles[idx].SelectorSelections = selections
	syncLegacyFromCurrent(&cfg)
	if err := a.persistConfig(cfg); err != nil {
		a.log("WARN: SELECTOR: не удалось сохранить выбор %q -> %q: %v", group.Name, resolvedOutbound, err)
		return err
	}
	a.invalidateSelectorCache()

	if !running {
		a.log("SELECTOR: сохранено %q -> %q (core stopped, %d ms)", group.Name, resolvedOutbound, time.Since(startedAt).Milliseconds())
		return nil
	}
	if !live {
		if err := a.clashSwitchSelectorWithRetry(group.Name, resolvedOutbound, 3); err == nil {
			a.invalidateSelectorCache()
			a.log("SELECTOR: переключено %q -> %q (runtime fallback, %d ms)", group.Name, resolvedOutbound, time.Since(startedAt).Milliseconds())
			return nil
		} else {
			a.log("WARN: SELECTOR: clash api переключение %q -> %q завершилось ошибкой: %v", group.Name, resolvedOutbound, err)
		}
		if sourceErr != nil {
			wrappedErr := fmt.Errorf("live переключение selector недоступно: %w", sourceErr)
			a.log("WARN: SELECTOR: %v", wrappedErr)
			return wrappedErr
		}
		err := errors.New("live переключение selector недоступно")
		a.log("WARN: SELECTOR: %v", err)
		return err
	}

	if err := a.clashSwitchSelectorWithRetry(group.Name, resolvedOutbound, 3); err != nil {
		wrappedErr := fmt.Errorf("не удалось переключить selector %q: %w", group.Name, err)
		a.log("WARN: SELECTOR: %v", wrappedErr)
		return wrappedErr
	}
	a.invalidateSelectorCache()
	a.log("SELECTOR: переключено %q -> %q (live, %d ms)", group.Name, resolvedOutbound, time.Since(startedAt).Milliseconds())
	return nil
}

func (a *App) applySavedSelectorSelections(profile ConfigProfile) {
	selections := normalizeSelectorSelections(profile.SelectorSelections)
	if len(selections) == 0 {
		return
	}
	profileName := strings.TrimSpace(profile.Name)
	if profileName == "" {
		return
	}
	copiedSelections := cloneSelectorSelections(selections)

	go func(expectedProfile string, expectedSelections map[string]string) {
		var lastErr error
		for attempt := 0; attempt < clashApplyRetryMaxTries; attempt++ {
			if !a.isProcessRunning() {
				return
			}

			currentCfg := a.getConfigSnapshot()
			currentActive := activeProfileFromConfig(currentCfg)
			if !strings.EqualFold(currentActive.Name, expectedProfile) {
				return
			}

			groups, err := a.clashGetProxies()
			if err != nil {
				lastErr = err
				if isRetryableClashError(err) {
					time.Sleep(clashApplyRetryInterval)
					continue
				}
				break
			}

			retry := false
			for selectorTag, outboundTag := range expectedSelections {
				group, ok := findSelectorGroup(groups, selectorTag)
				if !ok {
					continue
				}
				resolvedOutbound, ok := optionForGroup(group, outboundTag)
				if !ok {
					continue
				}
				if strings.EqualFold(group.Current, resolvedOutbound) {
					continue
				}
				if err := a.clashSwitchSelectorWithRetry(group.Name, resolvedOutbound, 2); err != nil {
					lastErr = err
					if isRetryableClashError(err) {
						retry = true
						break
					}
					a.log("WARN: selector %q не переключен в %q: %v", group.Name, resolvedOutbound, err)
					continue
				}
			}

			if retry {
				time.Sleep(clashApplyRetryInterval)
				continue
			}

			a.invalidateSelectorCache()
			return
		}

		if lastErr != nil {
			a.log("WARN: не удалось применить сохраненные selector-настройки: %v", lastErr)
		}
	}(profileName, copiedSelections)
}
