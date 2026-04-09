//go:build windows

package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

	uiInitialized := false
	showMainWindow := func(force bool) {
		if uiInitialized {
			return
		}
		uiInitialized = true
		if startMinimizedToTray && !force {
			a.hideMainWindow()
			return
		}
		a.showMainWindowFromTray()
	}

	if err := a.ensureTrayOwnerWindow(); err != nil {
		a.debugf("ui: ensureTrayOwnerWindow failed: %v", err)
		return err
	}
	a.debugf("ui: tray owner hwnd=%#x", uintptr(a.trayOwner.Handle()))

	webHost, err := newWebViewHost(
		func() {
			a.debugf("ui: webview ready callback")
			showMainWindow(false)
		},
		func(target string) {
			a.debugf("ui: external url requested: %s", target)
			_ = a.tryOpenExternalURL(target)
		},
	)
	if err != nil {
		a.debugf("ui: newWebViewHost failed: %v", err)
		return err
	}
	a.web = webHost
	a.webHwnd = webHost.HWND()
	if a.webHwnd == 0 {
		a.debugf("ui: invalid web hwnd")
		return syscall.EINVAL
	}
	a.debugf("ui: web host initialized")

	if err := a.bindUIBridge(); err != nil {
		a.debugf("ui: bindUIBridge failed: %v", err)
		return err
	}

	if err := a.web.SetTitle("Sing-box GUI"); err != nil {
		return err
	}
	if err := a.web.SetSize(900, 560, webview.HintNone); err != nil {
		return err
	}
	if err := a.web.SetSize(780, 460, webview.HintMin); err != nil {
		return err
	}

	a.applyMainWindowIcon()
	if err := a.initNotifyIcon(); err != nil {
		a.log("WARN: не удалось инициализировать иконку трея: %v", err)
		startMinimizedToTray = false
	}
	a.applyNativeDarkHints(a.systemDark)
	a.hideMainWindow()

	if err := a.web.SetHTML(uiHTML); err != nil {
		a.debugf("ui: SetHTML failed: %v", err)
		return err
	}

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
	a.debugf("ui: shutdown finished")
}

func (a *App) mainWindowHandle() win.HWND {
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
		return
	}
	win.ShowWindow(hwnd, win.SW_HIDE)
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
		return
	}

	big := loadMainHICON(int32(win.GetSystemMetrics(win.SM_CXICON)), true)
	small := loadMainHICON(int32(win.GetSystemMetrics(win.SM_CXSMICON)), false)

	// Reuse whichever icon was loaded successfully.
	if big == 0 {
		big = small
	}
	if small == 0 {
		small = big
	}

	if big == 0 && small == 0 {
		a.log("WARN: не удалось применить иконку окна")
		return
	}

	setWindowIcons(hwnd, big, small)
	if root := win.GetAncestor(hwnd, win.GA_ROOT); root != 0 && root != hwnd {
		setWindowIcons(root, big, small)
	}
	if owner := win.GetAncestor(hwnd, win.GA_ROOTOWNER); owner != 0 && owner != hwnd {
		setWindowIcons(owner, big, small)
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
}

func loadMainHICON(size int32, preferLarge bool) win.HICON {
	if size <= 0 {
		if preferLarge {
			size = int32(win.GetSystemMetrics(win.SM_CXICON))
		} else {
			size = int32(win.GetSystemMetrics(win.SM_CXSMICON))
		}
	}

	// 1) Try embedded EXE icon resource first.
	if hinstance := win.GetModuleHandle(nil); hinstance != 0 {
		if h := win.HICON(win.LoadImage(
			hinstance,
			win.MAKEINTRESOURCE(1),
			win.IMAGE_ICON,
			size,
			size,
			win.LR_DEFAULTCOLOR|win.LR_DEFAULTSIZE|win.LR_SHARED,
		)); h != 0 {
			return h
		}
	}

	// 2) Fallback to icon extracted from executable file path.
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
				return h
			}
		}
	}

	// 3) Always available system icon to avoid empty title-bar icon.
	return win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
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
	owner.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.setUICloseRequested(true)
		a.debugf("ui: tray owner closing reason=%v", reason)
	})
	a.trayOwner = owner
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
		return
	}
	win.ShowWindow(hwnd, win.SW_RESTORE)
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.BringWindowToTop(hwnd)
	win.SetForegroundWindow(hwnd)
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
