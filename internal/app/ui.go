//go:build windows

package app

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	webview "github.com/webview/webview_go"
	"golang.org/x/sys/windows/registry"
)

var (
	uxthemeDLL              = syscall.NewLazyDLL("uxtheme.dll")
	procSetPreferredAppMode = uxthemeDLL.NewProc("#135")
	procAllowDarkModeWindow = uxthemeDLL.NewProc("#133")
	procFlushMenuThemes     = uxthemeDLL.NewProc("#136")
	procRefreshImmersive    = uxthemeDLL.NewProc("#104")

	user32DLL             = syscall.NewLazyDLL("user32.dll")
	procSetWindowCompAttr = user32DLL.NewProc("SetWindowCompositionAttribute")
	procSetClassLongPtrW  = user32DLL.NewProc("SetClassLongPtrW")
	procSetClassLongW     = user32DLL.NewProc("SetClassLongW")
	procEnumWindows       = user32DLL.NewProc("EnumWindows")
	procGetWindowTextW    = user32DLL.NewProc("GetWindowTextW")
	procGetWindowTextLenW = user32DLL.NewProc("GetWindowTextLengthW")
	procIsWindow          = user32DLL.NewProc("IsWindow")
)

const (
	uiReadyFallbackTimeout = 5 * time.Second
	gclpHICON              = int32(-14)
	gclpHICONSM            = int32(-34)
	mainWindowMinWidth     = 780
	mainWindowMinHeight    = 460
	embeddedSyncDebounce   = 60 * time.Millisecond
)

type windowCompositionAttribData struct {
	Attrib uint32
	_      uint32
	PvData uintptr
	CbData uintptr
}

func (a *App) runUI() error {
	a.setUICloseRequested(false)
	a.debugf("ui: runUI started")
	a.debugDumpProcessWindows("runUI-enter")

	a.systemDark = detectSystemDarkTheme()
	setPreferredAppTheme(a.systemDark)
	startupCfg := a.getConfigSnapshot()
	startMinimizedToTray := startupCfg.StartMinimizedToTray && strings.TrimSpace(a.startupImport) == ""
	a.debugf("ui: systemDark=%v startMinimizedToTray=%v", a.systemDark, startMinimizedToTray)

	uiHTML, err := loadEmbeddedUIHTML()
	if err != nil {
		a.debugf("ui: loadEmbeddedUIHTML failed: %v", err)
		return err
	}
	defer a.shutdownUI()

	if err := a.ensureTrayOwnerWindow(); err != nil {
		a.debugf("ui: ensureTrayOwnerWindow failed: %v", err)
		return err
	}
	a.debugf("ui: tray owner hwnd=%#x", uintptr(a.trayOwner.Handle()))
	a.debugDumpProcessWindows("after-tray-owner")

	uiReadyNotified := make(chan struct{})
	var showMainWindowOnce sync.Once
	showMainWindow := func(force bool) {
		showMainWindowOnce.Do(func() {
			close(uiReadyNotified)
			a.debugf("ui: showMainWindow force=%v startMinimizedToTray=%v", force, startMinimizedToTray)
			if startMinimizedToTray && !force {
				a.debugf("ui: startup configured to stay minimized in tray")
				a.hideMainWindow()
				return
			}
			a.showMainWindowFromTray()
		})
	}

	webParent := win.HWND(0)
	if a.trayOwner != nil {
		webParent = a.trayOwner.Handle()
	}
	a.debugf("ui: resolved web host parent hwnd=%#x", uintptr(webParent))

	a.debugf("ui: creating web host")
	webHost, err := newWebViewHost(
		webParent,
		false,
		func() {
			a.debugf("ui: webview ready callback")
			showMainWindow(false)
		},
		func(target string) {
			a.debugf("ui: external url requested: %s", target)
			_ = a.tryOpenExternalURL(target)
		},
		nil,
		nil,
	)
	if err != nil {
		a.debugf("ui: newWebViewHost failed: %v", err)
		return err
	}
	a.web = webHost
	a.webWidget = 0
	a.webHwnd = webHost.HWND()
	if a.webHwnd == 0 {
		a.debugf("ui: invalid web hwnd")
		return syscall.EINVAL
	}
	a.debugf("ui: web host initialized")
	a.debugDumpWindowState("web-host-initial", a.webHwnd)
	a.debugDumpProcessWindows("after-web-host")
	a.debugDumpChildWindowsDetailed("after-web-host", a.mainWindowHandle())
	a.syncEmbeddedWebViewWidgetBounds("after-web-host")

	if err := a.bindUIBridge(); err != nil {
		a.debugf("ui: bindUIBridge failed: %v", err)
		return err
	}

	a.debugf("ui: configuring host window title/size")
	if err := a.web.SetTitle("Sing-box GUI"); err != nil {
		a.debugf("ui: SetTitle failed: %v", err)
		return err
	}
	if err := a.web.SetSize(900, 560, webview.HintNone); err != nil {
		a.debugf("ui: SetSize initial failed: %v", err)
		return err
	}
	if err := a.web.SetSize(780, 460, webview.HintMin); err != nil {
		a.debugf("ui: SetSize min failed: %v", err)
		return err
	}
	a.debugDumpWindowState("after-size", a.webHwnd)
	a.syncEmbeddedWebViewWidgetBounds("after-size")

	a.applyMainWindowIcon()
	if err := a.initNotifyIcon(); err != nil {
		a.log("WARN: не удалось инициализировать иконку трея: %v", err)
		startMinimizedToTray = false
	}
	a.applyNativeDarkHints(a.systemDark)
	a.hideMainWindow()

	a.debugf("ui: loading embedded HTML into webview")
	if err := a.web.SetHTML(uiHTML); err != nil {
		a.debugf("ui: SetHTML failed: %v", err)
		return err
	}
	a.debugf("ui: SetHTML completed")
	a.debugDumpWindowState("after-sethtml", a.webHwnd)
	a.debugDumpProcessWindows("after-sethtml")
	a.debugDumpChildWindowsDetailed("after-sethtml", a.mainWindowHandle())
	a.syncEmbeddedWebViewWidgetBounds("after-sethtml")
	a.scheduleEmbeddedWidgetSync("post-sethtml")

	go func() {
		select {
		case <-uiReadyNotified:
			return
		case <-time.After(uiReadyFallbackTimeout):
			a.debugf("ui: ready callback timeout after %s, forcing window show", uiReadyFallbackTimeout)
			if !a.dispatchOnUIThreadSync(func() {
				showMainWindow(true)
			}) {
				a.debugf("ui: failed to force-show window: UI thread is unavailable")
			}
		}
	}()

	if a.protoRegWarn != "" {
		a.log("WARN: не удалось зарегистрировать протокол sing-box://: %s", a.protoRegWarn)
	}
	if a.startupImport != "" {
		a.log("Получен import URI из аргумента запуска")
	}

	a.startAutoUpdateScheduler()
	a.startSystemThemeWatcher()
	a.startPowerResumeWatcher()
	a.startCoreOnStartupIfEnabled()
	a.debugf("ui: background schedulers initialized")

	if a.web != nil {
		if err := a.web.Run(); err != nil {
			a.debugf("ui: web.Run returned error: %v", err)
			return err
		}
		a.debugf("ui: web.Run exited closeRequested=%v", a.isUICloseRequested())
	}
	return nil
}

func (a *App) shutdownUI() {
	a.debugf("ui: shutdown started")
	a.setCoreDesiredRunning(false)
	a.stopEmbeddedWidgetSyncTimer()
	a.stopAutoUpdateScheduler()
	a.stopSystemThemeWatcher()
	a.stopPowerResumeWatcher()
	a.stopProcess()
	a.disposeNotifyIcon()
	a.disposeTrayOwnerWindow()

	if a.web != nil {
		a.web.Destroy()
		a.web = nil
	}
	a.webHwnd = 0
	a.webWidget = 0
	a.debugf("ui: shutdown finished")
}

func (a *App) mainWindowHandle() win.HWND {
	if a.trayOwner != nil {
		if hwnd := a.trayOwner.Handle(); hwnd != 0 {
			return hwnd
		}
	}
	if a.web != nil {
		if hwnd := a.web.HWND(); hwnd != 0 {
			a.webHwnd = hwnd
			return a.webHwnd
		}
	}
	return a.webHwnd
}

func (a *App) hideMainWindow() {
	hwnd := a.mainWindowHandle()
	if hwnd == 0 {
		a.debugf("ui: hideMainWindow skipped: hwnd=0")
		return
	}
	a.rememberMainWindowRect("hideMainWindow")
	a.debugf("ui: hideMainWindow hwnd=%#x", uintptr(hwnd))
	a.debugDumpWindowState("hideMainWindow-before", hwnd)
	win.ShowWindow(hwnd, win.SW_HIDE)
	a.debugDumpWindowState("hideMainWindow-after", hwnd)
}

func (a *App) rememberMainWindowRect(tag string) {
	hwnd := a.mainWindowHandle()
	if hwnd == 0 || !isWindowHandleValid(hwnd) {
		return
	}

	wp := win.WINDOWPLACEMENT{Length: uint32(unsafe.Sizeof(win.WINDOWPLACEMENT{}))}
	if !win.GetWindowPlacement(hwnd, &wp) {
		a.debugf("ui: remember window rect[%s] failed hwnd=%#x lastError=%d", tag, uintptr(hwnd), win.GetLastError())
		return
	}

	rect := wp.RcNormalPosition
	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	if width <= 0 || height <= 0 {
		a.debugf(
			"ui: remember window rect[%s] skipped hwnd=%#x invalid rect=(%d,%d)-(%d,%d)",
			tag,
			uintptr(hwnd),
			rect.Left,
			rect.Top,
			rect.Right,
			rect.Bottom,
		)
		return
	}

	maximized := wp.ShowCmd == win.SW_SHOWMAXIMIZED || wp.ShowCmd == win.SW_MAXIMIZE || win.IsZoomed(hwnd)

	a.windowRectMu.Lock()
	a.lastWindowRect = rect
	a.lastWindowRectOk = true
	a.lastWindowMaximized = maximized
	a.windowRectMu.Unlock()

	a.debugf(
		"ui: remember window rect[%s] hwnd=%#x rect=(%d,%d)-(%d,%d) showCmd=%d maximized=%v",
		tag,
		uintptr(hwnd),
		rect.Left,
		rect.Top,
		rect.Right,
		rect.Bottom,
		wp.ShowCmd,
		maximized,
	)
}

func (a *App) restoreMainWindowRect(tag string) {
	hwnd := a.mainWindowHandle()
	if hwnd == 0 || !isWindowHandleValid(hwnd) {
		return
	}

	a.windowRectMu.Lock()
	rect := a.lastWindowRect
	rectOk := a.lastWindowRectOk
	maximized := a.lastWindowMaximized
	a.windowRectMu.Unlock()

	if !rectOk {
		a.debugf("ui: restore window rect[%s] skipped hwnd=%#x reason=no-saved-rect", tag, uintptr(hwnd))
		return
	}

	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	if width < mainWindowMinWidth {
		width = mainWindowMinWidth
	}
	if height < mainWindowMinHeight {
		height = mainWindowMinHeight
	}
	if width <= 0 || height <= 0 {
		a.debugf(
			"ui: restore window rect[%s] skipped hwnd=%#x invalid size=%dx%d",
			tag,
			uintptr(hwnd),
			width,
			height,
		)
		return
	}

	flags := uint32(win.SWP_NOZORDER | win.SWP_NOOWNERZORDER | win.SWP_NOACTIVATE)
	if !win.SetWindowPos(hwnd, 0, rect.Left, rect.Top, width, height, flags) {
		a.debugf("ui: restore window rect[%s] SetWindowPos failed hwnd=%#x lastError=%d", tag, uintptr(hwnd), win.GetLastError())
		return
	}

	if maximized {
		win.ShowWindow(hwnd, win.SW_MAXIMIZE)
	}

	a.debugf(
		"ui: restore window rect[%s] hwnd=%#x rect=(%d,%d)-(%d,%d) size=%dx%d maximized=%v",
		tag,
		uintptr(hwnd),
		rect.Left,
		rect.Top,
		rect.Right,
		rect.Bottom,
		width,
		height,
		maximized,
	)
}

func (a *App) debugLogWebViewEvent(raw string) {
	payload := strings.TrimSpace(raw)
	if payload == "" {
		return
	}
	const maxPayloadLen = 3500
	if len(payload) > maxPayloadLen {
		payload = payload[:maxPayloadLen] + "...(truncated)"
	}
	a.debugf("webview: js-debug %s", payload)
}

func (a *App) scheduleWebViewProbe(tag string, delay time.Duration) {
	if strings.TrimSpace(tag) == "" {
		tag = "probe"
	}
	if delay < 0 {
		delay = 0
	}
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if !a.dispatchOnUIThreadSync(func() {
			a.emitWebViewProbe(tag)
		}) {
			a.debugf("ui: web probe skipped tag=%q: UI thread unavailable", tag)
		}
	}()
}

func (a *App) emitWebViewProbe(tag string) {
	if a.web == nil {
		a.debugf("ui: web probe skipped tag=%q: webview is nil", tag)
		return
	}
	snippet := fmt.Sprintf(`(function(){
  try {
    if (typeof window.__sbDebug !== "function") return;
    var body = document.body;
    var app = document.querySelector(".app");
    var bodyStyle = body && window.getComputedStyle ? window.getComputedStyle(body) : null;
    var appStyle = app && window.getComputedStyle ? window.getComputedStyle(app) : null;
    window.__sbDebug(JSON.stringify({
      kind: "go-probe",
      details: {
        tag: %q,
        readyState: String(document.readyState || ""),
        bodyClass: body ? String(body.className || "") : "",
        bodyBackground: bodyStyle ? String(bodyStyle.backgroundColor || "") : "",
        appExists: !!app,
        appDisplay: appStyle ? String(appStyle.display || "") : "",
        appVisibility: appStyle ? String(appStyle.visibility || "") : "",
        appOpacity: appStyle ? String(appStyle.opacity || "") : "",
        appClientWidth: app ? (app.clientWidth || 0) : 0,
        appClientHeight: app ? (app.clientHeight || 0) : 0,
        viewportWidth: window.innerWidth || 0,
        viewportHeight: window.innerHeight || 0,
        hasApiBridge: typeof window.__sbApiCall === "function"
      },
      href: String((window.location && window.location.href) || ""),
      ts: Date.now()
    }));
  } catch (e) {
    try {
      if (typeof window.__sbDebug === "function") {
        window.__sbDebug(JSON.stringify({
          kind: "go-probe-error",
          details: {
            tag: %q,
            message: String(e && e.message ? e.message : e)
          },
          href: String((window.location && window.location.href) || ""),
          ts: Date.now()
        }));
      }
    } catch (_) {}
  }
})();`, tag, tag)
	if err := a.web.Eval(snippet); err != nil {
		a.debugf("ui: web probe eval failed tag=%q err=%v", tag, err)
	}
}

func (a *App) stopEmbeddedWidgetSyncTimer() {
	a.embedSyncMu.Lock()
	defer a.embedSyncMu.Unlock()

	if a.embedSyncTimer != nil {
		a.embedSyncTimer.Stop()
		a.embedSyncTimer = nil
	}
	a.embedSyncTag = ""
}

func (a *App) scheduleEmbeddedWidgetSync(tag string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = "embedded-sync"
	}

	a.embedSyncMu.Lock()
	a.embedSyncTag = tag
	if a.embedSyncTimer != nil {
		a.embedSyncTimer.Stop()
	}
	a.embedSyncTimer = time.AfterFunc(embeddedSyncDebounce, func() {
		a.embedSyncMu.Lock()
		firedTag := a.embedSyncTag
		a.embedSyncTimer = nil
		a.embedSyncMu.Unlock()

		if !a.dispatchOnUIThreadSync(func() {
			a.syncEmbeddedWebViewWidgetBounds(firedTag)
		}) {
			a.debugf("ui: embedded sync skipped tag=%q: UI thread unavailable", firedTag)
		}
	})
	a.embedSyncMu.Unlock()
}

func (a *App) syncEmbeddedWebViewWidgetBounds(tag string) {
	if a.web == nil {
		return
	}

	main := a.mainWindowHandle()
	if main == 0 || !isWindowHandleValid(main) {
		a.debugf("ui: embedded sync[%s] skipped: invalid main hwnd=%#x", tag, uintptr(main))
		return
	}

	widget := a.findEmbeddedWebViewWidget(main)
	if widget == 0 {
		a.debugf("ui: embedded sync[%s] skipped: webview_widget not found under main=%#x", tag, uintptr(main))
		return
	}

	widgetParent := win.GetParent(widget)
	if widgetParent == 0 || !isWindowHandleValid(widgetParent) {
		widgetParent = main
	}

	targetHost := a.findEmbeddedContentHost(main)
	if targetHost == 0 {
		targetHost = widgetParent
	}
	liveResize := strings.Contains(tag, "size-changed-live")

	var (
		targetRect   win.RECT
		targetSource string
		ok           bool
	)
	if targetHost == widgetParent {
		ok = win.GetClientRect(widgetParent, &targetRect)
		targetSource = fmt.Sprintf("client(%#x)", uintptr(widgetParent))
	} else {
		targetRect, ok = windowRectToClientRect(targetHost, widgetParent)
		targetSource = fmt.Sprintf("host=%#x", uintptr(targetHost))
	}
	if !ok {
		a.debugf(
			"ui: embedded sync[%s] skipped: failed to resolve targetRect source=%s main=%#x widget=%#x parent=%#x",
			tag,
			targetSource,
			uintptr(main),
			uintptr(widget),
			uintptr(widgetParent),
		)
		return
	}

	width := targetRect.Right - targetRect.Left
	height := targetRect.Bottom - targetRect.Top
	if width <= 0 || height <= 0 {
		a.debugf(
			"ui: embedded sync[%s] skipped: zero target size source=%s rect=(%d,%d)-(%d,%d)",
			tag,
			targetSource,
			targetRect.Left,
			targetRect.Top,
			targetRect.Right,
			targetRect.Bottom,
		)
		return
	}

	beforeRect := win.RECT{}
	beforeVisible := win.IsWindowVisible(widget)
	if !liveResize {
		_ = win.GetWindowRect(widget, &beforeRect)
	}
	hostAffectsZOrder := targetHost != 0 &&
		targetHost != widget &&
		win.GetParent(targetHost) == widgetParent
	beforeAboveHost := true
	if hostAffectsZOrder && !liveResize {
		beforeAboveHost = isWindowAbove(widget, targetHost)
	}

	if currentRect, ok := windowRectToClientRect(widget, widgetParent); ok {
		if rectEqual(currentRect, targetRect) && beforeVisible && (liveResize || beforeAboveHost) {
			return
		}
	}

	flags := uint32(win.SWP_NOACTIVATE | win.SWP_SHOWWINDOW | win.SWP_NOOWNERZORDER)
	if !win.SetWindowPos(
		widget,
		win.HWND_TOP,
		targetRect.Left,
		targetRect.Top,
		width,
		height,
		flags,
	) {
		a.debugf(
			"ui: embedded sync[%s] SetWindowPos failed widget=%#x lastError=%d",
			tag,
			uintptr(widget),
			win.GetLastError(),
		)
		return
	}

	win.ShowWindow(widget, win.SW_SHOW)
	win.InvalidateRect(widget, nil, true)

	if liveResize {
		return
	}

	afterRect := win.RECT{}
	_ = win.GetWindowRect(widget, &afterRect)
	afterVisible := win.IsWindowVisible(widget)
	afterAboveHost := beforeAboveHost
	if hostAffectsZOrder {
		afterAboveHost = isWindowAbove(widget, targetHost)
	}
	a.debugf(
		"ui: embedded sync[%s] widget=%#x parent=%#x source=%s target=(%d,%d)-(%d,%d) before=(%d,%d)-(%d,%d) after=(%d,%d)-(%d,%d) visible:%v->%v zAboveHost:%v->%v host=%#x",
		tag,
		uintptr(widget),
		uintptr(widgetParent),
		targetSource,
		targetRect.Left,
		targetRect.Top,
		targetRect.Right,
		targetRect.Bottom,
		beforeRect.Left,
		beforeRect.Top,
		beforeRect.Right,
		beforeRect.Bottom,
		afterRect.Left,
		afterRect.Top,
		afterRect.Right,
		afterRect.Bottom,
		beforeVisible,
		afterVisible,
		beforeAboveHost,
		afterAboveHost,
		uintptr(targetHost),
	)
	if hostAffectsZOrder && !afterAboveHost {
		a.debugf(
			"ui: embedded sync[%s] warning: widget=%#x is not above host=%#x",
			tag,
			uintptr(widget),
			uintptr(targetHost),
		)
	}
}

func (a *App) findEmbeddedWebViewWidget(main win.HWND) win.HWND {
	if a.webWidget != 0 && isWindowHandleValid(a.webWidget) && strings.EqualFold(debugWindowClassName(a.webWidget), "webview_widget") {
		return a.webWidget
	}
	a.webWidget = 0

	if main == 0 || !isWindowHandleValid(main) {
		return 0
	}

	found := win.HWND(0)
	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		h := win.HWND(hwnd)
		if !isWindowHandleValid(h) {
			return 1
		}
		if strings.EqualFold(debugWindowClassName(h), "webview_widget") {
			found = h
			return 0
		}
		return 1
	})
	_ = win.EnumChildWindows(main, callback, 0)

	a.webWidget = found
	return found
}

func (a *App) findEmbeddedContentHost(main win.HWND) win.HWND {
	if main == 0 || !isWindowHandleValid(main) {
		return 0
	}

	bestVisible := win.HWND(0)
	bestVisibleArea := int64(0)
	bestAny := win.HWND(0)
	bestAnyArea := int64(0)

	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		h := win.HWND(hwnd)
		className := strings.ToLower(debugWindowClassName(h))
		if !strings.Contains(className, "walk_composite_class") {
			return 1
		}

		info, ok := a.debugCollectWindowInfo(h)
		if !ok {
			return 1
		}
		width := int64(info.Rect.Right - info.Rect.Left)
		height := int64(info.Rect.Bottom - info.Rect.Top)
		if width <= 0 || height <= 0 {
			return 1
		}
		area := width * height

		if area > bestAnyArea {
			bestAnyArea = area
			bestAny = h
		}
		if info.Visible && area > bestVisibleArea {
			bestVisibleArea = area
			bestVisible = h
		}
		return 1
	})
	_ = win.EnumChildWindows(main, callback, 0)

	if bestVisible != 0 {
		return bestVisible
	}
	return bestAny
}

func windowRectToClientRect(hwnd win.HWND, client win.HWND) (win.RECT, bool) {
	if hwnd == 0 || client == 0 {
		return win.RECT{}, false
	}
	var rect win.RECT
	if !win.GetWindowRect(hwnd, &rect) {
		return win.RECT{}, false
	}
	topLeft := win.POINT{X: rect.Left, Y: rect.Top}
	bottomRight := win.POINT{X: rect.Right, Y: rect.Bottom}
	if !win.ScreenToClient(client, &topLeft) {
		return win.RECT{}, false
	}
	if !win.ScreenToClient(client, &bottomRight) {
		return win.RECT{}, false
	}
	return win.RECT{
		Left:   topLeft.X,
		Top:    topLeft.Y,
		Right:  bottomRight.X,
		Bottom: bottomRight.Y,
	}, true
}

func rectEqual(a, b win.RECT) bool {
	return a.Left == b.Left &&
		a.Top == b.Top &&
		a.Right == b.Right &&
		a.Bottom == b.Bottom
}

func isWindowAbove(hwnd, target win.HWND) bool {
	if hwnd == 0 || target == 0 || hwnd == target {
		return false
	}
	if !isWindowHandleValid(hwnd) || !isWindowHandleValid(target) {
		return false
	}
	if win.GetParent(hwnd) != win.GetParent(target) {
		return false
	}

	for current := win.GetWindow(hwnd, win.GW_HWNDNEXT); current != 0; current = win.GetWindow(current, win.GW_HWNDNEXT) {
		if current == target {
			return true
		}
	}
	return false
}

func (a *App) dispatchOnUIThreadSync(fn func()) bool {
	if fn == nil {
		return true
	}
	if a.web == nil {
		fn()
		return true
	}

	done := make(chan struct{})
	a.web.Dispatch(func() {
		fn()
		close(done)
	})

	select {
	case <-done:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func (a *App) requestMainWindowClose() {
	a.setUICloseRequested(true)
	a.debugf("ui: close requested")

	if a.web == nil {
		return
	}

	a.web.Dispatch(func() {
		if hwnd := a.mainWindowHandle(); hwnd != 0 {
			_ = win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
		}
		a.web.Terminate()
	})
}

func (a *App) tryOpenExternalURL(rawTarget string) bool {
	if !a.shouldOpenInSystemBrowser(rawTarget) {
		return false
	}
	if err := openURLInDefaultBrowser(rawTarget); err != nil {
		a.log("WARN: не удалось открыть ссылку во внешнем браузере: %v", err)
	}
	return true
}

func (a *App) shouldOpenInSystemBrowser(rawTarget string) bool {
	target := strings.TrimSpace(rawTarget)
	if target == "" {
		return false
	}

	targetURL, err := url.Parse(target)
	if err != nil || !targetURL.IsAbs() {
		return false
	}

	scheme := strings.ToLower(strings.TrimSpace(targetURL.Scheme))
	if scheme != "http" && scheme != "https" {
		return false
	}

	return true
}

func (a *App) applyMainWindowIcon() {
	hwnd := a.mainWindowHandle()
	if hwnd == 0 {
		a.debugf("ui: applyMainWindowIcon skipped: hwnd=0")
		return
	}

	big, bigSource := loadMainHICON(int32(win.GetSystemMetrics(win.SM_CXICON)), true)
	small, smallSource := loadMainHICON(int32(win.GetSystemMetrics(win.SM_CXSMICON)), false)

	// Reuse whichever icon was loaded successfully.
	if big == 0 {
		big = small
		bigSource = smallSource
	}
	if small == 0 {
		small = big
		smallSource = bigSource
	}

	if big == 0 && small == 0 {
		a.log("WARN: не удалось применить иконку окна")
		a.debugf("ui: applyMainWindowIcon failed: no icon sources resolved")
		return
	}
	a.debugf("ui: applying icons hwnd=%#x bigSource=%q smallSource=%q", uintptr(hwnd), bigSource, smallSource)

	setWindowIcons(hwnd, big, small)
	if root := win.GetAncestor(hwnd, win.GA_ROOT); root != 0 && root != hwnd {
		setWindowIcons(root, big, small)
		a.debugf("ui: applied icons to root hwnd=%#x", uintptr(root))
	}
	if owner := win.GetAncestor(hwnd, win.GA_ROOTOWNER); owner != 0 && owner != hwnd {
		setWindowIcons(owner, big, small)
		a.debugf("ui: applied icons to root owner hwnd=%#x", uintptr(owner))
	}

	if a.trayOwner != nil {
		if icon := a.loadMainWindowIcon(); icon != nil {
			if err := a.trayOwner.SetIcon(icon); err != nil {
				a.debugf("ui: failed to refresh tray owner icon: %v", err)
			} else {
				a.debugf("ui: tray owner icon refreshed")
			}
		} else {
			a.debugf("ui: tray owner icon refresh skipped: icon source unavailable")
		}
	}
}

func setWindowIcons(hwnd win.HWND, big, small win.HICON) {
	if hwnd == 0 {
		return
	}
	if big != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, 1, uintptr(big))
	}
	if small != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, 0, uintptr(small))
	}
	setWindowClassIcons(hwnd, big, small)
}

func setWindowClassIcons(hwnd win.HWND, big, small win.HICON) {
	if hwnd == 0 {
		return
	}
	if err := user32DLL.Load(); err != nil {
		return
	}

	if err := procSetClassLongPtrW.Find(); err == nil {
		if big != 0 {
			_, _, _ = procSetClassLongPtrW.Call(uintptr(hwnd), classLongIndex(gclpHICON), uintptr(big))
		}
		if small != 0 {
			_, _, _ = procSetClassLongPtrW.Call(uintptr(hwnd), classLongIndex(gclpHICONSM), uintptr(small))
		}
		return
	}

	if err := procSetClassLongW.Find(); err == nil {
		if big != 0 {
			_, _, _ = procSetClassLongW.Call(uintptr(hwnd), classLongIndex(gclpHICON), uintptr(big))
		}
		if small != 0 {
			_, _, _ = procSetClassLongW.Call(uintptr(hwnd), classLongIndex(gclpHICONSM), uintptr(small))
		}
	}
}

func classLongIndex(index int32) uintptr {
	return uintptr(index)
}

func loadMainHICON(size int32, preferLarge bool) (win.HICON, string) {
	if size <= 0 {
		if preferLarge {
			size = int32(win.GetSystemMetrics(win.SM_CXICON))
		} else {
			size = int32(win.GetSystemMetrics(win.SM_CXSMICON))
		}
	}

	// 1) Try embedded EXE icon resource first.
	resourceCandidates := []struct {
		id    uintptr
		label string
	}{
		{2, "resource:#2"},
		{1, "resource:#1"},
	}
	if hinstance := win.GetModuleHandle(nil); hinstance != 0 {
		for _, candidate := range resourceCandidates {
			if h := win.HICON(win.LoadImage(
				hinstance,
				win.MAKEINTRESOURCE(candidate.id),
				win.IMAGE_ICON,
				size,
				size,
				win.LR_DEFAULTCOLOR|win.LR_DEFAULTSIZE|win.LR_SHARED,
			)); h != 0 {
				return h, candidate.label
			}
		}

		if ptr, convErr := syscall.UTF16PtrFromString("APPICON"); convErr == nil {
			if h := win.HICON(win.LoadImage(
				hinstance,
				ptr,
				win.IMAGE_ICON,
				size,
				size,
				win.LR_DEFAULTCOLOR|win.LR_DEFAULTSIZE|win.LR_SHARED,
			)); h != 0 {
				return h, "resource:APPICON"
			}
		}
	}

	// 2) Try sidecar icon files (portable fallback when resources are missing).
	for _, iconPath := range mainIconCandidatePaths() {
		if h := loadHICONFromFile(iconPath, size); h != 0 {
			return h, "file:" + iconPath
		}
	}

	// 3) Fallback to icon extracted from executable file path.
	if exePath, err := os.Executable(); err == nil && strings.TrimSpace(exePath) != "" {
		if real, realErr := filepath.EvalSymlinks(exePath); realErr == nil && strings.TrimSpace(real) != "" {
			exePath = real
		}
		if ptr, convErr := syscall.UTF16PtrFromString(exePath); convErr == nil {
			if h := win.HICON(win.LoadImage(
				0,
				ptr,
				win.IMAGE_ICON,
				size,
				size,
				win.LR_LOADFROMFILE|win.LR_DEFAULTSIZE,
			)); h != 0 {
				return h, "exe-path:" + exePath
			}
		}
	}

	// 4) Always available system icon to avoid empty title-bar icon.
	return win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION)), "system:IDI_APPLICATION"
}

func mainIconCandidatePaths() []string {
	paths := make([]string, 0, 6)
	seen := make(map[string]struct{}, 8)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		paths = append(paths, p)
	}

	if exePath, err := os.Executable(); err == nil && strings.TrimSpace(exePath) != "" {
		if real, realErr := filepath.EvalSymlinks(exePath); realErr == nil && strings.TrimSpace(real) != "" {
			exePath = real
		}
		exeDir := filepath.Dir(exePath)
		add(filepath.Join(exeDir, "app-icon.ico"))
		add(filepath.Join(exeDir, "build", "windows", "app-icon.ico"))
	}

	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		add(filepath.Join(cwd, "app-icon.ico"))
		add(filepath.Join(cwd, "build", "windows", "app-icon.ico"))
	}

	return paths
}

func loadHICONFromFile(path string, size int32) win.HICON {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	if _, err := os.Stat(path); err != nil {
		return 0
	}
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	return win.HICON(win.LoadImage(
		0,
		ptr,
		win.IMAGE_ICON,
		size,
		size,
		win.LR_LOADFROMFILE|win.LR_DEFAULTSIZE,
	))
}

func (a *App) loadMainWindowIcon() *walk.Icon {
	exePath, err := os.Executable()
	if err == nil && exePath != "" {
		if real, realErr := filepath.EvalSymlinks(exePath); realErr == nil && real != "" {
			exePath = real
		}
		if icon, iconErr := walk.NewIconExtractedFromFileWithSize(exePath, 0, 32); iconErr == nil && icon != nil {
			return icon
		}
	}

	for _, iconPath := range mainIconCandidatePaths() {
		if icon, iconErr := walk.NewIconFromFileWithSize(iconPath, walk.Size{Width: 32, Height: 32}); iconErr == nil && icon != nil {
			return icon
		}
	}

	if icon, err := walk.NewIconFromResourceId(2); err == nil && icon != nil {
		return icon
	}
	if icon, err := walk.NewIconFromResourceId(1); err == nil && icon != nil {
		return icon
	}
	if icon, err := walk.NewIconFromResource("APPICON"); err == nil && icon != nil {
		return icon
	}
	return walk.IconApplication()
}

func (a *App) applyNativeDarkHints(dark bool) {
	setPreferredAppTheme(dark)
	if hwnd := a.mainWindowHandle(); hwnd != 0 {
		applyWindowTheme(hwnd, dark)
	}
}

func applyWindowTheme(h win.HWND, dark bool) {
	if h == 0 {
		return
	}

	allowDarkModeForWindow(h, dark)
	setWindowCompositionDarkColors(h, dark)

	themeName := "Explorer"
	if dark {
		themeName = "DarkMode_Explorer"
	}
	if themePtr, err := syscall.UTF16PtrFromString(themeName); err == nil {
		win.SetWindowTheme(h, themePtr, nil)
	}

	setImmersiveDarkMode(h, dark)
	win.SendMessage(h, win.WM_THEMECHANGED, 0, 0)
	win.InvalidateRect(h, nil, false)
}

func setWindowCompositionDarkColors(hwnd win.HWND, dark bool) {
	if err := user32DLL.Load(); err != nil {
		return
	}
	if err := procSetWindowCompAttr.Find(); err != nil {
		return
	}
	var enabled int32
	if dark {
		enabled = 1
	}
	data := windowCompositionAttribData{
		Attrib: wcaUseDarkModeColors,
		PvData: uintptr(unsafe.Pointer(&enabled)),
		CbData: unsafe.Sizeof(enabled),
	}
	_, _, _ = procSetWindowCompAttr.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
}

func setImmersiveDarkMode(hwnd win.HWND, dark bool) {
	var value int32
	if dark {
		value = 1
	}

	dwmapi := syscall.NewLazyDLL("dwmapi.dll")
	proc := dwmapi.NewProc("DwmSetWindowAttribute")
	if err := dwmapi.Load(); err != nil {
		return
	}
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaUseImmersiveDarkMode), uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaUseImmersiveDarkModeBefore), uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))

	corner := dwmwcpRound
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaWindowCornerPreference), uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))

	caption := uint32(dwmColorDefault)
	text := uint32(dwmColorDefault)
	border := uint32(dwmColorDefault)
	if dark {
		caption = 0x00202020
		text = 0x00F3F3F3
		border = dwmColorNone
	}
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaCaptionColor), uintptr(unsafe.Pointer(&caption)), unsafe.Sizeof(caption))
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaTextColor), uintptr(unsafe.Pointer(&text)), unsafe.Sizeof(text))
	_, _, _ = proc.Call(uintptr(hwnd), uintptr(dwmwaBorderColor), uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))
}

func setPreferredAppTheme(dark bool) {
	if err := uxthemeDLL.Load(); err != nil {
		return
	}

	mode := preferredAppModeDefault
	if dark {
		mode = preferredAppModeForceDark
	}

	if err := procSetPreferredAppMode.Find(); err == nil {
		_, _, _ = procSetPreferredAppMode.Call(mode)
	}
	if err := procRefreshImmersive.Find(); err == nil {
		_, _, _ = procRefreshImmersive.Call()
	}
	if err := procFlushMenuThemes.Find(); err == nil {
		_, _, _ = procFlushMenuThemes.Call()
	}
}

func allowDarkModeForWindow(hwnd win.HWND, dark bool) {
	if err := uxthemeDLL.Load(); err != nil {
		return
	}
	if err := procAllowDarkModeWindow.Find(); err != nil {
		return
	}
	var enabled uintptr
	if dark {
		enabled = 1
	}
	_, _, _ = procAllowDarkModeWindow.Call(uintptr(hwnd), enabled)
}

func (a *App) ensureTrayOwnerWindow() error {
	if a.trayOwner != nil {
		return nil
	}
	a.debugf("ui: creating tray owner window")

	owner, err := walk.NewMainWindowWithName("singbox-gui-tray-owner")
	if err != nil {
		return err
	}
	owner.SetVisible(false)

	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{})
	layout.SetSpacing(0)
	if err := owner.SetLayout(layout); err != nil {
		owner.Dispose()
		return err
	}
	if err := owner.SetTitle("Sing-box GUI"); err != nil {
		owner.Dispose()
		return err
	}
	if err := owner.SetMinMaxSize(
		walk.Size{Width: mainWindowMinWidth, Height: mainWindowMinHeight},
		walk.Size{},
	); err != nil {
		owner.Dispose()
		return err
	}
	if icon := a.loadMainWindowIcon(); icon != nil {
		if err := owner.SetIcon(icon); err != nil {
			a.debugf("ui: failed to set tray owner icon: %v", err)
		} else {
			a.debugf("ui: tray owner icon applied")
		}
	}
	owner.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.setUICloseRequested(true)
		a.debugf("ui: tray owner closing reason=%v", reason)
		a.debugDumpWindowState("tray-owner-closing", owner.Handle())
		a.debugDumpProcessWindows("tray-owner-closing")
		if a.web != nil {
			a.web.Terminate()
		}
	})
	owner.VisibleChanged().Attach(func() {
		a.debugf("ui: tray owner visible changed visible=%v", owner.Visible())
		a.debugDumpWindowState("tray-owner-visible-changed", owner.Handle())
		if owner.Visible() {
			a.syncEmbeddedWebViewWidgetBounds("tray-owner-visible")
			a.scheduleEmbeddedWidgetSync("tray-owner-visible")
		}
	})
	owner.SizeChanged().Attach(func() {
		hwnd := owner.Handle()
		if hwnd != 0 && win.IsIconic(hwnd) {
			a.debugf("ui: tray owner minimized to taskbar; hiding to tray hwnd=%#x", uintptr(hwnd))
			a.hideMainWindow()
			return
		}
		if hwnd == 0 || !win.IsWindowVisible(hwnd) {
			return
		}
		now := time.Now()
		if a.lastLiveResizeSync.IsZero() || now.Sub(a.lastLiveResizeSync) >= 16*time.Millisecond {
			a.lastLiveResizeSync = now
			a.syncEmbeddedWebViewWidgetBounds("tray-owner-size-changed-live")
		}
		a.scheduleEmbeddedWidgetSync("tray-owner-size-changed")
	})
	a.trayOwner = owner
	a.debugf("ui: tray owner created hwnd=%#x", uintptr(owner.Handle()))
	a.debugDumpWindowState("tray-owner-created", owner.Handle())
	return nil
}

func (a *App) initNotifyIcon() error {
	if a.trayOwner == nil {
		return nil
	}
	if a.ni != nil {
		return nil
	}

	ni, err := walk.NewNotifyIcon(a.trayOwner)
	if err != nil {
		return err
	}
	if icon := a.loadMainWindowIcon(); icon != nil {
		_ = ni.SetIcon(icon)
	}
	_ = ni.SetToolTip("Sing-box GUI")
	if err := ni.SetVisible(true); err != nil {
		_ = ni.Dispose()
		return err
	}

	showAction := walk.NewAction()
	_ = showAction.SetText("Открыть")
	showAction.Triggered().Attach(func() {
		a.showMainWindowFromTray()
	})

	exitAction := walk.NewAction()
	_ = exitAction.SetText("Выход")
	exitAction.Triggered().Attach(func() {
		a.requestMainWindowClose()
	})

	_ = ni.ContextMenu().Actions().Add(showAction)
	_ = ni.ContextMenu().Actions().Add(exitAction)

	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		a.toggleMainWindowVisibilityFromTray()
	})

	a.ni = ni
	return nil
}

func (a *App) disposeNotifyIcon() {
	if a.ni == nil {
		return
	}
	_ = a.ni.Dispose()
	a.ni = nil
}

func (a *App) disposeTrayOwnerWindow() {
	if a.trayOwner == nil {
		return
	}
	a.trayOwner.Dispose()
	a.trayOwner = nil
}

func (a *App) showMainWindowFromTray() {
	// Ensure icon remains attached to the top-level host window.
	a.applyMainWindowIcon()

	hwnd := a.mainWindowHandle()
	if hwnd == 0 {
		a.debugf("ui: showMainWindowFromTray skipped: hwnd=0")
		return
	}
	a.debugf("ui: showMainWindowFromTray hwnd=%#x", uintptr(hwnd))
	a.debugDumpWindowState("showMainWindowFromTray-before", hwnd)
	win.ShowWindow(hwnd, win.SW_RESTORE)
	a.restoreMainWindowRect("showMainWindowFromTray")
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.BringWindowToTop(hwnd)
	win.SetForegroundWindow(hwnd)
	a.syncEmbeddedWebViewWidgetBounds("showMainWindowFromTray")
	a.scheduleEmbeddedWidgetSync("showMainWindowFromTray")
	a.debugDumpWindowState("showMainWindowFromTray-after", hwnd)
	a.debugDumpProcessWindows("showMainWindowFromTray-after")
	a.debugDumpChildWindowsDetailed("showMainWindowFromTray-after", hwnd)
}

func (a *App) toggleMainWindowVisibilityFromTray() {
	hwnd := a.mainWindowHandle()
	if hwnd == 0 {
		return
	}
	if win.IsWindowVisible(hwnd) {
		a.hideMainWindow()
		return
	}
	a.showMainWindowFromTray()
}

func (a *App) startCoreOnStartupIfEnabled() {
	cfg := a.getConfigSnapshot()
	if !cfg.AutoStartCore {
		return
	}
	a.setCoreDesiredRunning(true)
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := a.withRunningAction(func() error {
			if a.isProcessRunning() {
				return nil
			}
			return a.startPipeline()
		}); err != nil {
			a.setCoreDesiredRunning(false)
			a.log("WARN: автозапуск ядра не выполнен: %v", err)
		}
	}()
}

type windowDebugInfo struct {
	HWND     win.HWND
	Exists   bool
	Visible  bool
	Class    string
	Title    string
	Owner    win.HWND
	Parent   win.HWND
	Style    uint32
	ExStyle  uint32
	Rect     win.RECT
	ProcID   uint32
	ThreadID uint32
}

func (a *App) debugWindowTopologyMonitor(stop <-chan struct{}, duration time.Duration) {
	if duration <= 0 {
		return
	}

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(duration)
	defer timeout.Stop()

	lastSignature := ""
	for {
		select {
		case <-stop:
			return
		case <-timeout.C:
			return
		case <-ticker.C:
			windows := a.debugCollectProcessWindows()
			signature := debugWindowsSignature(windows)
			if signature == lastSignature {
				continue
			}
			lastSignature = signature

			a.debugf("ui: process windows topology changed count=%d", len(windows))
			for i, info := range windows {
				a.debugf("ui: process windows topology[%d] %s", i, a.debugFormatWindowInfo(info))
			}
		}
	}
}

func (a *App) debugMainWindowLifetimeMonitor(stop <-chan struct{}, duration time.Duration) {
	if duration <= 0 {
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(duration)
	defer timeout.Stop()

	var (
		initialized   bool
		lastMainState string
		lastTrayState string
	)

	for {
		select {
		case <-stop:
			return
		case <-timeout.C:
			return
		case <-ticker.C:
			mainHWND := a.mainWindowHandle()
			mainInfo, mainExists := a.debugCollectWindowInfo(mainHWND)
			mainChildren := a.debugCollectChildClassNames(mainHWND)
			mainState := fmt.Sprintf(
				"hwnd=%#x exists=%v visible=%v children=%q",
				uintptr(mainHWND),
				mainExists,
				mainExists && mainInfo.Visible,
				strings.Join(mainChildren, ","),
			)

			trayHWND := win.HWND(0)
			if a.trayOwner != nil {
				trayHWND = a.trayOwner.Handle()
			}
			trayInfo, trayExists := a.debugCollectWindowInfo(trayHWND)
			trayChildren := a.debugCollectChildClassNames(trayHWND)
			trayState := fmt.Sprintf(
				"hwnd=%#x exists=%v visible=%v children=%q",
				uintptr(trayHWND),
				trayExists,
				trayExists && trayInfo.Visible,
				strings.Join(trayChildren, ","),
			)

			if initialized && mainState == lastMainState && trayState == lastTrayState {
				continue
			}
			initialized = true
			lastMainState = mainState
			lastTrayState = trayState

			a.debugf("ui: lifetime main=%s", mainState)
			a.debugf("ui: lifetime tray=%s", trayState)
			if !mainExists {
				a.debugf("ui: lifetime detected missing main window")
				a.debugDumpProcessWindows("lifetime-main-missing")
			}
		}
	}
}

func (a *App) debugDumpProcessWindows(tag string) {
	windows := a.debugCollectProcessWindows()
	a.debugf("ui: process windows snapshot[%s]: count=%d", tag, len(windows))
	for i, info := range windows {
		a.debugf("ui: process windows snapshot[%s][%d] %s", tag, i, a.debugFormatWindowInfo(info))
	}
}

func (a *App) debugDumpWindowState(tag string, hwnd win.HWND) {
	info, ok := a.debugCollectWindowInfo(hwnd)
	if !ok {
		a.debugf("ui: window[%s]: hwnd=%#x exists=false", tag, uintptr(hwnd))
		return
	}
	a.debugf("ui: window[%s]: %s", tag, a.debugFormatWindowInfo(info))
}

func (a *App) debugDumpChildWindowsDetailed(tag string, parent win.HWND) {
	if parent == 0 || !isWindowHandleValid(parent) {
		a.debugf("ui: child windows[%s]: parent=%#x unavailable", tag, uintptr(parent))
		return
	}

	children := make([]windowDebugInfo, 0, 12)
	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		if info, ok := a.debugCollectWindowInfo(win.HWND(hwnd)); ok {
			children = append(children, info)
		}
		return 1
	})
	_ = win.EnumChildWindows(parent, callback, 0)
	sort.Slice(children, func(i, j int) bool {
		return uintptr(children[i].HWND) < uintptr(children[j].HWND)
	})

	a.debugf("ui: child windows[%s]: parent=%#x count=%d", tag, uintptr(parent), len(children))
	for i, info := range children {
		prev := win.GetWindow(info.HWND, win.GW_HWNDPREV)
		next := win.GetWindow(info.HWND, win.GW_HWNDNEXT)
		a.debugf(
			"ui: child windows[%s][%d] %s zPrev=%#x zNext=%#x",
			tag,
			i,
			a.debugFormatWindowInfo(info),
			uintptr(prev),
			uintptr(next),
		)
	}
}

func (a *App) debugCollectProcessWindows() []windowDebugInfo {
	if !ensureWindowDebugProcsReady() {
		return nil
	}

	targetPID := uint32(os.Getpid())
	windows := make([]windowDebugInfo, 0, 8)

	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		h := win.HWND(hwnd)
		var pid uint32
		tid := win.GetWindowThreadProcessId(h, &pid)
		if pid != targetPID || tid == 0 {
			return 1
		}

		if info, ok := a.debugCollectWindowInfo(h); ok {
			windows = append(windows, info)
		} else {
			windows = append(windows, windowDebugInfo{
				HWND:     h,
				Exists:   false,
				ProcID:   pid,
				ThreadID: tid,
			})
		}
		return 1
	})

	_, _, _ = procEnumWindows.Call(callback, 0)
	sort.Slice(windows, func(i, j int) bool {
		return uintptr(windows[i].HWND) < uintptr(windows[j].HWND)
	})
	return windows
}

func (a *App) debugCollectWindowInfo(hwnd win.HWND) (windowDebugInfo, bool) {
	info := windowDebugInfo{HWND: hwnd}
	if hwnd == 0 {
		return info, false
	}
	if !isWindowHandleValid(hwnd) {
		return info, false
	}

	var pid uint32
	tid := win.GetWindowThreadProcessId(hwnd, &pid)
	info.Exists = true
	info.ProcID = pid
	info.ThreadID = tid
	info.Visible = win.IsWindowVisible(hwnd)
	info.Owner = win.GetWindow(hwnd, win.GW_OWNER)
	info.Parent = win.GetParent(hwnd)
	info.Style = uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	info.ExStyle = uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	_ = win.GetWindowRect(hwnd, &info.Rect)
	info.Class = debugWindowClassName(hwnd)
	info.Title = debugWindowTitle(hwnd)
	return info, true
}

func (a *App) debugFormatWindowInfo(info windowDebugInfo) string {
	role := a.debugWindowRole(info.HWND)
	if role == "" {
		role = "other"
	}
	return fmt.Sprintf(
		"hwnd=%#x role=%s exists=%v visible=%v class=%q title=%q owner=%#x parent=%#x style=%#x exStyle=%#x pid=%d tid=%d rect=(%d,%d)-(%d,%d)",
		uintptr(info.HWND),
		role,
		info.Exists,
		info.Visible,
		info.Class,
		info.Title,
		uintptr(info.Owner),
		uintptr(info.Parent),
		info.Style,
		info.ExStyle,
		info.ProcID,
		info.ThreadID,
		info.Rect.Left,
		info.Rect.Top,
		info.Rect.Right,
		info.Rect.Bottom,
	)
}

func (a *App) debugWindowRole(hwnd win.HWND) string {
	roles := make([]string, 0, 2)
	if hwnd != 0 {
		if a.webHwnd != 0 && hwnd == a.webHwnd {
			roles = append(roles, "web-main")
		}
		if a.trayOwner != nil && hwnd == a.trayOwner.Handle() {
			roles = append(roles, "tray-owner")
		}
	}
	return strings.Join(roles, ",")
}

func debugWindowsSignature(windows []windowDebugInfo) string {
	if len(windows) == 0 {
		return "none"
	}

	var sb strings.Builder
	for i, info := range windows {
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(fmt.Sprintf(
			"%#x,%t,%#x,%#x,%#x,%#x,%d,%d,%d,%d,%s,%s",
			uintptr(info.HWND),
			info.Visible,
			uintptr(info.Owner),
			uintptr(info.Parent),
			info.Style,
			info.ExStyle,
			info.Rect.Left,
			info.Rect.Top,
			info.Rect.Right,
			info.Rect.Bottom,
			info.Class,
			info.Title,
		))
	}
	return sb.String()
}

func ensureWindowDebugProcsReady() bool {
	if err := user32DLL.Load(); err != nil {
		return false
	}
	if err := procEnumWindows.Find(); err != nil {
		return false
	}
	if err := procGetWindowTextW.Find(); err != nil {
		return false
	}
	if err := procGetWindowTextLenW.Find(); err != nil {
		return false
	}
	if err := procIsWindow.Find(); err != nil {
		return false
	}
	return true
}

func isWindowHandleValid(hwnd win.HWND) bool {
	if hwnd == 0 || !ensureWindowDebugProcsReady() {
		return false
	}
	ret, _, _ := procIsWindow.Call(uintptr(hwnd))
	return ret != 0
}

func debugWindowClassName(hwnd win.HWND) string {
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, 256)
	n, _ := win.GetClassName(hwnd, &buf[0], len(buf))
	if n <= 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func debugWindowTitle(hwnd win.HWND) string {
	if hwnd == 0 || !ensureWindowDebugProcsReady() {
		return ""
	}
	length, _, _ := procGetWindowTextLenW.Call(uintptr(hwnd))
	maxCount := int(length) + 1
	if maxCount <= 1 {
		return ""
	}

	buf := make([]uint16, maxCount)
	n, _, _ := procGetWindowTextW.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(maxCount),
	)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:int(n)])
}

func (a *App) debugCollectChildClassNames(parent win.HWND) []string {
	if parent == 0 || !isWindowHandleValid(parent) {
		return nil
	}

	children := make([]string, 0, 8)
	callback := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		h := win.HWND(hwnd)
		className := debugWindowClassName(h)
		if className == "" {
			className = "<unknown>"
		}
		children = append(children, fmt.Sprintf("%#x:%s", hwnd, className))
		return 1
	})
	_ = win.EnumChildWindows(parent, callback, 0)
	sort.Strings(children)
	return children
}

func detectSystemDarkTheme() bool {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	defer key.Close()

	v, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return v == 0
}

func (a *App) startSystemThemeWatcher() {
	if a.themeWatchStop != nil {
		return
	}
	stop := make(chan struct{})
	a.themeWatchStop = stop

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				dark := detectSystemDarkTheme()
				if dark == a.systemDark {
					continue
				}
				a.systemDark = dark
				a.applyNativeDarkHints(dark)
				if dark {
					a.log("Системная тема: Dark")
				} else {
					a.log("Системная тема: Light")
				}
			}
		}
	}()
}

func (a *App) stopSystemThemeWatcher() {
	if a.themeWatchStop == nil {
		return
	}
	close(a.themeWatchStop)
	a.themeWatchStop = nil
}
