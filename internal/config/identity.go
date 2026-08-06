package config

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	machineIDPath         = "/etc/machine-id"
	dbusMachineIDPath     = "/var/lib/dbus/machine-id"
	mqttIdentityNamespace = "zimaos-monitor/mqtt-client/v1"
	deviceIdentityNS      = "zimaos-monitor/device/v1"
	identityTokenLength   = 20
)

type IdentityProvenance string

const (
	IdentityExplicit  IdentityProvenance = "explicit"
	IdentityAutomatic IdentityProvenance = "automatic"
)

type IdentityStatus struct {
	ClientIDProvenance IdentityProvenance
	DeviceIDProvenance IdentityProvenance
	Warnings           []string
}

// IdentitySource isolates host identity reads so configuration behavior is deterministic in tests.
type IdentitySource interface {
	Hostname() (string, error)
	ReadFile(path string) ([]byte, error)
}

type osIdentitySource struct{}

func (osIdentitySource) Hostname() (string, error) {
	return os.Hostname()
}

func (osIdentitySource) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func canonicalMachineID(source IdentitySource) (string, error) {
	if source == nil {
		return "", errors.New("automatic identity requires a Linux machine ID source")
	}
	for _, path := range []string{machineIDPath, dbusMachineIDPath} {
		value, err := source.ReadFile(path)
		if err != nil {
			continue
		}
		canonical, err := validateMachineID(string(value))
		if err != nil {
			return "", fmt.Errorf("invalid Linux machine ID in %s: %w", path, err)
		}
		return canonical, nil
	}
	return "", fmt.Errorf(
		"automatic identity requires a readable Linux machine ID at %s or %s; repair machine-id or configure both mqtt.client_id and device.id",
		machineIDPath,
		dbusMachineIDPath,
	)
}

func validateMachineID(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 {
		return "", errors.New("expected exactly 32 hexadecimal characters")
	}
	allZero := true
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", errors.New("expected exactly 32 hexadecimal characters")
		}
		if r != '0' {
			allZero = false
		}
	}
	if allZero {
		return "", errors.New("all-zero value is not an initialized machine identity")
	}
	return value, nil
}

func deriveClientID(machineID string) string {
	return "zm-" + deriveIdentityToken(mqttIdentityNamespace, machineID)
}

func deriveDeviceID(machineID string) string {
	return "zimaos_" + deriveIdentityToken(deviceIdentityNS, machineID)
}

func deriveIdentityToken(namespace, machineID string) string {
	digest := sha256.Sum256([]byte(namespace + "\x00" + machineID))
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])
	return strings.ToLower(encoded[:identityTokenLength])
}

func safeDiscoveryDeviceID(deviceID string) bool {
	return deviceID != "" && !strings.ContainsAny(deviceID, "/+#\x00")
}
