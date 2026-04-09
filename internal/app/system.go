//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
)

func hideConsoleWindow() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	user32 := syscall.NewLazyDLL("user32.dll")
	showWindow := user32.NewProc("ShowWindow")

	const swHide = 0
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	_, _, _ = showWindow.Call(hwnd, uintptr(swHide))
}

func systemUIScale() float64 {
	dpi := systemDPI()
	if dpi < 96 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0
	if scale < 1.0 {
		return 1.0
	}
	if scale > 3.0 {
		return 3.0
	}
	return scale
}

func systemDPI() int {
	user32 := syscall.NewLazyDLL("user32.dll")
	getDpiForSystem := user32.NewProc("GetDpiForSystem")
	if err := user32.Load(); err == nil {
		if err := getDpiForSystem.Find(); err == nil {
			if dpi, _, _ := getDpiForSystem.Call(); dpi >= 96 && dpi <= 960 {
				return int(dpi)
			}
		}
	}

	getDC := user32.NewProc("GetDC")
	releaseDC := user32.NewProc("ReleaseDC")
	gdi32 := syscall.NewLazyDLL("gdi32.dll")
	getDeviceCaps := gdi32.NewProc("GetDeviceCaps")
	if err := user32.Load(); err == nil {
		if err := gdi32.Load(); err == nil {
			if err := getDC.Find(); err == nil {
				if err := releaseDC.Find(); err == nil {
					if err := getDeviceCaps.Find(); err == nil {
						hdc, _, _ := getDC.Call(0)
						if hdc != 0 {
							const logPixelsX = 88
							dpi, _, _ := getDeviceCaps.Call(hdc, uintptr(logPixelsX))
							_, _, _ = releaseDC.Call(0, hdc)
							if dpi >= 96 && dpi <= 960 {
								return int(dpi)
							}
						}
					}
				}
			}
		}
	}

	return 96
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = real
	}
	return filepath.Dir(exe), nil
}

func isRunningAsAdmin() bool {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("IsUserAnAdmin")
	ret, _, _ := proc.Call()
	return ret != 0
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	var escaped []string
	for _, a := range os.Args[1:] {
		escaped = append(escaped, syscall.EscapeArg(a))
	}
	params := strings.Join(escaped, " ")

	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	paramsPtr, err := syscall.UTF16PtrFromString(params)
	if err != nil {
		return err
	}

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecuteW := shell32.NewProc("ShellExecuteW")
	ret, _, callErr := shellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(paramsPtr)),
		0,
		1,
	)

	if ret <= 32 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("ShellExecuteW ret=%d: %w", ret, callErr)
		}
		return fmt.Errorf("ShellExecuteW ret=%d", ret)
	}
	return nil
}

func openURLInDefaultBrowser(rawURL string) error {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return fmt.Errorf("пустой URL")
	}

	verbPtr, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecuteW := shell32.NewProc("ShellExecuteW")
	ret, _, callErr := shellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(targetPtr)),
		0,
		0,
		1,
	)

	if ret <= 32 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("ShellExecuteW ret=%d: %w", ret, callErr)
		}
		return fmt.Errorf("ShellExecuteW ret=%d", ret)
	}
	return nil
}

func ensureSingBoxProtocolRegistration() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = real
	}

	basePath := `Software\Classes\sing-box`
	baseKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer baseKey.Close()

	if err := baseKey.SetStringValue("", "URL:sing-box Protocol"); err != nil {
		return err
	}
	if err := baseKey.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath+`\DefaultIcon`, registry.SET_VALUE)
	if err == nil {
		_ = iconKey.SetStringValue("", fmt.Sprintf(`"%s",0`, exePath))
		iconKey.Close()
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmdKey.Close()

	command := fmt.Sprintf(`"%s" "%%1"`, exePath)
	return cmdKey.SetStringValue("", command)
}

func showError(title, message string) {
	if walk.MsgBox(nil, title, message, walk.MsgBoxIconError) != 0 {
		return
	}
	titlePtr, titleErr := syscall.UTF16PtrFromString(title)
	msgPtr, msgErr := syscall.UTF16PtrFromString(message)
	if titleErr != nil || msgErr != nil {
		return
	}
	_ = win.MessageBox(0, msgPtr, titlePtr, win.MB_OK|win.MB_ICONERROR|win.MB_TOPMOST)
}
