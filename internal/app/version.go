//go:build windows

package app

import (
	"net/url"
	"strings"
)

const appReleaseBaseURL = "https://github.com/Adam-Sizzler/singbox-gui/releases/tag/"

// appReleaseTag is injected at build time via -ldflags -X.
var appReleaseTag = "dev"

func currentAppReleaseTag() string {
	tag := strings.TrimSpace(appReleaseTag)
	if tag == "" || strings.EqualFold(tag, "dev") {
		return ""
	}
	return tag
}

func currentAppReleaseURL() string {
	tag := currentAppReleaseTag()
	if tag == "" {
		return ""
	}
	return appReleaseBaseURL + url.PathEscape(tag)
}
