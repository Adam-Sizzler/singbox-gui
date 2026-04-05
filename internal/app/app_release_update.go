//go:build windows

package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"
)

const (
	appReleaseLatestAPIURL       = "https://api.github.com/repos/Adam-Sizzler/singbox-gui/releases/latest"
	appReleaseCheckInterval      = 12 * time.Hour
	appReleaseCheckErrorInterval = 10 * time.Minute
)

type versionTag struct {
	parts      []int
	prerelease string
}

func (a *App) appUpdateSnapshot() (bool, string, string) {
	currentTag := currentAppReleaseTag()
	if currentTag == "" {
		return false, "", ""
	}

	now := time.Now()
	a.appUpdateMu.Lock()
	shouldCheck := !a.appUpdateChecking && (a.appUpdateCheckedAt.IsZero() || !now.Before(a.appUpdateNextCheckAt))
	if shouldCheck {
		a.appUpdateChecking = true
	}
	available := a.appUpdateAvailable
	latestTag := a.appLatestReleaseTag
	latestURL := a.appLatestReleaseURL
	a.appUpdateMu.Unlock()

	if shouldCheck {
		go a.refreshAppUpdateStatus(currentTag)
	}

	return available, latestTag, latestURL
}

func (a *App) refreshAppUpdateStatus(currentTag string) {
	latestTag, latestURL, err := fetchLatestAppRelease()
	now := time.Now()

	a.appUpdateMu.Lock()
	defer a.appUpdateMu.Unlock()
	a.appUpdateChecking = false
	a.appUpdateCheckedAt = now

	if err != nil {
		a.appUpdateNextCheckAt = now.Add(appReleaseCheckErrorInterval)
		return
	}

	a.appUpdateNextCheckAt = now.Add(appReleaseCheckInterval)
	a.appLatestReleaseTag = latestTag
	a.appLatestReleaseURL = latestURL
	a.appUpdateAvailable = isVersionTagNewer(currentTag, latestTag)
}

func fetchLatestAppRelease() (string, string, error) {
	req, err := http.NewRequest(http.MethodGet, appReleaseLatestAPIURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github недоступен: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("github вернул HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", fmt.Errorf("не удалось распарсить ответ GitHub: %w", err)
	}

	tagRaw := strings.TrimSpace(body.TagName)
	parsed, ok := parseVersionTag(tagRaw)
	if !ok {
		return "", "", fmt.Errorf("получен некорректный tag_name: %q", body.TagName)
	}

	displayTag := strings.TrimSpace(tagRaw)
	if displayTag == "" || strings.EqualFold(displayTag, parsed.normalizedString()) {
		displayTag = "v" + parsed.normalizedString()
	}

	releaseURL := strings.TrimSpace(body.HTMLURL)
	if releaseURL == "" {
		releaseURL = appReleaseBaseURL + neturl.PathEscape(displayTag)
	}

	return displayTag, releaseURL, nil
}

func isVersionTagNewer(currentTag, latestTag string) bool {
	current, okCurrent := parseVersionTag(currentTag)
	latest, okLatest := parseVersionTag(latestTag)
	if !okCurrent || !okLatest {
		return false
	}

	maxLen := len(current.parts)
	if len(latest.parts) > maxLen {
		maxLen = len(latest.parts)
	}
	for i := 0; i < maxLen; i++ {
		var currentPart int
		if i < len(current.parts) {
			currentPart = current.parts[i]
		}
		var latestPart int
		if i < len(latest.parts) {
			latestPart = latest.parts[i]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}

	if latest.prerelease == "" && current.prerelease != "" {
		return true
	}
	if latest.prerelease != "" && current.prerelease == "" {
		return false
	}
	if latest.prerelease == current.prerelease {
		return false
	}

	return strings.Compare(latest.prerelease, current.prerelease) > 0
}

func parseVersionTag(raw string) (versionTag, bool) {
	tag := strings.TrimSpace(raw)
	tag = strings.TrimPrefix(tag, "v")
	tag = strings.TrimPrefix(tag, "V")
	if tag == "" {
		return versionTag{}, false
	}

	if buildIdx := strings.Index(tag, "+"); buildIdx >= 0 {
		tag = tag[:buildIdx]
	}

	parsed := versionTag{}
	if preIdx := strings.Index(tag, "-"); preIdx >= 0 {
		parsed.prerelease = strings.TrimSpace(tag[preIdx+1:])
		tag = tag[:preIdx]
	}

	core := strings.Split(tag, ".")
	if len(core) < 3 {
		return versionTag{}, false
	}

	parts := make([]int, 0, len(core))
	for _, part := range core {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 {
			return versionTag{}, false
		}
		parts = append(parts, value)
	}
	parsed.parts = parts
	return parsed, true
}

func (v versionTag) normalizedString() string {
	if len(v.parts) == 0 {
		return ""
	}
	partStrings := make([]string, 0, len(v.parts))
	for _, part := range v.parts {
		partStrings = append(partStrings, strconv.Itoa(part))
	}
	base := strings.Join(partStrings, ".")
	if v.prerelease == "" {
		return base
	}
	return base + "-" + v.prerelease
}
