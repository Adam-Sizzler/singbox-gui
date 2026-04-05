//go:build windows

package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
	"gopkg.in/yaml.v3"
)

const (
	configFileName        = "config.yaml"
	singboxExeName        = "sing-box.exe"
	runtimeCfgName        = "config.json"
	userAgent             = "sfw"
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
	ctrlBreakEvent        = 1

	dwmwaUseImmersiveDarkMode               = 20
	dwmwaUseImmersiveDarkModeBefore         = 19
	dwmwaWindowCornerPreference             = 33
	dwmwaBorderColor                        = 34
	dwmwaCaptionColor                       = 35
	dwmwaTextColor                          = 36
	wcaUseDarkModeColors                    = 26
	dwmwcpRound                     int32   = 2
	dwmColorDefault                         = 0xFFFFFFFF
	dwmColorNone                            = 0xFFFFFFFE
	preferredAppModeDefault         uintptr = 0
	preferredAppModeForceDark       uintptr = 2
	logFlushInterval                        = 35 * time.Millisecond
	logFlushBatchMax                        = 120
	jsURLMaxLen                             = 1800
	gracefulStopTimeout                     = 4 * time.Second
	forceStopTimeout                        = 2 * time.Second
	unifiedButtonWidth                      = 120
	unifiedButtonHeight                     = 32
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

var (
	uxthemeDLL              = syscall.NewLazyDLL("uxtheme.dll")
	procSetPreferredAppMode = uxthemeDLL.NewProc("#135")
	procAllowDarkModeWindow = uxthemeDLL.NewProc("#133")
	procFlushMenuThemes     = uxthemeDLL.NewProc("#136")
	procRefreshImmersive    = uxthemeDLL.NewProc("#104")
	user32DLL               = syscall.NewLazyDLL("user32.dll")
	procSetWindowCompAttr   = user32DLL.NewProc("SetWindowCompositionAttribute")
	procSetWindowRgn        = user32DLL.NewProc("SetWindowRgn")
	gdi32DLL                = syscall.NewLazyDLL("gdi32.dll")
	procCreateRoundRectRgn  = gdi32DLL.NewProc("CreateRoundRectRgn")
	kernel32DLL             = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole       = kernel32DLL.NewProc("AttachConsole")
	procFreeConsole         = kernel32DLL.NewProc("FreeConsole")
	procGenerateCtrlEvent   = kernel32DLL.NewProc("GenerateConsoleCtrlEvent")
	procSetCtrlHandler      = kernel32DLL.NewProc("SetConsoleCtrlHandler")
)

type windowCompositionAttribData struct {
	Attrib uint32
	_      uint32
	PvData uintptr
	CbData uintptr
}

type AppConfig struct {
	// Legacy flat fields (kept for backward compatibility).
	URL         string `yaml:"url,omitempty"`
	Version     string `yaml:"version,omitempty"`
	ProfileName string `yaml:"profile_name,omitempty"`

	CurrentProfile string          `yaml:"current_profile,omitempty"`
	Profiles       []ConfigProfile `yaml:"profiles,omitempty"`
}

type appConfigPersist struct {
	CurrentProfile string          `yaml:"current_profile"`
	Profiles       []ConfigProfile `yaml:"profiles"`
}

func (c AppConfig) MarshalYAML() (interface{}, error) {
	cfg := c
	normalizeConfigProfiles(&cfg)
	return appConfigPersist{
		CurrentProfile: cfg.CurrentProfile,
		Profiles:       cfg.Profiles,
	}, nil
}

type ConfigProfile struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Version string `yaml:"version"`
}

type App struct {
	workDir       string
	configPath    string
	singBoxPath   string
	runtimeCfg    string
	config        AppConfig
	startupImport string
	protoRegWarn  string
	cfgMu         sync.Mutex
	procMu        sync.Mutex
	runMu         sync.Mutex
	runningAction bool
	proc          *exec.Cmd

	logServerMu  sync.Mutex
	logServer    *http.Server
	logServerURL string
	logViewHTML  string

	mw               *walk.MainWindow
	settingsPanel    *walk.Composite
	logsPanel        *walk.Composite
	actionsBar       *walk.Composite
	settingsTitle    *walk.Label
	logsTitle        *walk.Label
	configURLLabel   *walk.Label
	singBoxVerLabel  *walk.Label
	profileLabel     *walk.Label
	urlEdit          *walk.LineEdit
	versionEdit      *walk.LineEdit
	profileCombo     *walk.ComboBox
	newProfileBtn    *walk.PushButton
	deleteProfileBtn *walk.PushButton
	startBtn         *walk.PushButton
	copyLogsBtn      *walk.PushButton
	logWeb           *walk.WebView

	loadingUI bool

	darkBrush      *walk.SolidColorBrush
	darkPanelBrush *walk.SolidColorBrush
	darkInputBrush *walk.SolidColorBrush

	themeWatchStop  chan struct{}
	systemDark      bool
	logLines        []string
	pendingLogLines []string
	logWebReady     bool
	logFlushTimer   *time.Timer

	procStopRequested bool
	procWaitDone      chan struct{}
}

func main() {
	if !isRunningAsAdmin() {
		if err := relaunchElevated(); err != nil {
			showError("Admin rights required", "Не удалось запросить права администратора. Запустите приложение от имени администратора.\n\n"+err.Error())
		}
		return
	}

	workDir, err := executableDir()
	if err != nil {
		showError("Startup error", "Не удалось определить рабочую директорию:\n"+err.Error())
		return
	}

	app := &App{
		workDir:     workDir,
		configPath:  filepath.Join(workDir, configFileName),
		singBoxPath: filepath.Join(workDir, singboxExeName),
		runtimeCfg:  filepath.Join(workDir, runtimeCfgName),
	}
	app.startupImport = findImportURIArg(os.Args[1:])

	if err := ensureSingBoxProtocolRegistration(); err != nil {
		app.protoRegWarn = err.Error()
	}

	cfg, err := loadOrCreateConfig(app.configPath)
	if err != nil {
		showError("Config error", "Не удалось прочитать config.yaml:\n"+err.Error())
		return
	}
	normalizeConfigProfiles(&cfg)
	if app.startupImport != "" {
		if resolvedURL, profileName, err := resolveSubscriptionInput(app.startupImport); err == nil {
			applyImportToConfig(&cfg, resolvedURL, profileName)
		} else {
			setActiveProfileURL(&cfg, app.startupImport)
		}
	}
	if err := saveConfig(app.configPath, cfg); err != nil {
		showError("Config error", "Не удалось сохранить config.yaml:\n"+err.Error())
		return
	}
	app.config = cfg

	if err := app.runUI(); err != nil {
		showError("UI error", err.Error())
	}
}

func (a *App) runUI() error {
	a.systemDark = detectSystemDarkTheme()
	setPreferredAppTheme(a.systemDark)

	decl := MainWindow{
		AssignTo: &a.mw,
		Title:    "sing-box GUI Client",
		Size:     Size{Width: 800, Height: 400},
		MinSize:  Size{Width: 800, Height: 400},
		Layout:   VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Composite{
				AssignTo: &a.settingsPanel,
				Layout:   VBox{Spacing: 6},
				Children: []Widget{
					Label{
						AssignTo: &a.settingsTitle,
						Text:     "Settings",
						Font:     Font{Family: "Segoe UI Semibold", PointSize: 11},
					},
					Composite{
						Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 8},
						Children: []Widget{
							Label{AssignTo: &a.configURLLabel, Alignment: AlignHNearVCenter, Text: "Config URL:", Font: Font{Family: "Segoe UI", PointSize: 10}},
							LineEdit{
								AssignTo:      &a.urlEdit,
								Alignment:     AlignHNearVCenter,
								Text:          activeProfileFromConfig(a.config).URL,
								StretchFactor: 1,
								MinSize:       Size{Width: 0, Height: 30},
								Font:          Font{Family: "Segoe UI", PointSize: 10},
								OnTextChanged: a.onFieldChanged,
							},
							Label{AssignTo: &a.singBoxVerLabel, Alignment: AlignHNearVCenter, Text: "sing-box version:", Font: Font{Family: "Segoe UI", PointSize: 10}},
							LineEdit{
								AssignTo:      &a.versionEdit,
								Alignment:     AlignHNearVCenter,
								Text:          activeProfileFromConfig(a.config).Version,
								StretchFactor: 1,
								MinSize:       Size{Width: 0, Height: 30},
								Font:          Font{Family: "Segoe UI", PointSize: 10},
								OnTextChanged: a.onFieldChanged,
							},
							Label{AssignTo: &a.profileLabel, Alignment: AlignHNearVCenter, Text: "Profile:", Font: Font{Family: "Segoe UI", PointSize: 10}},
							Composite{
								Alignment: AlignHNearVCenter,
								Layout:    HBox{MarginsZero: true, Spacing: 8},
								Children: []Widget{
									ComboBox{
										AssignTo:              &a.profileCombo,
										Model:                 []string{},
										Editable:              false,
										StretchFactor:         1,
										MinSize:               Size{Width: 0, Height: 30},
										Font:                  Font{Family: "Segoe UI", PointSize: 10},
										OnCurrentIndexChanged: a.onProfileSelectionChanged,
									},
									PushButton{
										AssignTo:  &a.newProfileBtn,
										Text:      "New",
										MinSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
										MaxSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
										Font:      Font{Family: "Segoe UI", PointSize: 10},
										OnClicked: a.onCreateProfileClicked,
									},
									PushButton{
										AssignTo:  &a.deleteProfileBtn,
										Text:      "Delete",
										MinSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
										MaxSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
										Font:      Font{Family: "Segoe UI", PointSize: 10},
										OnClicked: a.onDeleteProfileClicked,
									},
								},
							},
						},
					},
				},
			},
			Composite{
				AssignTo: &a.actionsBar,
				Layout:   HBox{Spacing: 8},
				Children: []Widget{
					PushButton{
						AssignTo:  &a.startBtn,
						Text:      "Start",
						MinSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
						MaxSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
						Font:      Font{Family: "Segoe UI", PointSize: 10},
						OnClicked: a.onStartClicked,
					},
					PushButton{
						AssignTo:  &a.copyLogsBtn,
						Text:      "Copy Logs",
						MinSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
						MaxSize:   Size{Width: unifiedButtonWidth, Height: unifiedButtonHeight},
						Font:      Font{Family: "Segoe UI", PointSize: 10},
						OnClicked: a.onCopyLogsClicked,
					},
					HSpacer{},
				},
			},
			Composite{
				AssignTo:      &a.logsPanel,
				Layout:        VBox{Spacing: 6},
				StretchFactor: 1,
				Children: []Widget{
					Label{
						AssignTo: &a.logsTitle,
						Text:     "Logs",
						Font:     Font{Family: "Segoe UI Semibold", PointSize: 11},
					},
					WebView{
						AssignTo:      &a.logWeb,
						StretchFactor: 1,
						OnDocumentCompleted: func(url string) {
							a.onLogWebReady()
						},
					},
				},
			},
		},
	}

	if err := decl.Create(); err != nil {
		return err
	}
	if a.logWeb != nil {
		a.logWeb.SetShortcutsEnabled(true)
		a.logWeb.SetNativeContextMenuEnabled(true)
	}
	a.applyInputStyle()
	a.applyButtonStyle()
	a.clearControlEffects()
	a.refreshProfilesUI()
	if a.urlEdit != nil {
		_ = a.urlEdit.SetFocus()
	}
	a.setupRoundedControls()
	a.updateStartStopButton(false)
	a.applyTheme(a.systemDark)
	a.startSystemThemeWatcher()

	if a.protoRegWarn != "" {
		a.log("WARN: не удалось зарегистрировать протокол sing-box://: %s", a.protoRegWarn)
	}

	if a.startupImport != "" {
		a.log("Получен import URI из аргумента запуска")
		go func() {
			time.Sleep(250 * time.Millisecond)
			a.onStartClicked()
		}()
	}

	// Re-apply non-client dark attributes after first paint.
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(250 * time.Millisecond)
			if a.mw == nil {
				continue
			}
			a.mw.Synchronize(func() {
				a.applyNativeDarkHints(a.systemDark)
				a.applyInputStyle()
				a.applyButtonStyle()
			})
		}
	}()

	a.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.stopSystemThemeWatcher()
		if a.logFlushTimer != nil {
			a.logFlushTimer.Stop()
			a.logFlushTimer = nil
		}
		a.stopLogViewServer()
		a.stopProcess()
	})

	a.mw.Run()
	return nil
}

func (a *App) onStartClicked() {
	a.runMu.Lock()
	if a.runningAction {
		a.runMu.Unlock()
		a.log("Операция уже выполняется")
		return
	}
	a.runningAction = true
	a.runMu.Unlock()

	a.startBtn.SetEnabled(false)

	go func() {
		defer func() {
			a.mw.Synchronize(func() {
				a.startBtn.SetEnabled(true)
			})
			a.runMu.Lock()
			a.runningAction = false
			a.runMu.Unlock()
		}()

		if a.isProcessRunning() {
			a.stopProcess()
			return
		}

		if err := a.startPipeline(); err != nil {
			a.log("ERROR: %v", err)
			a.updateStartStopButton(a.isProcessRunning())
			a.mw.Synchronize(func() {
				showError("Start failed", err.Error())
			})
			return
		}

		a.updateStartStopButton(true)
	}()
}

func (a *App) onCopyLogsClicked() {
	if len(a.logLines) == 0 {
		a.log("Логи пустые")
		return
	}
	text := strings.Join(a.logLines, "\r\n")
	if err := walk.Clipboard().SetText(text); err != nil {
		a.log("WARN: не удалось скопировать лог: %v", err)
		return
	}
	a.log("Лог скопирован в буфер обмена (%d строк)", len(a.logLines))
}

func (a *App) startPipeline() error {
	if !isRunningAsAdmin() {
		return errors.New("приложение запущено без прав администратора")
	}

	cfg := a.currentConfigFromUI()
	if err := validateConfig(cfg); err != nil {
		return err
	}
	active := activeProfileFromConfig(cfg)
	resolvedConfigURL, _, err := resolveSubscriptionInput(active.URL)
	if err != nil {
		return err
	}
	if active.Name != "" {
		a.log("Профиль: %s", active.Name)
	}
	if err := saveConfig(a.configPath, cfg); err != nil {
		return fmt.Errorf("не удалось сохранить %s: %w", configFileName, err)
	}
	a.log("Сохранён %s", configFileName)

	resolvedVersion, err := resolveVersion(active.Version)
	if err != nil {
		return fmt.Errorf("не удалось определить версию sing-box: %w", err)
	}

	if err := a.ensureSingBox(resolvedVersion); err != nil {
		return err
	}

	if strings.TrimSpace(resolvedConfigURL) == "" {
		if err := ensureLocalRuntimeConfig(a.runtimeCfg); err != nil {
			return err
		}
		a.log("URL не задан, использую локальный %s", runtimeCfgName)
	} else {
		if err := downloadRuntimeConfig(resolvedConfigURL, a.runtimeCfg); err != nil {
			return err
		}
		a.log("Скачан %s", runtimeCfgName)
	}

	a.stopProcess()

	if err := a.startProcess(); err != nil {
		return err
	}

	a.log("sing-box запущен")
	return nil
}

func (a *App) currentConfigFromUI() AppConfig {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()

	cfg := a.config
	normalizeConfigProfiles(&cfg)
	idx := activeProfileIndex(&cfg)
	if idx < 0 {
		idx = 0
		cfg.CurrentProfile = cfg.Profiles[idx].Name
	}

	if a.urlEdit != nil {
		cfg.Profiles[idx].URL = strings.TrimSpace(a.urlEdit.Text())
	}
	version := "latest"
	if a.versionEdit != nil {
		version = strings.TrimSpace(a.versionEdit.Text())
		if version == "" {
			version = "latest"
		}
	}
	cfg.Profiles[idx].Version = version

	syncLegacyFromCurrent(&cfg)
	a.config = cfg
	return cfg
}

func (a *App) onFieldChanged() {
	if a.loadingUI {
		return
	}

	cfg := a.currentConfigFromUI()
	idx := activeProfileIndex(&cfg)
	if idx >= 0 {
		raw := strings.TrimSpace(strings.Trim(cfg.Profiles[idx].URL, `"'`))
		if strings.HasPrefix(strings.ToLower(raw), "sing-box://") {
			if resolvedURL, profileName, err := resolveSubscriptionInput(raw); err == nil {
				applyImportToConfig(&cfg, resolvedURL, profileName)
				a.setConfigAndRefreshUI(cfg)
			}
		}
	}

	if err := saveConfig(a.configPath, cfg); err != nil {
		a.log("WARN: не удалось сохранить config: %v", err)
	}
}

func (a *App) onProfileSelectionChanged() {
	if a.loadingUI || a.profileCombo == nil {
		return
	}

	cfg := a.currentConfigFromUI()
	idx := a.profileCombo.CurrentIndex()
	if idx < 0 || idx >= len(cfg.Profiles) {
		return
	}
	cfg.CurrentProfile = cfg.Profiles[idx].Name
	syncLegacyFromCurrent(&cfg)
	a.setConfigAndRefreshUI(cfg)

	if err := saveConfig(a.configPath, cfg); err != nil {
		a.log("WARN: не удалось сохранить config: %v", err)
	}
}

func (a *App) onCreateProfileClicked() {
	cfg := a.currentConfigFromUI()

	name := generateNextProfileName(cfg.Profiles)

	if existing := findProfileIndexByName(cfg.Profiles, name); existing >= 0 {
		cfg.CurrentProfile = cfg.Profiles[existing].Name
		syncLegacyFromCurrent(&cfg)
		a.setConfigAndRefreshUI(cfg)
		a.log("Профиль уже существует: %s", cfg.CurrentProfile)
		if err := saveConfig(a.configPath, cfg); err != nil {
			a.log("WARN: не удалось сохранить config: %v", err)
		}
		return
	}

	cfg.Profiles = append(cfg.Profiles, ConfigProfile{
		Name:    name,
		URL:     "",
		Version: "latest",
	})
	cfg.CurrentProfile = name
	syncLegacyFromCurrent(&cfg)
	a.setConfigAndRefreshUI(cfg)
	a.log("Профиль создан: %s", name)
	if err := saveConfig(a.configPath, cfg); err != nil {
		a.log("WARN: не удалось сохранить config: %v", err)
	}
}

func (a *App) onDeleteProfileClicked() {
	cfg := a.currentConfigFromUI()
	if len(cfg.Profiles) <= 1 {
		showError("Profile error", "Нельзя удалить последний профиль")
		return
	}

	idx := activeProfileIndex(&cfg)
	if idx < 0 || idx >= len(cfg.Profiles) {
		return
	}
	name := cfg.Profiles[idx].Name

	result := walk.MsgBox(a.mw, "Удалить профиль", fmt.Sprintf("Удалить профиль \"%s\"?", name), walk.MsgBoxYesNo|walk.MsgBoxIconQuestion)
	if result != walk.DlgCmdYes {
		return
	}

	cfg.Profiles = append(cfg.Profiles[:idx], cfg.Profiles[idx+1:]...)
	if idx >= len(cfg.Profiles) {
		idx = len(cfg.Profiles) - 1
	}
	cfg.CurrentProfile = cfg.Profiles[idx].Name
	syncLegacyFromCurrent(&cfg)
	a.setConfigAndRefreshUI(cfg)
	a.log("Профиль удалён: %s", name)
	if err := saveConfig(a.configPath, cfg); err != nil {
		a.log("WARN: не удалось сохранить config: %v", err)
	}
}

func (a *App) refreshProfilesUI() {
	a.cfgMu.Lock()
	cfg := a.config
	a.cfgMu.Unlock()
	a.applyConfigToUI(cfg)
}

func (a *App) setConfigAndRefreshUI(cfg AppConfig) {
	normalizeConfigProfiles(&cfg)
	a.cfgMu.Lock()
	a.config = cfg
	a.cfgMu.Unlock()
	a.applyConfigToUI(cfg)
}

func (a *App) applyConfigToUI(cfg AppConfig) {
	normalizeConfigProfiles(&cfg)
	idx := activeProfileIndex(&cfg)
	if idx < 0 || idx >= len(cfg.Profiles) {
		idx = 0
	}

	names := make([]string, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		names = append(names, p.Name)
	}
	active := cfg.Profiles[idx]
	if strings.TrimSpace(active.Version) == "" {
		active.Version = "latest"
	}

	a.loadingUI = true
	defer func() { a.loadingUI = false }()

	if a.profileCombo != nil {
		_ = a.profileCombo.SetModel(names)
		_ = a.profileCombo.SetCurrentIndex(idx)
	}
	if a.deleteProfileBtn != nil {
		a.deleteProfileBtn.SetEnabled(len(cfg.Profiles) > 1)
	}
	if a.urlEdit != nil {
		a.urlEdit.SetText(active.URL)
	}
	if a.versionEdit != nil {
		a.versionEdit.SetText(active.Version)
	}
}

func (a *App) ensureSingBox(targetVersion string) error {
	installedVersion, err := detectSingBoxVersion(a.singBoxPath)
	if err != nil {
		a.log("WARN: не удалось определить текущую версию sing-box: %v", err)
	}

	if installedVersion == targetVersion {
		a.log("sing-box уже актуальный: %s", targetVersion)
		return nil
	}

	a.log("Требуется sing-box %s (текущая: %s)", targetVersion, emptyIf(installedVersion, "не найден"))
	return downloadAndInstallSingBox(targetVersion, a.singBoxPath)
}

func (a *App) startProcess() error {
	cmd := exec.Command(a.singBoxPath, "run", "-c", a.runtimeCfg, "--disable-color")
	cmd.Dir = a.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось запустить sing-box: %w", err)
	}

	waitDone := make(chan struct{})
	a.procMu.Lock()
	a.proc = cmd
	a.procStopRequested = false
	a.procWaitDone = waitDone
	a.procMu.Unlock()

	go a.pipeLogs(stdout)
	go a.pipeLogs(stderr)

	go func(c *exec.Cmd, done chan struct{}) {
		err := c.Wait()
		close(done)

		stoppedByUser := false
		a.procMu.Lock()
		if a.proc == c {
			stoppedByUser = a.procStopRequested
			a.proc = nil
			a.procStopRequested = false
			a.procWaitDone = nil
		}
		a.procMu.Unlock()
		a.updateStartStopButton(false)
		if stoppedByUser {
			a.log("sing-box остановлен")
			return
		}
		if err != nil {
			a.log("sing-box завершился с ошибкой: %v", err)
		} else {
			a.log("sing-box завершился")
		}
	}(cmd, waitDone)

	return nil
}

func (a *App) stopProcess() {
	a.procMu.Lock()
	cmd := a.proc
	waitDone := a.procWaitDone
	firstRequest := false
	if cmd != nil && cmd.Process != nil && !a.procStopRequested {
		firstRequest = true
		a.procStopRequested = true
	}
	a.procMu.Unlock()

	if cmd == nil || cmd.Process == nil {
		a.updateStartStopButton(false)
		return
	}

	if !firstRequest {
		waitForProcessExit(waitDone, gracefulStopTimeout+forceStopTimeout)
		return
	}

	a.log("Останавливаю процесс sing-box (pid=%d)", cmd.Process.Pid)
	if tryGracefulProcessStop(cmd.Process.Pid, cmd.Process) {
		if waitForProcessExit(waitDone, gracefulStopTimeout) {
			return
		}
		a.log("WARN: мягкая остановка не сработала, принудительное завершение")
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		a.log("WARN: не удалось остановить процесс: %v", err)
	}
	waitForProcessExit(waitDone, forceStopTimeout)
}

func waitForProcessExit(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func tryGracefulProcessStop(pid int, proc *os.Process) bool {
	if pid <= 0 {
		return false
	}
	if err := sendCtrlBreakToProcessGroup(pid); err == nil {
		return true
	}
	if proc != nil {
		if err := proc.Signal(os.Interrupt); err == nil {
			return true
		}
	}
	return false
}

func sendCtrlBreakToProcessGroup(pid int) error {
	if err := kernel32DLL.Load(); err != nil {
		return err
	}
	if err := procAttachConsole.Find(); err != nil {
		return err
	}
	if err := procFreeConsole.Find(); err != nil {
		return err
	}
	if err := procGenerateCtrlEvent.Find(); err != nil {
		return err
	}
	if err := procSetCtrlHandler.Find(); err != nil {
		return err
	}

	// Detach from any current console (safe for GUI processes too).
	_, _, _ = procFreeConsole.Call()

	attached, _, attachErr := procAttachConsole.Call(uintptr(uint32(pid)))
	if attached == 0 {
		return normalizeWinProcErr("AttachConsole", attachErr)
	}
	defer procFreeConsole.Call()

	_, _, _ = procSetCtrlHandler.Call(0, 1)
	defer procSetCtrlHandler.Call(0, 0)

	sent, _, sendErr := procGenerateCtrlEvent.Call(uintptr(ctrlBreakEvent), uintptr(uint32(pid)))
	if sent == 0 {
		return normalizeWinProcErr("GenerateConsoleCtrlEvent", sendErr)
	}
	return nil
}

func normalizeWinProcErr(api string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", api)
	}
	return fmt.Errorf("%s: %w", api, err)
}

func (a *App) pipeLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		a.appendLogLine(stripANSIEscape(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		a.log("log read error: %v", err)
	}
}

func (a *App) appendLogLine(line string) {
	if a.mw == nil || a.logWeb == nil {
		return
	}

	chunks := normalizeLogChunks(line)
	if len(chunks) == 0 {
		return
	}

	a.mw.Synchronize(func() {
		const maxLogLines = 4000
		for _, chunk := range chunks {
			if len(a.logLines) >= maxLogLines {
				a.logLines = a.logLines[1:]
			}
			a.logLines = append(a.logLines, chunk)
			a.pendingLogLines = append(a.pendingLogLines, chunk)
		}
		if len(a.pendingLogLines) >= logFlushBatchMax {
			a.flushPendingLogsLocked()
			return
		}
		a.scheduleLogFlushLocked()
	})
}

func normalizeLogChunks(line string) []string {
	line = strings.TrimRight(line, "\r\n \t")
	if line == "" {
		return nil
	}

	// Some IE WebView transport paths can surface escaped separators as "\n".
	line = strings.ReplaceAll(line, `\r\n`, "\n")
	line = strings.ReplaceAll(line, `\n`, "\n")
	line = strings.ReplaceAll(line, `\r`, "\n")
	line = strings.ReplaceAll(line, "\r\n", "\n")
	line = strings.ReplaceAll(line, "\r", "\n")

	raw := strings.Split(line, "\n")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimRight(part, " \t")
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func (a *App) scheduleLogFlushLocked() {
	if a.logFlushTimer != nil {
		return
	}

	a.logFlushTimer = time.AfterFunc(logFlushInterval, func() {
		if a.mw == nil {
			return
		}
		a.mw.Synchronize(func() {
			a.logFlushTimer = nil
			a.flushPendingLogsLocked()
		})
	})
}

func (a *App) flushPendingLogsLocked() {
	if len(a.pendingLogLines) == 0 {
		return
	}
	if a.logWeb == nil || !a.logWebReady {
		a.scheduleLogFlushLocked()
		return
	}

	lines := append([]string(nil), a.pendingLogLines...)
	a.pendingLogLines = nil
	if err := a.logWebAppendLines(lines); err != nil {
		a.pendingLogLines = append(lines, a.pendingLogLines...)
		a.reloadLogWeb()
	}
}

func (a *App) log(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	a.appendLogLine(line)
}

func (a *App) isProcessRunning() bool {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	return a.proc != nil && a.proc.Process != nil
}

func (a *App) updateStartStopButton(running bool) {
	if a.mw == nil || a.startBtn == nil {
		return
	}
	a.mw.Synchronize(func() {
		if running {
			a.startBtn.SetText("Stop")
		} else {
			a.startBtn.SetText("Start")
		}
	})
}

type roundedWidget interface {
	walk.Window
	SizeChanged() *walk.Event
}

func (a *App) applyButtonStyle() {
	styleFlatButton(a.startBtn)
	styleFlatButton(a.copyLogsBtn)
	styleFlatButton(a.newProfileBtn)
	styleFlatButton(a.deleteProfileBtn)
}

func (a *App) applyInputStyle() {
	// Use a single border (without client edge) so URL/Version match Profile combo visuals.
	styleThinEditControl(a.urlEdit)
	styleThinEditControl(a.versionEdit)
}

func (a *App) clearControlEffects() {
	clearWidgetEffects(a.urlEdit)
	clearWidgetEffects(a.versionEdit)
	clearWidgetEffects(a.profileCombo)
	clearWidgetEffects(a.startBtn)
	clearWidgetEffects(a.copyLogsBtn)
	clearWidgetEffects(a.newProfileBtn)
	clearWidgetEffects(a.deleteProfileBtn)
}

func (a *App) setupRoundedControls() {
	// Avoid region-clipping on input/button controls: it makes borders look heavier.
	bindRoundedRegion(a.settingsPanel, 12)
	bindRoundedRegion(a.actionsBar, 12)
	bindRoundedRegion(a.logsPanel, 12)
}

func styleFlatButton(btn *walk.PushButton) {
	if btn == nil {
		return
	}
	hwnd := btn.Handle()
	if hwnd == 0 {
		return
	}
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style |= win.BS_FLAT
	style &^= win.WS_BORDER
	_ = win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))

	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	exStyle &^= win.WS_EX_CLIENTEDGE
	_ = win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle))

	win.SetWindowPos(
		hwnd,
		0,
		0,
		0,
		0,
		0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED,
	)
}

func clearWidgetEffects(w walk.Widget) {
	if w == nil {
		return
	}
	_ = w.GraphicsEffects().Clear()
}

func styleThinEditControl(w walk.Window) {
	if w == nil {
		return
	}
	hwnd := w.Handle()
	if hwnd == 0 {
		return
	}

	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style |= win.WS_BORDER
	_ = win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))

	exStyle := uint32(win.GetWindowLong(hwnd, win.GWL_EXSTYLE))
	exStyle &^= win.WS_EX_CLIENTEDGE
	_ = win.SetWindowLong(hwnd, win.GWL_EXSTYLE, int32(exStyle))

	win.SetWindowPos(
		hwnd,
		0,
		0,
		0,
		0,
		0,
		win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED,
	)
}

func bindRoundedRegion(w roundedWidget, radius int32) {
	if w == nil || radius <= 0 {
		return
	}
	applyRoundedRegion(w.Handle(), radius)
	w.SizeChanged().Attach(func() {
		applyRoundedRegion(w.Handle(), radius)
	})
}

func applyRoundedRegion(hwnd win.HWND, radius int32) {
	if hwnd == 0 || radius <= 0 {
		return
	}
	if err := gdi32DLL.Load(); err != nil {
		return
	}
	if err := user32DLL.Load(); err != nil {
		return
	}
	if err := procCreateRoundRectRgn.Find(); err != nil {
		return
	}
	if err := procSetWindowRgn.Find(); err != nil {
		return
	}

	var rc win.RECT
	if !win.GetClientRect(hwnd, &rc) {
		return
	}
	w := rc.Right - rc.Left
	h := rc.Bottom - rc.Top
	if w <= 2 || h <= 2 {
		return
	}

	rgn, _, _ := procCreateRoundRectRgn.Call(
		0,
		0,
		uintptr(w+1),
		uintptr(h+1),
		uintptr(radius),
		uintptr(radius),
	)
	if rgn == 0 {
		return
	}
	_, _, _ = procSetWindowRgn.Call(uintptr(hwnd), rgn, uintptr(1))
}

func (a *App) applyTheme(dark bool) {
	if a.mw == nil {
		return
	}

	a.applyNativeDarkHints(dark)

	a.mw.Synchronize(func() {
		if dark {
			if err := a.ensureDarkBrushes(); err != nil {
				a.log("WARN: failed to init dark theme brushes: %v", err)
				return
			}
			a.mw.SetBackground(a.darkBrush)
			if a.settingsPanel != nil {
				a.settingsPanel.SetBackground(a.darkPanelBrush)
			}
			if a.logsPanel != nil {
				a.logsPanel.SetBackground(a.darkPanelBrush)
			}
			if a.actionsBar != nil {
				a.actionsBar.SetBackground(a.darkPanelBrush)
			}
			if a.settingsTitle != nil {
				a.settingsTitle.SetTextColor(walk.RGB(243, 243, 243))
			}
			if a.logsTitle != nil {
				a.logsTitle.SetTextColor(walk.RGB(243, 243, 243))
			}
			if a.configURLLabel != nil {
				a.configURLLabel.SetTextColor(walk.RGB(217, 217, 217))
			}
			if a.singBoxVerLabel != nil {
				a.singBoxVerLabel.SetTextColor(walk.RGB(217, 217, 217))
			}
			if a.profileLabel != nil {
				a.profileLabel.SetTextColor(walk.RGB(217, 217, 217))
			}
			if a.urlEdit != nil {
				a.urlEdit.SetBackground(a.darkInputBrush)
				a.urlEdit.SetTextColor(walk.RGB(243, 243, 243))
			}
			if a.versionEdit != nil {
				a.versionEdit.SetBackground(a.darkInputBrush)
				a.versionEdit.SetTextColor(walk.RGB(243, 243, 243))
			}
			if a.profileCombo != nil {
				a.profileCombo.SetBackground(a.darkInputBrush)
			}
			if a.startBtn != nil {
				a.startBtn.SetBackground(a.darkInputBrush)
			}
			if a.copyLogsBtn != nil {
				a.copyLogsBtn.SetBackground(a.darkInputBrush)
			}
			if a.newProfileBtn != nil {
				a.newProfileBtn.SetBackground(a.darkInputBrush)
			}
			if a.deleteProfileBtn != nil {
				a.deleteProfileBtn.SetBackground(a.darkInputBrush)
			}
			a.applyInputStyle()
			a.applyButtonStyle()
			a.reloadLogWeb()
			return
		}

		a.mw.SetBackground(nil)
		if a.settingsPanel != nil {
			a.settingsPanel.SetBackground(nil)
		}
		if a.logsPanel != nil {
			a.logsPanel.SetBackground(nil)
		}
		if a.actionsBar != nil {
			a.actionsBar.SetBackground(nil)
		}
		if a.settingsTitle != nil {
			a.settingsTitle.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.logsTitle != nil {
			a.logsTitle.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.configURLLabel != nil {
			a.configURLLabel.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.singBoxVerLabel != nil {
			a.singBoxVerLabel.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.profileLabel != nil {
			a.profileLabel.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.urlEdit != nil {
			a.urlEdit.SetBackground(nil)
			a.urlEdit.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.versionEdit != nil {
			a.versionEdit.SetBackground(nil)
			a.versionEdit.SetTextColor(walk.RGB(0, 0, 0))
		}
		if a.profileCombo != nil {
			a.profileCombo.SetBackground(nil)
		}
		if a.startBtn != nil {
			a.startBtn.SetBackground(nil)
		}
		if a.copyLogsBtn != nil {
			a.copyLogsBtn.SetBackground(nil)
		}
		if a.newProfileBtn != nil {
			a.newProfileBtn.SetBackground(nil)
		}
		if a.deleteProfileBtn != nil {
			a.deleteProfileBtn.SetBackground(nil)
		}
		a.applyInputStyle()
		a.applyButtonStyle()
		a.reloadLogWeb()
	})
}

func (a *App) applyNativeDarkHints(dark bool) {
	setPreferredAppTheme(dark)

	applyDarkTo := func(w walk.Window) {
		if w == nil {
			return
		}
		applyWindowTheme(w.Handle(), dark)
	}

	applyDarkTo(a.mw)
	applyDarkTo(a.urlEdit)
	applyDarkTo(a.versionEdit)
	applyComboBoxTheme(a.profileCombo, dark)
	applyDarkTo(a.startBtn)
	applyDarkTo(a.copyLogsBtn)
	applyDarkTo(a.newProfileBtn)
	applyDarkTo(a.deleteProfileBtn)
	applyDarkTo(a.logWeb)
}

func applyComboBoxTheme(cb *walk.ComboBox, dark bool) {
	if cb == nil {
		return
	}
	h := cb.Handle()
	if h == 0 {
		return
	}

	allowDarkModeForWindow(h, dark)
	setWindowCompositionDarkColors(h, dark)

	themeName := "CFD"
	if dark {
		themeName = "DarkMode_CFD"
	}

	themePtr, err := syscall.UTF16PtrFromString(themeName)
	if err == nil {
		win.SetWindowTheme(h, themePtr, nil)
	}

	if editHwnd := win.GetWindow(h, win.GW_CHILD); editHwnd != 0 {
		applyWindowTheme(editHwnd, dark)
	}

	win.SendMessage(h, win.WM_THEMECHANGED, 0, 0)
	win.InvalidateRect(h, nil, true)
}

func applyWindowTheme(h win.HWND, dark bool) {
	if h == 0 {
		return
	}

	allowDarkModeForWindow(h, dark)
	setWindowCompositionDarkColors(h, dark)

	var themeName string
	if dark {
		themeName = "DarkMode_Explorer"
	} else {
		themeName = "Explorer"
	}

	themePtr, err := syscall.UTF16PtrFromString(themeName)
	if err == nil {
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
	_, _, _ = procSetWindowCompAttr.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(&data)),
	)
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
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaUseImmersiveDarkMode),
		uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value),
	)
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaUseImmersiveDarkModeBefore),
		uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value),
	)
	corner := dwmwcpRound
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaWindowCornerPreference),
		uintptr(unsafe.Pointer(&corner)),
		unsafe.Sizeof(corner),
	)

	// Force caption colors for stronger title bar contrast.
	caption := uint32(dwmColorDefault)
	text := uint32(dwmColorDefault)
	border := uint32(dwmColorDefault)
	if dark {
		caption = uint32(win.RGB(6, 8, 11))
		text = uint32(win.RGB(235, 239, 247))
		border = dwmColorNone
	}
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaCaptionColor),
		uintptr(unsafe.Pointer(&caption)),
		unsafe.Sizeof(caption),
	)
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaTextColor),
		uintptr(unsafe.Pointer(&text)),
		unsafe.Sizeof(text),
	)
	_, _, _ = proc.Call(
		uintptr(hwnd),
		uintptr(dwmwaBorderColor),
		uintptr(unsafe.Pointer(&border)),
		unsafe.Sizeof(border),
	)
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

func (a *App) onLogWebReady() {
	if a.mw == nil {
		return
	}
	a.mw.Synchronize(func() {
		a.logWebReady = true
		a.flushPendingLogsLocked()
	})
}

func (a *App) reloadLogWeb() {
	if a.logWeb == nil {
		return
	}

	logsJSON, err := json.Marshal(a.logLines)
	if err != nil {
		logsJSON = []byte("[]")
	}
	initialLogsB64 := base64.RawURLEncoding.EncodeToString(logsJSON)

	bg := "#f3f3f3"
	panel := "#ffffff"
	fg := "#1f1f1f"
	border := "#d9d9d9"
	scrollTrack := "#efefef"
	scrollThumb := "#b5b5b5"
	tsColor := "#6f6f6f"
	idNumColor := "#0f7b0f"
	idTailColor := "#8a8a8a"
	durColor := "#7a7a7a"
	infoColor := "#006fc8"
	warnColor := "#8a6700"
	errorColor := "#b42318"
	debugColor := "#5e5e5e"
	traceColor := "#4f46c8"
	fatalColor := "#9f1239"
	if a.systemDark {
		bg = "#202020"
		panel = "#1e1e1e"
		fg = "#f3f3f3"
		border = "#3a3a3a"
		scrollTrack = "#262626"
		scrollThumb = "#616161"
		tsColor = "#a7a7a7"
		idNumColor = "#58d26b"
		idTailColor = "#b0b0b0"
		durColor = "#a6a6a6"
		infoColor = "#4cc2ff"
		warnColor = "#f5c451"
		errorColor = "#ff7b72"
		debugColor = "#c6c6c6"
		traceColor = "#9ab4ff"
		fatalColor = "#ff5f87"
	}

	html := fmt.Sprintf(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="X-UA-Compatible" content="IE=edge">
  <style>
    html, body {
      margin: 0;
      width: 100%%;
      height: 100%%;
      background: %s !important;
      color: %s;
      font-family: Consolas, "Courier New", monospace;
      font-size: 18px;
      line-height: 1.35;
      overflow: hidden;
    }
    body {
      scrollbar-face-color: %s;
      scrollbar-track-color: %s;
      scrollbar-arrow-color: %s;
      scrollbar-highlight-color: %s;
      scrollbar-shadow-color: %s;
      scrollbar-3dlight-color: %s;
      scrollbar-darkshadow-color: %s;
    }
    #wrap {
      box-sizing: border-box;
      width: 100%%;
      height: 100%%;
      border: 1px solid %s;
      background: %s !important;
      color: %s;
      overflow: auto;
      padding: 10px 12px;
      cursor: text;
      user-select: text;
      -ms-user-select: text;
      scrollbar-face-color: %s;
      scrollbar-track-color: %s;
      scrollbar-arrow-color: %s;
      scrollbar-highlight-color: %s;
      scrollbar-shadow-color: %s;
      scrollbar-3dlight-color: %s;
      scrollbar-darkshadow-color: %s;
    }
    .line {
      white-space: pre-wrap;
      word-break: break-word;
    }
    .ts { color: %s; }
    .id-num { color: %s; }
    .id-tail { color: %s; }
    .dur { color: %s; }
    .lvl-info { color: %s; }
    .lvl-warn { color: %s; }
    .lvl-error { color: %s; }
    .lvl-debug { color: %s; }
    .lvl-trace { color: %s; }
    .lvl-fatal { color: %s; }
  </style>
</head>
<body>
  <div id="wrap" tabindex="0"></div>
  <script>
    (function() {
      var wrap = document.getElementById('wrap');

      function decodeB64UrlUtf8(input) {
        var s = String(input || "");
        s = s.replace(/-/g, "+").replace(/_/g, "/");
        while (s.length %% 4) {
          s += "=";
        }
        var bin = window.atob(s);
        try {
          return decodeURIComponent(escape(bin));
        } catch (e) {
          return bin;
        }
      }

      function escapeHtml(str) {
        return String(str)
          .replace(/&/g, "&amp;")
          .replace(/</g, "&lt;")
          .replace(/>/g, "&gt;");
      }

      function normalizeAndSplit(rawLine) {
        var line = String(rawLine == null ? "" : rawLine);
        line = line.replace(/\\r\\n/g, "\n").replace(/\\n/g, "\n").replace(/\\r/g, "\n");
        line = line.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
        var parts = line.split("\n");
        var out = [];
        for (var i = 0; i < parts.length; i++) {
          if (parts[i].length > 0) {
            out.push(parts[i]);
          }
        }
        return out;
      }

      function levelClass(level) {
        var lvl = String(level || "").toUpperCase();
        if (lvl === "INFO") return "lvl-info";
        if (lvl === "WARN" || lvl === "WARNING") return "lvl-warn";
        if (lvl === "ERROR") return "lvl-error";
        if (lvl === "DEBUG") return "lvl-debug";
        if (lvl === "TRACE") return "lvl-trace";
        if (lvl === "FATAL") return "lvl-fatal";
        return "";
      }

      function colorizeLine(raw) {
        var s = escapeHtml(raw);
        s = s.replace(/^\[[0-9]{2}:[0-9]{2}:[0-9]{2}\]/, '<span class="ts">$&</span>');
        s = s.replace(/\b(INFO|WARN|WARNING|ERROR|DEBUG|TRACE|FATAL)\b/g, function(_, lvl) {
          var cls = levelClass(lvl);
          if (!cls) return lvl;
          return '<span class="' + cls + '">' + lvl + '</span>';
        });
        s = s.replace(/\[(\d{6,})([^\]]*)\]/g, '[<span class="id-num">$1</span><span class="id-tail">$2</span>]');
        s = s.replace(/\b(\d+(?:\.\d+)?(?:ms|s))\b/g, '<span class="dur">$1</span>');
        return s;
      }

      function appendLogs(lines) {
        if (!lines || !lines.length) {
          return;
        }
        var frag = document.createDocumentFragment();
        for (var i = 0; i < lines.length; i++) {
          var parts = normalizeAndSplit(lines[i]);
          for (var j = 0; j < parts.length; j++) {
            var div = document.createElement("div");
            div.className = "line";
            div.innerHTML = colorizeLine(parts[j]);
            frag.appendChild(div);
          }
        }
        wrap.appendChild(frag);
        wrap.scrollTop = wrap.scrollHeight;
      }

      window.__appendLogs = function(lines) {
        appendLogs(lines);
      };

      window.__appendLogsB64 = function(payloadB64) {
        if (!payloadB64) {
          return;
        }
        var decoded = decodeB64UrlUtf8(payloadB64);
        var lines;
        try {
          lines = JSON.parse(decoded);
        } catch (e) {
          return;
        }
        appendLogs(lines);
      };

      window.__setLogs = function(lines) {
        wrap.innerHTML = "";
        appendLogs(lines);
      };

      window.__setLogsB64 = function(payloadB64) {
        if (!payloadB64) {
          wrap.innerHTML = "";
          return;
        }
        var decoded = decodeB64UrlUtf8(payloadB64);
        var lines;
        try {
          lines = JSON.parse(decoded);
        } catch (e) {
          wrap.innerHTML = "";
          return;
        }
        window.__setLogs(lines);
      };

      window.__setLogsB64("%s");
      wrap.scrollTop = wrap.scrollHeight;
    })();
  </script>
</body>
</html>`,
		bg, fg,
		scrollThumb, scrollTrack, fg, scrollThumb, scrollTrack, scrollThumb, scrollTrack,
		border, panel, fg,
		scrollThumb, scrollTrack, fg, scrollThumb, scrollTrack, scrollThumb, scrollTrack,
		tsColor, idNumColor, idTailColor, durColor, infoColor, warnColor, errorColor, debugColor, traceColor, fatalColor,
		initialLogsB64,
	)

	a.logWebReady = false
	a.pendingLogLines = nil
	a.logServerMu.Lock()
	a.logViewHTML = html
	a.logServerMu.Unlock()

	logURL, err := a.ensureLogViewURL()
	if err != nil {
		a.log("WARN: не удалось подготовить встраиваемый HTML для логов: %v", err)
		return
	}
	logURL += "?ts=" + strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = a.logWeb.SetURL(logURL)
}

func (a *App) logWebAppendLines(lines []string) error {
	if a.logWeb == nil || !a.logWebReady || len(lines) == 0 {
		return nil
	}

	for start := 0; start < len(lines); {
		end := start
		for end < len(lines) {
			candidate, err := json.Marshal(lines[start : end+1])
			if err != nil {
				return err
			}
			b64 := base64.RawURLEncoding.EncodeToString(candidate)
			js := "javascript:window.__appendLogsB64(\"" + b64 + "\");void(0)"
			if len(js) > jsURLMaxLen && end > start {
				break
			}
			if len(js) > jsURLMaxLen && end == start {
				return fmt.Errorf("single log line too large for javascript URL")
			}
			end++
		}

		payload, err := json.Marshal(lines[start:end])
		if err != nil {
			return err
		}
		b64 := base64.RawURLEncoding.EncodeToString(payload)
		js := "javascript:window.__appendLogsB64(\"" + b64 + "\");void(0)"
		if err := a.logWeb.SetURL(js); err != nil {
			return err
		}
		start = end
	}

	return nil
}

func (a *App) ensureLogViewURL() (string, error) {
	a.logServerMu.Lock()
	defer a.logServerMu.Unlock()

	if a.logServerURL != "" {
		return a.logServerURL, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/log-view", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		a.logServerMu.Lock()
		html := a.logViewHTML
		a.logServerMu.Unlock()
		if html == "" {
			html = "<!doctype html><html><head><meta charset=\"utf-8\"></head><body></body></html>"
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		_, _ = io.WriteString(w, html)
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	a.logServer = srv
	a.logServerURL = "http://" + ln.Addr().String() + "/log-view"
	return a.logServerURL, nil
}

func (a *App) stopLogViewServer() {
	a.logServerMu.Lock()
	srv := a.logServer
	a.logServer = nil
	a.logServerURL = ""
	a.logViewHTML = ""
	a.logServerMu.Unlock()

	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (a *App) ensureDarkBrushes() error {
	if a.darkBrush == nil {
		// Close to native Windows Explorer dark surfaces.
		b, err := walk.NewSolidColorBrush(walk.RGB(32, 32, 32))
		if err != nil {
			return err
		}
		a.darkBrush = b
	}

	if a.darkPanelBrush == nil {
		b, err := walk.NewSolidColorBrush(walk.RGB(43, 43, 43))
		if err != nil {
			return err
		}
		a.darkPanelBrush = b
	}

	if a.darkInputBrush == nil {
		b, err := walk.NewSolidColorBrush(walk.RGB(31, 31, 31))
		if err != nil {
			return err
		}
		a.darkInputBrush = b
	}

	return nil
}

func stripANSIEscape(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
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

	// 0 = dark, 1 = light
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
				a.applyTheme(dark)
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

func loadOrCreateConfig(path string) (AppConfig, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := defaultAppConfig()
		if err := saveConfig(path, cfg); err != nil {
			return AppConfig{}, err
		}
		return cfg, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}

	var cfg AppConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return AppConfig{}, err
	}
	normalizeConfigProfiles(&cfg)
	return cfg, nil
}

func saveConfig(path string, cfg AppConfig) error {
	normalizeConfigProfiles(&cfg)
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func validateConfig(cfg AppConfig) error {
	active := activeProfileFromConfig(cfg)
	if strings.TrimSpace(active.Version) == "" {
		return errors.New("поле Version не заполнено")
	}
	if _, _, err := resolveSubscriptionInput(active.URL); err != nil {
		return err
	}
	return nil
}

func defaultAppConfig() AppConfig {
	cfg := AppConfig{
		CurrentProfile: "default",
		Profiles: []ConfigProfile{
			{
				Name:    "default",
				URL:     "",
				Version: "latest",
			},
		},
	}
	syncLegacyFromCurrent(&cfg)
	return cfg
}

func normalizeConfigProfiles(cfg *AppConfig) {
	if cfg == nil {
		return
	}

	if len(cfg.Profiles) == 0 {
		name := sanitizeProfileName(cfg.ProfileName)
		if name == "" {
			name = "default"
		}
		version := strings.TrimSpace(cfg.Version)
		if version == "" {
			version = "latest"
		}
		cfg.Profiles = []ConfigProfile{
			{
				Name:    name,
				URL:     strings.TrimSpace(cfg.URL),
				Version: version,
			},
		}
	}

	normalized := make([]ConfigProfile, 0, len(cfg.Profiles))
	for i, p := range cfg.Profiles {
		name := sanitizeProfileName(p.Name)
		if name == "" {
			if i == 0 {
				name = sanitizeProfileName(cfg.ProfileName)
			}
			if name == "" {
				name = fmt.Sprintf("profile-%d", i+1)
			}
		}
		name = makeUniqueProfileName(normalized, name)

		version := strings.TrimSpace(p.Version)
		if version == "" {
			version = "latest"
		}

		normalized = append(normalized, ConfigProfile{
			Name:    name,
			URL:     strings.TrimSpace(p.URL),
			Version: version,
		})
	}
	cfg.Profiles = normalized
	if len(cfg.Profiles) == 0 {
		*cfg = defaultAppConfig()
		return
	}

	current := sanitizeProfileName(cfg.CurrentProfile)
	if current == "" {
		current = sanitizeProfileName(cfg.ProfileName)
	}
	idx := findProfileIndexByName(cfg.Profiles, current)
	if idx < 0 {
		idx = 0
	}
	cfg.CurrentProfile = cfg.Profiles[idx].Name
	syncLegacyFromCurrent(cfg)
}

func syncLegacyFromCurrent(cfg *AppConfig) {
	if cfg == nil {
		return
	}
	if len(cfg.Profiles) == 0 {
		cfg.URL = ""
		cfg.Version = "latest"
		cfg.ProfileName = ""
		cfg.CurrentProfile = ""
		return
	}
	idx := activeProfileIndex(cfg)
	if idx < 0 || idx >= len(cfg.Profiles) {
		idx = 0
		cfg.CurrentProfile = cfg.Profiles[idx].Name
	}
	p := cfg.Profiles[idx]
	if strings.TrimSpace(p.Version) == "" {
		p.Version = "latest"
		cfg.Profiles[idx].Version = p.Version
	}
	cfg.URL = strings.TrimSpace(p.URL)
	cfg.Version = strings.TrimSpace(p.Version)
	cfg.ProfileName = p.Name
}

func activeProfileIndex(cfg *AppConfig) int {
	if cfg == nil || len(cfg.Profiles) == 0 {
		return -1
	}
	if idx := findProfileIndexByName(cfg.Profiles, cfg.CurrentProfile); idx >= 0 {
		return idx
	}
	return 0
}

func activeProfileFromConfig(cfg AppConfig) ConfigProfile {
	normalizeConfigProfiles(&cfg)
	idx := activeProfileIndex(&cfg)
	if idx < 0 || idx >= len(cfg.Profiles) {
		return ConfigProfile{Name: "default", URL: "", Version: "latest"}
	}
	return cfg.Profiles[idx]
}

func findProfileIndexByName(profiles []ConfigProfile, name string) int {
	n := sanitizeProfileName(name)
	if n == "" {
		return -1
	}
	for i := range profiles {
		if strings.EqualFold(strings.TrimSpace(profiles[i].Name), n) {
			return i
		}
	}
	return -1
}

func sanitizeProfileName(raw string) string {
	s := strings.TrimSpace(strings.Trim(raw, `"'`))
	if s == "" {
		return ""
	}
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func makeUniqueProfileName(profiles []ConfigProfile, base string) string {
	name := sanitizeProfileName(base)
	if name == "" {
		name = "profile"
	}
	if findProfileIndexByName(profiles, name) < 0 {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if findProfileIndexByName(profiles, candidate) < 0 {
			return candidate
		}
	}
}

func generateNextProfileName(profiles []ConfigProfile) string {
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("profile-%d", i)
		if findProfileIndexByName(profiles, candidate) < 0 {
			return candidate
		}
	}
}

func setActiveProfileURL(cfg *AppConfig, rawURL string) {
	if cfg == nil {
		return
	}
	normalizeConfigProfiles(cfg)
	idx := activeProfileIndex(cfg)
	if idx < 0 {
		return
	}
	cfg.Profiles[idx].URL = strings.TrimSpace(rawURL)
	syncLegacyFromCurrent(cfg)
}

func applyImportToConfig(cfg *AppConfig, resolvedURL, profileName string) {
	if cfg == nil {
		return
	}
	normalizeConfigProfiles(cfg)
	idx := activeProfileIndex(cfg)
	if idx < 0 {
		return
	}

	resolvedURL = strings.TrimSpace(strings.Trim(resolvedURL, `"'`))
	cfg.Profiles[idx].URL = resolvedURL

	name := sanitizeProfileName(profileName)
	if name == "" {
		syncLegacyFromCurrent(cfg)
		return
	}

	target := findProfileIndexByName(cfg.Profiles, name)
	if target < 0 {
		baseVersion := strings.TrimSpace(cfg.Profiles[idx].Version)
		if baseVersion == "" {
			baseVersion = "latest"
		}
		cfg.Profiles = append(cfg.Profiles, ConfigProfile{
			Name:    name,
			URL:     resolvedURL,
			Version: baseVersion,
		})
		target = len(cfg.Profiles) - 1
	} else {
		cfg.Profiles[target].URL = resolvedURL
	}
	cfg.CurrentProfile = cfg.Profiles[target].Name
	syncLegacyFromCurrent(cfg)
}

func resolveSubscriptionInput(raw string) (resolvedURL string, profileName string, err error) {
	input := strings.TrimSpace(strings.Trim(raw, `"'`))
	if input == "" {
		return "", "", nil
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return "", "", errors.New("поле URL имеет неверный формат")
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		u, err := url.ParseRequestURI(input)
		if err != nil {
			return "", "", errors.New("поле URL имеет неверный формат")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", "", errors.New("поле URL должно начинаться с http:// или https://")
		}
		return input, "", nil

	case "sing-box":
		if !strings.EqualFold(parsed.Host, "import-remote-profile") {
			return "", "", errors.New("поддерживается только sing-box://import-remote-profile")
		}
		remoteURL := strings.TrimSpace(parsed.Query().Get("url"))
		if remoteURL == "" {
			return "", "", errors.New("в import-ссылке не найден параметр url")
		}
		remoteParsed, err := url.ParseRequestURI(remoteURL)
		if err != nil || (remoteParsed.Scheme != "http" && remoteParsed.Scheme != "https") {
			return "", "", errors.New("параметр url в import-ссылке должен быть http:// или https://")
		}
		profileName := strings.TrimSpace(parsed.Fragment)
		if decoded, err := url.QueryUnescape(profileName); err == nil {
			profileName = strings.TrimSpace(decoded)
		}
		return remoteURL, profileName, nil

	default:
		return "", "", errors.New("поле URL должно быть http(s) или sing-box://import-remote-profile?...")
	}
}

func findImportURIArg(args []string) string {
	for _, raw := range args {
		s := strings.TrimSpace(strings.Trim(raw, `"'`))
		if s == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(s), "sing-box://") {
			return s
		}
	}
	return ""
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

func resolveVersion(version string) (string, error) {
	v := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if strings.EqualFold(v, "latest") || v == "" {
		latest, err := fetchLatestVersion()
		if err != nil {
			return "", err
		}
		return latest, nil
	}
	if !semverRegex.MatchString(v) {
		return "", fmt.Errorf("версия %q имеет неверный формат", version)
	}
	return v, nil
}

func fetchLatestVersion() (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/SagerNet/sing-box/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github недоступен: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github вернул HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("не удалось распарсить ответ GitHub: %w", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(body.TagName, "v"))
	if !semverRegex.MatchString(version) {
		return "", fmt.Errorf("получен некорректный tag_name: %q", body.TagName)
	}
	return version, nil
}

func detectSingBoxVersion(singboxPath string) (string, error) {
	if _, err := os.Stat(singboxPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, singboxPath, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	match := semverRegex.FindString(string(out))
	if match == "" {
		return "", fmt.Errorf("не удалось извлечь версию из вывода: %q", string(out))
	}
	return strings.TrimSpace(match), nil
}

func downloadAndInstallSingBox(version, targetExe string) error {
	downloadURL := fmt.Sprintf(
		"https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-windows-amd64.zip",
		version,
		version,
	)

	zipPath := targetExe + ".zip"

	if err := downloadFile(downloadURL, zipPath, map[string]string{"User-Agent": userAgent}); err != nil {
		return fmt.Errorf("не удалось скачать sing-box %s: %w", version, err)
	}
	defer os.Remove(zipPath)

	if err := extractSingBoxExe(zipPath, targetExe); err != nil {
		return fmt.Errorf("ошибка распаковки sing-box: %w", err)
	}

	return nil
}

func downloadRuntimeConfig(url, target string) error {
	if err := downloadFile(url, target, map[string]string{"User-Agent": userAgent}); err != nil {
		return fmt.Errorf("не удалось скачать config.json: %w", err)
	}
	return validateRuntimeConfigFile(target)
}

func ensureLocalRuntimeConfig(target string) error {
	if _, err := os.Stat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("URL не указан, а локальный config.json не найден")
		}
		return err
	}
	if err := validateRuntimeConfigFile(target); err != nil {
		return fmt.Errorf("локальный config.json не является валидным JSON: %w", err)
	}
	return nil
}

func validateRuntimeConfigFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !json.Valid(bytes.TrimSpace(b)) {
		return errors.New("config.json не является валидным JSON")
	}
	return nil
}

func downloadFile(url, target string, headers map[string]string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmpPath := target + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func extractSingBoxExe(zipPath, targetExe string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.EqualFold(filepath.Base(f.Name), singboxExeName) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			tmp := targetExe + ".tmp"
			out, err := os.Create(tmp)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, rc); err != nil {
				out.Close()
				_ = os.Remove(tmp)
				return err
			}
			if err := out.Close(); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			if err := os.Rename(tmp, targetExe); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			return nil
		}
	}
	return errors.New("sing-box.exe не найден в архиве")
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

func showError(title, message string) {
	_ = walk.MsgBox(nil, title, message, walk.MsgBoxIconError)
}

func emptyIf(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
