//go:build windows

package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultAppLanguage   = "ru"
	defaultAutoUpdateHrs = 12
	maxAutoUpdateHours   = 24 * 365
)

var defaultSingBoxEnv = map[string]string{
	"ENABLE_DEPRECATED_LEGACY_DNS_SERVERS":      "true",
	"ENABLE_DEPRECATED_MISSING_DOMAIN_RESOLVER": "true",
}

type AppConfig struct {
	// Legacy flat fields (kept for backward compatibility).
	URL         string `yaml:"url,omitempty"`
	Version     string `yaml:"version,omitempty"`
	ProfileName string `yaml:"profile_name,omitempty"`

	AutoUpdateHours      int               `yaml:"auto_update_hours,omitempty"`
	AutoStartCore        bool              `yaml:"auto_start_core,omitempty"`
	StartMinimizedToTray bool              `yaml:"start_minimized_to_tray,omitempty"`
	Language             string            `yaml:"language,omitempty"`
	CurrentProfile       string            `yaml:"current_profile,omitempty"`
	Profiles             []ConfigProfile   `yaml:"profiles,omitempty"`
	SingboxEnv           map[string]string `yaml:"singbox-env,omitempty"`
}

type appConfigPersist struct {
	AutoUpdateHours      int               `yaml:"auto_update_hours"`
	AutoStartCore        bool              `yaml:"auto_start_core"`
	StartMinimizedToTray bool              `yaml:"start_minimized_to_tray"`
	Language             string            `yaml:"language"`
	CurrentProfile       string            `yaml:"current_profile"`
	Profiles             []ConfigProfile   `yaml:"profiles"`
	SingboxEnv           map[string]string `yaml:"singbox-env,omitempty"`
}

func (c AppConfig) MarshalYAML() (interface{}, error) {
	cfg := c
	normalizeConfigProfiles(&cfg)
	return appConfigPersist{
		AutoUpdateHours:      cfg.AutoUpdateHours,
		AutoStartCore:        cfg.AutoStartCore,
		StartMinimizedToTray: cfg.StartMinimizedToTray,
		Language:             cfg.Language,
		CurrentProfile:       cfg.CurrentProfile,
		Profiles:             cfg.Profiles,
		SingboxEnv:           cfg.SingboxEnv,
	}, nil
}

type ConfigProfile struct {
	Name    string `yaml:"name" json:"name"`
	URL     string `yaml:"url" json:"url"`
	Version string `yaml:"version" json:"version"`
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
	var detect struct {
		AutoUpdateHours *int `yaml:"auto_update_hours"`
	}
	if err := yaml.Unmarshal(b, &detect); err != nil {
		return AppConfig{}, err
	}
	if detect.AutoUpdateHours == nil {
		cfg.AutoUpdateHours = defaultAutoUpdateHrs
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
		AutoUpdateHours: defaultAutoUpdateHrs,
		Language:        defaultAppLanguage,
		CurrentProfile:  "default",
		SingboxEnv:      cloneEnvMap(defaultSingBoxEnv),
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
	cfg.AutoUpdateHours = normalizeAutoUpdateHours(cfg.AutoUpdateHours)
	cfg.Language = normalizeAppLanguage(cfg.Language)
	cfg.SingboxEnv = normalizeSingboxEnv(cfg.SingboxEnv)

	if len(cfg.Profiles) == 0 {
		name := sanitizeProfileName(cfg.ProfileName)
		if name == "" {
			name = "default"
		}
		version := strings.TrimSpace(cfg.Version)
		if version == "" {
			version = "latest"
		}
		cfg.Profiles = []ConfigProfile{{
			Name:    name,
			URL:     strings.TrimSpace(cfg.URL),
			Version: version,
		}}
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

func normalizeSingboxEnv(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(raw))
	for k, v := range raw {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		normalized[key] = strings.TrimSpace(v)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func cloneEnvMap(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(raw))
	for k, v := range raw {
		cloned[k] = v
	}
	return cloned
}

func normalizeAutoUpdateHours(raw int) int {
	if raw < 0 {
		return 0
	}
	if raw > maxAutoUpdateHours {
		return maxAutoUpdateHours
	}
	return raw
}

func syncLegacyFromCurrent(cfg *AppConfig) {
	if cfg == nil {
		return
	}
	cfg.Language = normalizeAppLanguage(cfg.Language)
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

func normalizeAppLanguage(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "en":
		return "en"
	case "ru":
		return "ru"
	default:
		return defaultAppLanguage
	}
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

func applyImportURIToConfig(cfg *AppConfig, rawImport string) {
	if cfg == nil {
		return
	}

	importURI := strings.TrimSpace(rawImport)
	if importURI == "" {
		return
	}

	if resolvedURL, profileName, err := resolveSubscriptionInput(importURI); err == nil {
		applyImportToConfig(cfg, resolvedURL, profileName)
		return
	}
	setActiveProfileURL(cfg, importURI)
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
		name = generateNextProfileName(cfg.Profiles)
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
		name := strings.TrimSpace(parsed.Fragment)
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = strings.TrimSpace(decoded)
		}
		return remoteURL, name, nil

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
