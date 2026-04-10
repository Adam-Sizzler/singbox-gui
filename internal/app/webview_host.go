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
const webViewBridgeDebugBinding = "__sbDebug"

const webViewBridgeScript = `(function () {
  if (window.__sbBridgeInstalled) return;
  window.__sbBridgeInstalled = true;

  function safeToString(value) {
    try {
      return String(value);
    } catch (e) {
      return "<stringify-error>";
    }
  }

  function postDebug(kind, details) {
    if (typeof window.__sbDebug !== "function") return;
    var payload = {
      kind: safeToString(kind || ""),
      details: details == null ? null : details,
      href: safeToString((window.location && window.location.href) || ""),
      ts: Date.now()
    };
    try { window.__sbDebug(JSON.stringify(payload)); } catch (e) {}
  }

  function snapshot(tag) {
    var body = document.body;
    var app = document.querySelector(".app");
    var bodyStyle = body && window.getComputedStyle ? window.getComputedStyle(body) : null;
    var appStyle = app && window.getComputedStyle ? window.getComputedStyle(app) : null;

    postDebug("snapshot", {
      tag: safeToString(tag || ""),
      readyState: safeToString(document.readyState || ""),
      bodyClass: body ? safeToString(body.className || "") : "",
      bodyBackground: bodyStyle ? safeToString(bodyStyle.backgroundColor || "") : "",
      appExists: !!app,
      appDisplay: appStyle ? safeToString(appStyle.display || "") : "",
      appVisibility: appStyle ? safeToString(appStyle.visibility || "") : "",
      appOpacity: appStyle ? safeToString(appStyle.opacity || "") : "",
      appClientWidth: app ? (app.clientWidth || 0) : 0,
      appClientHeight: app ? (app.clientHeight || 0) : 0,
      viewportWidth: window.innerWidth || 0,
      viewportHeight: window.innerHeight || 0,
      hasApiBridge: typeof window.__sbApiCall === "function",
      hasReadyBridge: typeof window.__sbOnReady === "function"
    });
  }

  function notifyReady() {
    snapshot("notifyReady");
    if (typeof window.__sbOnReady !== "function") return;
    try { window.__sbOnReady(); } catch (e) {
      postDebug("ready-callback-error", {
        message: safeToString(e && e.message ? e.message : e)
      });
    }
  }

  function postExternal(url) {
    if (typeof window.__sbOpenExternal !== "function") return;
    try { window.__sbOpenExternal(String(url || "")); } catch (e) {
      postDebug("open-external-error", {
        message: safeToString(e && e.message ? e.message : e)
      });
    }
  }

  function maybeExternal(raw) {
    try {
      var resolved = new URL(String(raw || ""), window.location.href);
      if (!/^https?:$/i.test(resolved.protocol)) return false;
      if (resolved.origin === window.location.origin) return false;
      postExternal(resolved.href);
      return true;
    } catch (e) {
      postDebug("url-parse-error", {
        raw: safeToString(raw || ""),
        message: safeToString(e && e.message ? e.message : e)
      });
      return false;
    }
  }

  window.addEventListener("error", function (event) {
    postDebug("window.error", {
      message: safeToString(event && event.message ? event.message : ""),
      source: safeToString(event && event.filename ? event.filename : ""),
      line: event && event.lineno ? event.lineno : 0,
      col: event && event.colno ? event.colno : 0,
      stack: event && event.error && event.error.stack ? safeToString(event.error.stack).slice(0, 2000) : ""
    });
  });

  window.addEventListener("unhandledrejection", function (event) {
    var reason = event ? event.reason : "";
    postDebug("window.unhandledrejection", {
      reason: reason && reason.stack ? safeToString(reason.stack).slice(0, 2000) : safeToString(reason || "")
    });
  });

  if (window.console && typeof window.console === "object") {
    var levels = ["log", "warn", "error"];
    for (var i = 0; i < levels.length; i++) {
      (function (level) {
        var original = window.console[level];
        if (typeof original !== "function") return;
        window.console[level] = function () {
          var args = [];
          for (var j = 0; j < arguments.length && j < 5; j++) {
            args.push(safeToString(arguments[j]));
          }
          postDebug("console." + level, { args: args });
          return original.apply(window.console, arguments);
        };
      })(levels[i]);
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

  document.addEventListener("readystatechange", function () {
    snapshot("readystatechange:" + safeToString(document.readyState || ""));
  });

  if (document.readyState === "complete" || document.readyState === "interactive") {
    notifyReady();
  } else {
    var readyHandled = false;
    document.addEventListener("DOMContentLoaded", function () {
      if (readyHandled) return;
      readyHandled = true;
      snapshot("domcontentloaded");
      notifyReady();
    });
  }

  window.addEventListener("load", function () {
    snapshot("window-load");
  });

  if (typeof window.requestAnimationFrame === "function") {
    window.requestAnimationFrame(function () {
      snapshot("raf");
    });
  }

  setTimeout(function () { snapshot("timeout-100"); }, 100);
  setTimeout(function () { snapshot("timeout-500"); }, 500);
  setTimeout(function () { snapshot("timeout-2000"); }, 2000);

  postDebug("bridge-installed", {
    userAgent: safeToString((window.navigator && window.navigator.userAgent) || "")
  });
  snapshot("bridge-installed");
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
	onDebugEvent func(string),
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

	if onDebugEvent != nil {
		if err := view.Bind(webViewBridgeDebugBinding, func(raw string) {
			payload := strings.TrimSpace(raw)
			if payload == "" {
				return
			}
			onDebugEvent(payload)
		}); err != nil {
			view.Destroy()
			return nil, fmt.Errorf("bind debug bridge failed: %w", err)
		}
		host.debugf("webview: debug bridge bound")
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
