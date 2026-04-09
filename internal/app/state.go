//go:build windows

package app

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	configFileName        = "config.yaml"
	singboxExeName        = "sing-box.exe"
	legacyRuntimeCfgName  = "config.json"
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

	gracefulStopTimeout = 4 * time.Second
	forceStopTimeout    = 2 * time.Second
	maxLogLines         = 2000
	uiScaleCacheTTL     = 30 * time.Second
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)

type App struct {
	workDir       string
	configPath    string
	singBoxPath   string
	debugLogPath  string
	startupImport string
	protoRegWarn  string

	cfgMu  sync.Mutex
	config AppConfig

	procMu            sync.Mutex
	proc              *exec.Cmd
	procStopRequested bool
	procWaitDone      chan struct{}
	procStartedAt     time.Time
	runtimeCfgMu      sync.Mutex

	runMu         sync.Mutex
	runningAction bool

	logMu      sync.Mutex
	logEntries []logEntry
	logStart   int
	nextLogID  int64

	debugMu sync.Mutex

	uiScaleMu       sync.Mutex
	cachedUIScale   float64
	cachedUIScaleAt time.Time

	instanceIPCMu sync.Mutex
	instanceMutex windows.Handle
	instanceEvent windows.Handle
	instanceStop  chan struct{}
	instanceDone  chan struct{}

	trayOwner *walk.MainWindow
	web       *webViewHost
	webHwnd   win.HWND
	ni        *walk.NotifyIcon

	autoUpdateMu   sync.Mutex
	autoUpdateStop chan struct{}
	autoUpdateWake chan struct{}

	appUpdateMu          sync.Mutex
	appUpdateChecking    bool
	appUpdateCheckedAt   time.Time
	appUpdateNextCheckAt time.Time
	appUpdateAvailable   bool
	appLatestReleaseTag  string
	appLatestReleaseURL  string

	themeWatchStop chan struct{}
	powerWatchStop chan struct{}
	systemDark     bool

	uiCloseMu        sync.Mutex
	uiCloseRequested bool

	coreDesiredMu      sync.Mutex
	coreDesiredRunning bool
}

type logEntry struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type AppState struct {
	CurrentProfile      string          `json:"current_profile"`
	Profiles            []ConfigProfile `json:"profiles"`
	Language            string          `json:"language"`
	URL                 string          `json:"url"`
	Version             string          `json:"version"`
	AutoUpdateHours     int             `json:"auto_update_hours"`
	AutoStartCore       bool            `json:"auto_start_core"`
	StartMinimizedTray  bool            `json:"start_minimized_to_tray"`
	UIScale             float64         `json:"ui_scale"`
	UptimeSeconds       int64           `json:"uptime_seconds"`
	Running             bool            `json:"running"`
	Busy                bool            `json:"busy"`
	ProtoRegWarn        string          `json:"proto_reg_warn,omitempty"`
	AppReleaseTag       string          `json:"app_release_tag,omitempty"`
	AppReleaseURL       string          `json:"app_release_url,omitempty"`
	AppUpdateAvailable  bool            `json:"app_update_available"`
	AppLatestReleaseTag string          `json:"app_latest_release_tag,omitempty"`
	AppLatestReleaseURL string          `json:"app_latest_release_url,omitempty"`
}

func (a *App) setConfig(cfg AppConfig) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	normalizeConfigProfiles(&cfg)
	a.config = cfg
}

func (a *App) getConfigSnapshot() AppConfig {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg := a.config
	normalizeConfigProfiles(&cfg)
	return cfg
}

func (a *App) persistConfig(cfg AppConfig) error {
	normalizeConfigProfiles(&cfg)
	if err := saveConfig(a.configPath, cfg); err != nil {
		return err
	}
	a.setConfig(cfg)
	a.triggerAutoUpdateReconfigure()
	return nil
}

func (a *App) uiScaleSnapshot() float64 {
	now := time.Now()

	a.uiScaleMu.Lock()
	defer a.uiScaleMu.Unlock()

	if a.cachedUIScale <= 0 || a.cachedUIScaleAt.IsZero() || now.Sub(a.cachedUIScaleAt) >= uiScaleCacheTTL {
		a.cachedUIScale = systemUIScale()
		a.cachedUIScaleAt = now
	}

	return a.cachedUIScale
}

func (a *App) snapshotState() AppState {
	cfg := a.getConfigSnapshot()
	active := activeProfileFromConfig(cfg)

	a.runMu.Lock()
	busy := a.runningAction
	a.runMu.Unlock()

	appUpdateAvailable, appLatestTag, appLatestURL := a.appUpdateSnapshot()

	return AppState{
		CurrentProfile:      cfg.CurrentProfile,
		Profiles:            append([]ConfigProfile(nil), cfg.Profiles...),
		Language:            cfg.Language,
		URL:                 active.URL,
		Version:             active.Version,
		AutoUpdateHours:     cfg.AutoUpdateHours,
		AutoStartCore:       cfg.AutoStartCore,
		StartMinimizedTray:  cfg.StartMinimizedToTray,
		UIScale:             a.uiScaleSnapshot(),
		UptimeSeconds:       a.processUptimeSeconds(),
		Running:             a.isProcessRunning(),
		Busy:                busy,
		ProtoRegWarn:        a.protoRegWarn,
		AppReleaseTag:       currentAppReleaseTag(),
		AppReleaseURL:       currentAppReleaseURL(),
		AppUpdateAvailable:  appUpdateAvailable,
		AppLatestReleaseTag: appLatestTag,
		AppLatestReleaseURL: appLatestURL,
	}
}

type StatePatch struct {
	CurrentProfile       *string `json:"current_profile"`
	Language             *string `json:"language"`
	URL                  *string `json:"url"`
	Version              *string `json:"version"`
	AutoUpdateHours      *int    `json:"auto_update_hours"`
	AutoStartCore        *bool   `json:"auto_start_core"`
	StartMinimizedToTray *bool   `json:"start_minimized_to_tray"`
}

func (a *App) applyStatePatch(p StatePatch) error {
	cfg := a.getConfigSnapshot()
	normalizeConfigProfiles(&cfg)

	if p.CurrentProfile != nil {
		name := sanitizeProfileName(*p.CurrentProfile)
		if name != "" {
			if idx := findProfileIndexByName(cfg.Profiles, name); idx >= 0 {
				cfg.CurrentProfile = cfg.Profiles[idx].Name
			} else {
				return fmt.Errorf("профиль %q не найден", name)
			}
		}
	}

	if p.Language != nil {
		cfg.Language = normalizeAppLanguage(*p.Language)
	}
	if p.AutoUpdateHours != nil {
		cfg.AutoUpdateHours = normalizeAutoUpdateHours(*p.AutoUpdateHours)
	}
	if p.AutoStartCore != nil {
		cfg.AutoStartCore = *p.AutoStartCore
	}
	if p.StartMinimizedToTray != nil {
		cfg.StartMinimizedToTray = *p.StartMinimizedToTray
	}

	idx := activeProfileIndex(&cfg)
	if idx < 0 {
		return errors.New("активный профиль не найден")
	}

	if p.URL != nil {
		cfg.Profiles[idx].URL = strings.TrimSpace(*p.URL)
	}
	if p.Version != nil {
		version := strings.TrimSpace(*p.Version)
		if version == "" {
			version = "latest"
		}
		cfg.Profiles[idx].Version = version
	}

	syncLegacyFromCurrent(&cfg)
	if err := a.persistConfig(cfg); err != nil {
		return err
	}
	return nil
}

func (a *App) createProfile(name string) error {
	cfg := a.getConfigSnapshot()
	normalizeConfigProfiles(&cfg)

	candidate := sanitizeProfileName(name)
	if candidate == "" {
		candidate = generateNextProfileName(cfg.Profiles)
	}
	candidate = makeUniqueProfileName(cfg.Profiles, candidate)

	cfg.Profiles = append(cfg.Profiles, ConfigProfile{
		Name:    candidate,
		URL:     "",
		Version: "latest",
	})
	cfg.CurrentProfile = candidate
	syncLegacyFromCurrent(&cfg)
	return a.persistConfig(cfg)
}

func (a *App) deleteProfile(name string) error {
	cfg := a.getConfigSnapshot()
	normalizeConfigProfiles(&cfg)
	if len(cfg.Profiles) <= 1 {
		return errors.New("нельзя удалить последний профиль")
	}

	target := sanitizeProfileName(name)
	if target == "" {
		target = cfg.CurrentProfile
	}
	idx := findProfileIndexByName(cfg.Profiles, target)
	if idx < 0 {
		return fmt.Errorf("профиль %q не найден", target)
	}

	cfg.Profiles = append(cfg.Profiles[:idx], cfg.Profiles[idx+1:]...)
	if len(cfg.Profiles) == 0 {
		cfg = defaultAppConfig()
	}
	if findProfileIndexByName(cfg.Profiles, cfg.CurrentProfile) < 0 {
		cfg.CurrentProfile = cfg.Profiles[0].Name
	}
	syncLegacyFromCurrent(&cfg)
	return a.persistConfig(cfg)
}

func (a *App) renameProfile(name string) error {
	cfg := a.getConfigSnapshot()
	normalizeConfigProfiles(&cfg)

	idx := activeProfileIndex(&cfg)
	if idx < 0 {
		return errors.New("активный профиль не найден")
	}

	nextName := sanitizeProfileName(name)
	if nextName == "" {
		return errors.New("имя профиля пустое")
	}

	currentName := cfg.Profiles[idx].Name
	if strings.EqualFold(currentName, nextName) {
		cfg.Profiles[idx].Name = nextName
		cfg.CurrentProfile = nextName
		syncLegacyFromCurrent(&cfg)
		return a.persistConfig(cfg)
	}

	if existingIdx := findProfileIndexByName(cfg.Profiles, nextName); existingIdx >= 0 && existingIdx != idx {
		return fmt.Errorf("профиль %q уже существует", nextName)
	}

	cfg.Profiles[idx].Name = nextName
	cfg.CurrentProfile = nextName
	syncLegacyFromCurrent(&cfg)
	return a.persistConfig(cfg)
}
