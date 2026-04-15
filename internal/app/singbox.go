//go:build windows

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultDownloadTimeout = 60 * time.Second

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
	req.Header.Set("User-Agent", appUserAgent())

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

	out, err := commandWithTimeout(singboxPath, 6*time.Second, "version")
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
	if err := downloadFile(downloadURL, zipPath, map[string]string{"User-Agent": appUserAgent()}); err != nil {
		return fmt.Errorf("не удалось скачать sing-box %s: %w", version, err)
	}
	defer os.Remove(zipPath)

	if err := extractSingBoxExe(zipPath, targetExe); err != nil {
		return fmt.Errorf("ошибка распаковки sing-box: %w", err)
	}
	return nil
}

func downloadRuntimeConfig(url, target string) (bool, error) {
	return downloadRuntimeConfigWithTimeout(url, target, 0)
}

func downloadRuntimeConfigWithTimeout(url, target string, timeout time.Duration) (bool, error) {
	targetName := filepath.Base(target)
	tmpPath := target + ".download.tmp"
	if err := downloadFileWithTimeout(url, tmpPath, subscriptionRequestHeaders(), timeout); err != nil {
		return false, fmt.Errorf("не удалось скачать %s: %w", targetName, err)
	}
	defer os.Remove(tmpPath)

	if err := validateRuntimeConfigFile(tmpPath); err != nil {
		return false, fmt.Errorf("полученный %s не является валидным JSON: %w", targetName, err)
	}

	newContent, err := os.ReadFile(tmpPath)
	if err != nil {
		return false, err
	}

	oldContent, err := os.ReadFile(target)
	if err == nil {
		if bytes.Equal(bytes.TrimSpace(oldContent), bytes.TrimSpace(newContent)) {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		return false, err
	}
	return true, nil
}

func ensureLocalRuntimeConfig(target string) error {
	targetName := filepath.Base(target)
	if _, err := os.Stat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			legacyPath := filepath.Join(filepath.Dir(target), legacyRuntimeCfgName)
			if !strings.EqualFold(legacyPath, target) {
				if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
					if err := validateRuntimeConfigFile(legacyPath); err != nil {
						return fmt.Errorf("локальный %s не является валидным JSON: %w", filepath.Base(legacyPath), err)
					}
					content, err := os.ReadFile(legacyPath)
					if err != nil {
						return err
					}
					if err := os.WriteFile(target, content, 0o644); err != nil {
						return err
					}
					return nil
				}
			}
			return fmt.Errorf("URL не указан, а локальный %s не найден", targetName)
		}
		return err
	}
	if err := validateRuntimeConfigFile(target); err != nil {
		return fmt.Errorf("локальный %s не является валидным JSON: %w", targetName, err)
	}
	return nil
}

func validateRemoteRuntimeConfig(url string) error {
	return validateRemoteRuntimeConfigWithTimeout(url, 0)
}

func validateRemoteRuntimeConfigWithTimeout(url string, timeout time.Duration) error {
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("singbox-wrapper-config-check-%d.json", time.Now().UnixNano()))
	if err := downloadFileWithTimeout(url, tmpPath, subscriptionRequestHeaders(), timeout); err != nil {
		return fmt.Errorf("не удалось скачать runtime-конфиг: %w", err)
	}
	defer os.Remove(tmpPath)
	return validateRuntimeConfigFile(tmpPath)
}

func subscriptionRequestHeaders() map[string]string {
	// Keep subscription fetch behavior stable: runtime config providers are
	// expected to rely on User-Agent only.
	return map[string]string{
		"User-Agent": appUserAgent(),
	}
}

func validateRuntimeConfigFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !json.Valid(bytes.TrimSpace(b)) {
		return errors.New("конфиг не является валидным JSON")
	}
	return nil
}

func downloadFile(url, target string, headers map[string]string) error {
	return downloadFileWithTimeout(url, target, headers, 0)
}

func downloadFileWithTimeout(url, target string, headers map[string]string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultDownloadTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	dialTimeout := timeout / 2
	if dialTimeout < time.Second {
		dialTimeout = time.Second
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   dialTimeout,
			ResponseHeaderTimeout: timeout,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return fmt.Errorf("превышено время ожидания (%s)", timeout.Round(time.Second))
		}
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
