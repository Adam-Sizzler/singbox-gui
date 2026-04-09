//go:build windows

package app

import (
	"fmt"
	"strings"
)

const (
	uiStylesTag = `<link rel="stylesheet" href="/styles.css">`
	uiScriptTag = `<script src="/app.js"></script>`
)

func loadEmbeddedUIHTML() (string, error) {
	indexBytes, err := uiAssets.ReadFile("web/ui/index.html")
	if err != nil {
		return "", fmt.Errorf("read index.html: %w", err)
	}
	stylesBytes, err := uiAssets.ReadFile("web/ui/styles.css")
	if err != nil {
		return "", fmt.Errorf("read styles.css: %w", err)
	}
	scriptBytes, err := uiAssets.ReadFile("web/ui/app.js")
	if err != nil {
		return "", fmt.Errorf("read app.js: %w", err)
	}

	html := string(indexBytes)
	if !strings.Contains(html, uiStylesTag) {
		return "", fmt.Errorf("styles tag not found in index.html")
	}
	if !strings.Contains(html, uiScriptTag) {
		return "", fmt.Errorf("script tag not found in index.html")
	}

	html = strings.Replace(html, uiStylesTag, "<style>\n"+string(stylesBytes)+"\n</style>", 1)
	html = strings.Replace(html, uiScriptTag, "<script>\n"+string(scriptBytes)+"\n</script>", 1)
	return html, nil
}
