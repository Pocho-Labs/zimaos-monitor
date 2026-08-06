package config

import (
	"errors"
	"strings"
	"testing"
)

type fakeIdentitySource struct {
	host  string
	files map[string]string
	errs  map[string]error
	reads []string
}

func (f *fakeIdentitySource) Hostname() (string, error) {
	return f.host, nil
}

func (f *fakeIdentitySource) ReadFile(path string) ([]byte, error) {
	f.reads = append(f.reads, path)
	if err := f.errs[path]; err != nil {
		return nil, err
	}
	value, ok := f.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(value), nil
}

func TestCanonicalMachineID(t *testing.T) {
	const valid = "0123456789ABCDEF0123456789abcdef"
	tests := []struct {
		name      string
		source    *fakeIdentitySource
		want      string
		wantErr   bool
		wantReads []string
	}{
		{
			name: "canonical path",
			source: &fakeIdentitySource{files: map[string]string{
				machineIDPath: "  " + valid + "\n",
			}},
			want:      strings.ToLower(valid),
			wantReads: []string{machineIDPath},
		},
		{
			name: "dbus fallback",
			source: &fakeIdentitySource{files: map[string]string{
				dbusMachineIDPath: valid,
			}},
			want:      strings.ToLower(valid),
			wantReads: []string{machineIDPath, dbusMachineIDPath},
		},
		{
			name: "invalid canonical does not fall through",
			source: &fakeIdentitySource{files: map[string]string{
				machineIDPath:     "short",
				dbusMachineIDPath: valid,
			}},
			wantErr:   true,
			wantReads: []string{machineIDPath},
		},
		{
			name: "all zero",
			source: &fakeIdentitySource{files: map[string]string{
				machineIDPath: strings.Repeat("0", 32),
			}},
			wantErr:   true,
			wantReads: []string{machineIDPath},
		},
		{
			name:    "unavailable",
			source:  &fakeIdentitySource{},
			wantErr: true,
			wantReads: []string{
				machineIDPath,
				dbusMachineIDPath,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalMachineID(tt.source)
			if (err != nil) != tt.wantErr {
				t.Fatalf("canonicalMachineID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("canonicalMachineID() = %q, want %q", got, tt.want)
			}
			if strings.Join(tt.source.reads, ",") != strings.Join(tt.wantReads, ",") {
				t.Fatalf("reads = %#v, want %#v", tt.source.reads, tt.wantReads)
			}
		})
	}
}

func TestAutomaticIdentityContract(t *testing.T) {
	const machineA = "0123456789abcdef0123456789abcdef"
	const machineB = "fedcba9876543210fedcba9876543210"

	clientA := deriveClientID(machineA)
	deviceA := deriveDeviceID(machineA)
	if len(clientA) != 23 || !strings.HasPrefix(clientA, "zm-") {
		t.Fatalf("client ID = %q, want 23 characters with zm- prefix", clientA)
	}
	if len(deviceA) != 27 || !strings.HasPrefix(deviceA, "zimaos_") {
		t.Fatalf("device ID = %q, want 27 characters with zimaos_ prefix", deviceA)
	}
	for _, value := range []string{clientA[3:], deviceA[7:]} {
		for _, r := range value {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", r) {
				t.Fatalf("derived token %q contains invalid character %q", value, r)
			}
		}
	}
	if clientA[3:] == deviceA[7:] {
		t.Fatal("domain-separated client and device tokens must differ")
	}
	if strings.Contains(clientA, machineA) || strings.Contains(deviceA, machineA) {
		t.Fatal("public identities expose the raw machine ID")
	}
	if deriveClientID(machineB) == clientA || deriveDeviceID(machineB) == deviceA {
		t.Fatal("different machine IDs produced a collision")
	}
	for range 3 {
		if deriveClientID(machineA) != clientA || deriveDeviceID(machineA) != deviceA {
			t.Fatal("automatic identity changed across repeated resolution")
		}
	}
}

func TestAutomaticIdentityIgnoresHostnameChanges(t *testing.T) {
	const machineID = "0123456789abcdef0123456789abcdef"
	first := &fakeIdentitySource{host: "same-host", files: map[string]string{machineIDPath: machineID}}
	renamed := &fakeIdentitySource{host: "renamed-host", files: map[string]string{machineIDPath: machineID}}

	firstMachineID, err := canonicalMachineID(first)
	if err != nil {
		t.Fatal(err)
	}
	renamedMachineID, err := canonicalMachineID(renamed)
	if err != nil {
		t.Fatal(err)
	}
	if deriveClientID(firstMachineID) != deriveClientID(renamedMachineID) ||
		deriveDeviceID(firstMachineID) != deriveDeviceID(renamedMachineID) {
		t.Fatal("hostname rename changed automatic identities")
	}
}
