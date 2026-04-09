//go:build windows

package app

import (
	"errors"
	"strings"
	"sync"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/lxn/win"
)

const webView2ExternalOpenPrefix = "openExternal:"

const webView2ExternalBridgeScript = `(function () {
  if (window.__sbExternalBridgeInstalled) return;
  window.__sbExternalBridgeInstalled = true;

  function postExternal(url) {
    if (!window.chrome || !window.chrome.webview || typeof window.chrome.webview.postMessage !== "function") {
      return;
    }
    window.chrome.webview.postMessage("openExternal:" + String(url || ""));
  }

  function maybeExternal(raw) {
    try {
      var resolved = new URL(String(raw || ""), window.location.href);
      if (!/^https?:$/i.test(resolved.protocol)) return false;
      if (resolved.origin === window.location.origin) return false;
      postExternal(resolved.href);
      return true;
    } catch (e) {
      return false;
    }
  }

  document.addEventListener("click", function (ev) {
    if (!ev) return;
    var node = ev.target;
    while (node && node.tagName !== "A") {
      node = node.parentElement;
    }
    if (!node) return;
    var href = node.getAttribute("href");
    if (!href) return;
    if (maybeExternal(href)) {
      if (ev.preventDefault) ev.preventDefault();
      if (ev.stopPropagation) ev.stopPropagation();
    }
  }, true);

  var originalOpen = window.open;
  window.open = function (url) {
    if (maybeExternal(url)) {
      return null;
    }
    if (typeof originalOpen === "function") {
      return originalOpen.apply(window, arguments);
    }
    return null;
  };
})();`

type webViewHost struct {
	chromium *edge.Chromium
}

func newWebViewHost(parent win.HWND, onReady func(), onExternalURL func(string)) (*webViewHost, error) {
	if parent == 0 {
		return nil, errors.New("invalid parent window handle")
	}

	chromium := edge.NewChromium()
	chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)

	if onExternalURL != nil {
		chromium.MessageCallback = func(message string) {
			raw := strings.TrimSpace(message)
			if !strings.HasPrefix(raw, webView2ExternalOpenPrefix) {
				return
			}
			target := strings.TrimSpace(strings.TrimPrefix(raw, webView2ExternalOpenPrefix))
			if target == "" {
				return
			}
			onExternalURL(target)
		}
	}

	if onReady != nil {
		var once sync.Once
		chromium.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
			once.Do(onReady)
		}
	}

	if ok := chromium.Embed(uintptr(parent)); !ok {
		return nil, errors.New("failed to initialize WebView2")
	}
	chromium.Resize()

	if settings, err := chromium.GetSettings(); err == nil && settings != nil {
		_ = settings.PutIsWebMessageEnabled(true)
		_ = settings.PutIsScriptEnabled(true)
		_ = settings.PutAreDefaultContextMenusEnabled(true)
		_ = settings.PutAreBrowserAcceleratorKeysEnabled(true)
		_ = settings.PutIsStatusBarEnabled(false)
	}

	chromium.Init(webView2ExternalBridgeScript)
	return &webViewHost{chromium: chromium}, nil
}

func (w *webViewHost) Navigate(url string) error {
	if w == nil || w.chromium == nil {
		return errors.New("webview is not initialized")
	}
	w.chromium.Navigate(url)
	return nil
}

func (w *webViewHost) Resize() {
	if w == nil || w.chromium == nil {
		return
	}
	w.chromium.Resize()
}

func (w *webViewHost) NotifyParentWindowPositionChanged() {
	if w == nil || w.chromium == nil {
		return
	}
	_ = w.chromium.NotifyParentWindowPositionChanged()
}
