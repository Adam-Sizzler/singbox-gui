//go:build windows

package app

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lxn/walk"
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

	gracefulStopTimeout = 4 * time.Second
	forceStopTimeout    = 2 * time.Second
	maxLogLines         = 4000
)

var semverRegex = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?`)

type App struct {
	workDir       string
	configPath    string
	singBoxPath   string
	runtimeCfg    string
	startupImport string
	protoRegWarn  string

	cfgMu  sync.Mutex
	config AppConfig

	procMu            sync.Mutex
	proc              *exec.Cmd
	procStopRequested bool
	procWaitDone      chan struct{}

	runMu         sync.Mutex
	runningAction bool

	logMu      sync.Mutex
	logEntries []logEntry
	nextLogID  int64

	uiSrvMu   sync.Mutex
	uiServer  *http.Server
	uiBaseURL string

	instanceSrvMu sync.Mutex
	instanceSrv   *http.Server

	mw  *walk.MainWindow
	web *walk.WebView

	themeWatchStop chan struct{}
	systemDark     bool
}

type logEntry struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type AppState struct {
	CurrentProfile string          `json:"current_profile"`
	Profiles       []ConfigProfile `json:"profiles"`
	Language       string          `json:"language"`
	URL            string          `json:"url"`
	Version        string          `json:"version"`
	Running        bool            `json:"running"`
	Busy           bool            `json:"busy"`
	ProtoRegWarn   string          `json:"proto_reg_warn,omitempty"`
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
	return nil
}

func (a *App) snapshotState() AppState {
	cfg := a.getConfigSnapshot()
	active := activeProfileFromConfig(cfg)

	a.runMu.Lock()
	busy := a.runningAction
	a.runMu.Unlock()

	return AppState{
		CurrentProfile: cfg.CurrentProfile,
		Profiles:       append([]ConfigProfile(nil), cfg.Profiles...),
		Language:       cfg.Language,
		URL:            active.URL,
		Version:        active.Version,
		Running:        a.isProcessRunning(),
		Busy:           busy,
		ProtoRegWarn:   a.protoRegWarn,
	}
}

type StatePatch struct {
	CurrentProfile *string `json:"current_profile"`
	Language       *string `json:"language"`
	URL            *string `json:"url"`
	Version        *string `json:"version"`
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
