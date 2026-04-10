//go:build windows

package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
)

const webViewBridgeOpenExternalBinding = "__sbOpenExternal"
const webViewBridgeReadyBinding = "__sbOnReady"

const webViewBridgeScript = `(function () {
  if (window.__sbBridgeInstalled) return;
  window.__sbBridgeInstalled = true;

  function openExternal(url) {
    if (typeof window.__sbOpenExternal !== "function") return;
    try { window.__sbOpenExternal(String(url || "")); } catch (e) {}
  }

  function maybeOpenExternal(raw) {
    try {
      var resolved = new URL(String(raw || ""), window.location.href);
      if (!/^https?:$/i.test(resolved.protocol)) return false;
      if (resolved.origin === window.location.origin) return false;
      openExternal(resolved.href);
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
    if (maybeOpenExternal(href)) {
      if (event.preventDefault) event.preventDefault();
      if (event.stopPropagation) event.stopPropagation();
    }
  }, true);

  var originalOpen = window.open;
  window.open = function (url) {
    if (maybeOpenExternal(url)) {
      return null;
    }
    if (typeof originalOpen === "function") {
      return originalOpen.apply(window, arguments);
    }
    return null;
  };

  var readyHandled = false;
  function notifyReady() {
    if (readyHandled) return;
    readyHandled = true;
    if (typeof window.__sbOnReady !== "function") return;
    try { window.__sbOnReady(); } catch (e) {}
  }

  if (document.readyState === "complete" || document.readyState === "interactive") {
    notifyReady();
  } else {
    document.addEventListener("DOMContentLoaded", function () {
      notifyReady();
    });
    window.addEventListener("load", notifyReady);
  }
})();`

type webViewHost struct {
	view  webview.WebView
	hwnd  win.HWND
	debug func(string, ...any)
}

func newWebViewHost(
	parentHWND win.HWND,
	startHidden bool,
	onReady func(),
	onExternalURL func(string),
	debugf func(string, ...any),
) (*webViewHost, error) {
	if debugf != nil {
		debugf("webview: creating host parent=%#x", uintptr(parentHWND))
	}
	var view webview.WebView
	if parentHWND != 0 {
		view = webview.NewWindow(false, unsafe.Pointer(parentHWND))
	} else {
		view = webview.New(false)
	}
	if view == nil {
		return nil, errors.New("failed to initialize webview")
	}

	host := &webViewHost{view: view, debug: debugf}
	host.debugf("webview: native view object created")

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
	host.debugf("webview: native hwnd=%#x", uintptr(host.hwnd))
	if parentHWND != 0 {
		host.debugf("webview: embedded into parent hwnd=%#x", uintptr(parentHWND))
	} else if startHidden {
		win.ShowWindow(host.hwnd, win.SW_HIDE)
		host.debugf("webview: initial top-level window hidden")
	} else {
		host.debugf("webview: initial top-level window left visible")
	}

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
		host.debugf("webview: external URL bridge bound")
	}

	if onReady != nil {
		var once sync.Once
		if err := view.Bind(webViewBridgeReadyBinding, func() {
			once.Do(onReady)
		}); err != nil {
			view.Destroy()
			return nil, fmt.Errorf("bind ready bridge failed: %w", err)
		}
		host.debugf("webview: ready bridge bound")
	}

	view.Init(webViewBridgeScript)
	host.debugf("webview: init script injected")
	return host, nil
}

func (w *webViewHost) debugf(format string, args ...any) {
	if w == nil || w.debug == nil {
		return
	}
	w.debug(format, args...)
}

func (w *webViewHost) SetTitle(title string) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.debugf("webview: SetTitle(%q)", title)
	w.view.SetTitle(title)
	return nil
}

func (w *webViewHost) SetSize(width, height int, hint webview.Hint) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.debugf("webview: SetSize(width=%d height=%d hint=%d)", width, height, hint)
	w.view.SetSize(width, height, hint)
	return nil
}

func (w *webViewHost) SetHTML(html string) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.debugf("webview: SetHTML(length=%d)", len(html))
	w.view.SetHtml(html)
	return nil
}

func (w *webViewHost) Eval(js string) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.debugf("webview: Eval(length=%d)", len(js))
	w.view.Eval(js)
	return nil
}

func (w *webViewHost) Bind(name string, f any) error {
	if w == nil || w.view == nil {
		return errors.New("webview is not initialized")
	}
	w.debugf("webview: Bind(%s)", name)
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
	w.debugf("webview: Run enter")
	w.view.Run()
	w.debugf("webview: Run exit")
	return nil
}

func (w *webViewHost) Terminate() {
	if w == nil || w.view == nil {
		return
	}
	w.debugf("webview: Terminate called")
	w.view.Terminate()
}

func (w *webViewHost) Destroy() {
	if w == nil || w.view == nil {
		return
	}
	w.debugf("webview: Destroy called")
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
