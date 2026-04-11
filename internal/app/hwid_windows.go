//go:build windows

package app

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/windows/registry"
)

var (
	hwidOnce  sync.Once
	hwidCache string
)

func appHWID() string {
	hwidOnce.Do(func() {
		hwidCache = computeHWID()
	})
	return hwidCache
}

func computeHWID() string {
	parts := []string{
		readRegistryString(`SOFTWARE\Microsoft\Cryptography`, "MachineGuid"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "BaseBoardManufacturer"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "BaseBoardProduct"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemManufacturer"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "Identifier"),
		strings.TrimSpace(os.Getenv("COMPUTERNAME")),
		"SINGBOX-GUI",
	}

	seedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		seedParts = append(seedParts, part)
	}
	if len(seedParts) == 0 {
		seedParts = append(seedParts, "SINGBOX-GUI")
		if v := strings.TrimSpace(os.Getenv("USERNAME")); v != "" {
			seedParts = append(seedParts, v)
		}
		if host, err := os.Hostname(); err == nil {
			if v := strings.TrimSpace(host); v != "" {
				seedParts = append(seedParts, v)
			}
		}
	}

	sum := md5.Sum([]byte(strings.Join(seedParts, "|")))
	encoded := strings.ToUpper(hex.EncodeToString(sum[:]))
	if len(encoded) < 32 {
		return encoded
	}
	return encoded[0:8] + "-" +
		encoded[8:12] + "-" +
		encoded[12:16] + "-" +
		encoded[16:20] + "-" +
		encoded[20:32]
}

func readRegistryString(path, name string) string {
	if value := readRegistryStringWithAccess(path, name, registry.QUERY_VALUE|registry.WOW64_64KEY); value != "" {
		return value
	}
	return readRegistryStringWithAccess(path, name, registry.QUERY_VALUE)
}

func readRegistryStringWithAccess(path, name string, access uint32) string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, access)
	if err != nil {
		return ""
	}
	defer key.Close()

	if str, _, err := key.GetStringValue(name); err == nil {
		return strings.TrimSpace(str)
	}
	if num, _, err := key.GetIntegerValue(name); err == nil {
		return strconv.FormatUint(num, 10)
	}
	if raw, _, err := key.GetBinaryValue(name); err == nil && len(raw) > 0 {
		return strings.ToUpper(hex.EncodeToString(raw))
	}
	return ""
}
