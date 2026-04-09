//go:build windows

package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
)

const webViewBridgeOpenExternalBinding = "__sbOpenExternal"
const webViewBridgeReadyBinding = "__sbOnReady"

const webViewBridgeScript = `(function () {
  if (window.__sbBridgeInstalled) return;
  window.__sbBridgeInstalled = true;

  function notifyReady() {
    if (typeof window.__sbOnReady !== "function") return;
    try { window.__sbOnReady(); } catch (e) {}
  }

  function postExternal(url) {
    if (typeof window.__sbOpenExternal !== "function") return;
    try { window.__sbOpenExternal(String(url || "")); } catch (e) {}
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

  document.addEventListener("click", function (event) {
    if (!event) return;
    var node = event.target;
    while (node && node.tagName !== "A") {
      node = node.parentElement;
    }
    if (!node) return;
    var href = node.getAttribute("href");
    if (!href) return;
    if (maybeExternal(href)) {
      if (event.preventDefault) event.preventDefault();
      if (event.stopPropagation) event.stopPropagation();
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

  if (document.readyState === "complete" || document.readyState === "interactive") {
    notifyReady();
  } else {
    document.addEventListener("DOMContentLoaded", notifyReady, { once: true });
  }
})();`

type webViewHost struct {
	view webview.WebView
	hwnd win.HWND
}

func newWebViewHost(onReady func(), onExternalURL func(string)) (*webViewHost, error) {
	view := webview.New(false)
	if view == nil {
		return nil, errors.New("failed to initialize webview")
	}

	host := &webViewHost{view: view}

	rawHWND := view.Window()
	if rawHWND == nil {
		view.Destroy()
		return nil, errors.New("failed to acquire webview window handle")
	}
	host.hwnd = win.HWND(uintptr(rawHWND))
	if host.hwnd == 0 {
		view.Destroy()
		return nil, errors.New("invalid webview window handle")
	}
	win.ShowWindow(host.hwnd, win.SW_HIDE)

	if onExternalURL != nil {
		if err := view.Bind(webViewBridgeOpenExternalBinding, func(raw string) {
			target := strings.TrimSpace(raw)
			if target == "" {
				return
			}
			onExternalURL(target)
		}); err != nil {
			view.Destroy()
			return nil, fmt.Errorf("bind external bridge failed: %w", err)
		}
	}

	if onReady != nil {
		var once sync.Once
		if err := view.Bind(webViewBridgeReadyBinding, func() {
			once.Do(onReady)
		}); err != nil {
			view.Destroy()
			return nil, fmt.Errorf("bind ready bridge failed: %w", err)
		}
	}

	view.Init(webViewBridgeScript)
	return host, nil
}

func (w *webViewHost) SetTitle(title string) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.view.SetTitle(title)
	return nil
}

func (w *webViewHost) SetSize(width, height int, hint webview.Hint) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.view.SetSize(width, height, hint)
	return nil
}

func (w *webViewHost) SetHTML(html string) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.view.SetHtml(html)
	return nil
}

func (w *webViewHost) Bind(name string, f any) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	return w.view.Bind(name, f)
}

func (w *webViewHost) Dispatch(f func()) {
	if w == nil || w.view == nil || f == nil {
		return
	}
	w.view.Dispatch(f)
}

func (w *webViewHost) Run() error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.view.Run()
	return nil
}

func (w *webViewHost) Terminate() {
	if w == nil || w.view == nil {
		return
	}
	w.view.Terminate()
}

func (w *webViewHost) Destroy() {
	if w == nil || w.view == nil {
		return
	}
	w.view.Destroy()
	w.view = nil
	w.hwnd = 0
}

func (w *webViewHost) HWND() win.HWND {
	if w == nil {
		return 0
	}
	if w.view == nil {
		return w.hwnd
	}
	raw := w.view.Window()
	if raw == nil {
		return w.hwnd
	}
	w.hwnd = win.HWND(uintptr(raw))
	return w.hwnd
}
