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
	"golang.org/x/sys/windows/registry"
)

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
	_ = walk.MsgBox(nil, title, message, walk.MsgBoxIconError)
}
