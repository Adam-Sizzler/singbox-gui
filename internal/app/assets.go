//go:build windows

package app

import "embed"

//go:embed web/ui/*
var uiAssets embed.FS
