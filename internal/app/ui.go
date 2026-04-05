//go:build windows

package app

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
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
	a.systemDark = detectSystemDarkTheme()
	setPreferredAppTheme(a.systemDark)

	if err := a.startUIServer(); err != nil {
		return err
	}
	created := false
	defer func() {
		if !created {
			a.stopUIServer()
		}
	}()

	uiURL := a.uiBaseURL + "/?ts=" + strconv.FormatInt(time.Now().UnixNano(), 10)
	uiShown := false
	showMainWindow := func() {
		if uiShown || a.mw == nil {
			return
		}
		uiShown = true
		a.applyNativeDarkHints(a.systemDark)
		a.mw.Show()
		a.mw.BringToTop()
	}

	decl := MainWindow{
		AssignTo: &a.mw,
		Title:    "Sing-box GUI",
		Size:     Size{Width: 900, Height: 560},
		MinSize:  Size{Width: 780, Height: 460},
		Visible:  false,
		Background: SolidColorBrush{
			Color: walk.RGB(43, 43, 43),
		},
		Layout: VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			WebView{
				AssignTo:      &a.web,
				StretchFactor: 1,
				OnDocumentCompleted: func(string) {
					showMainWindow()
				},
				OnNavigatedError: func(*walk.WebViewNavigatedErrorEventData) {
					showMainWindow()
				},
				OnNavigating: func(eventData *walk.WebViewNavigatingEventData) {
					if eventData == nil {
						return
					}
					if a.tryOpenExternalURL(eventData.Url()) {
						eventData.SetCanceled(true)
					}
				},
				OnNewWindow: func(eventData *walk.WebViewNewWindowEventData) {
					if eventData == nil {
						return
					}
					target := strings.TrimSpace(eventData.Url())
					if target == "" {
						target = strings.TrimSpace(eventData.UrlContext())
					}
					if a.tryOpenExternalURL(target) {
						eventData.SetCanceled(true)
					}
				},
			},
		},
	}
	if err := decl.Create(); err != nil {
		return err
	}
	created = true
	a.applyMainWindowIcon()

	if a.web != nil {
		a.web.SetShortcutsEnabled(true)
		a.web.SetNativeContextMenuEnabled(true)
	}

	a.applyNativeDarkHints(a.systemDark)
	if a.web != nil {
		if err := a.web.SetURL(uiURL); err != nil {
			a.log("WARN: не удалось открыть UI страницу: %v", err)
			showMainWindow()
		}
	} else {
		showMainWindow()
	}

	if a.protoRegWarn != "" {
		a.log("WARN: не удалось зарегистрировать протокол sing-box://: %s", a.protoRegWarn)
	}
	if a.startupImport != "" {
		a.log("Получен import URI из аргумента запуска")
	}

	a.startAutoUpdateScheduler()
	a.startSystemThemeWatcher()
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(250 * time.Millisecond)
			if a.mw == nil {
				continue
			}
			a.mw.Synchronize(func() {
				a.applyNativeDarkHints(a.systemDark)
			})
		}
	}()

	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.stopAutoUpdateScheduler()
		a.stopSystemThemeWatcher()
		a.stopUIServer()
		a.stopProcess()
	})

	a.mw.Run()
	return nil
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

	if baseURL, err := url.Parse(strings.TrimSpace(a.uiBaseURL)); err == nil && baseURL.IsAbs() {
		if strings.EqualFold(targetURL.Scheme, baseURL.Scheme) && strings.EqualFold(targetURL.Host, baseURL.Host) {
			return false
		}
	}

	return true
}

func (a *App) applyMainWindowIcon() {
	if a.mw == nil {
		return
	}

	icon := a.loadMainWindowIcon()
	if icon == nil {
		a.log("WARN: не удалось применить иконку окна")
		return
	}

	if err := a.mw.SetIcon(icon); err != nil {
		a.log("WARN: не удалось установить иконку окна")
	}
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
	return nil
}

func (a *App) applyNativeDarkHints(dark bool) {
	setPreferredAppTheme(dark)
	if a.mw != nil {
		applyWindowTheme(a.mw.Handle(), dark)
	}
	if a.web != nil {
		applyWindowTheme(a.web.Handle(), dark)
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
	win.InvalidateRect(h, nil, true)
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
