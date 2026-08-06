package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeBrokerHost(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "dns", input: " broker.local ", want: "broker.local"},
		{name: "ipv4", input: "192.168.1.20", want: "192.168.1.20"},
		{name: "raw ipv6", input: "2001:db8::10", want: "2001:db8::10"},
		{name: "bracketed ipv6", input: "[2001:db8::10]", want: "2001:db8::10"},
		{name: "empty", input: "  ", wantErr: true},
		{name: "scheme", input: "tcp://broker.local", wantErr: true},
		{name: "embedded port", input: "broker.local:1883", wantErr: true},
		{name: "path", input: "broker.local/path", wantErr: true},
		{name: "bad label", input: "-broker.local", wantErr: true},
		{name: "scoped ipv6", input: "fe80::1%eth0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBrokerHost(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBrokerHost(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeBrokerHost(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseBrokerPort(t *testing.T) {
	tests := []struct {
		input   string
		want    uint16
		wantErr bool
	}{
		{input: "", want: 1883},
		{input: "1884", want: 1884},
		{input: "1", want: 1},
		{input: "65535", want: 65535},
		{input: "0", wantErr: true},
		{input: "65536", wantErr: true},
		{input: "-1", wantErr: true},
		{input: "mqtt", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseBrokerPort(tt.input)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parseBrokerPort(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want {
			t.Fatalf("parseBrokerPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestBrokerURI(t *testing.T) {
	tests := []struct {
		host string
		port uint16
		want string
	}{
		{host: "broker.local", port: 1883, want: "tcp://broker.local:1883"},
		{host: "192.168.1.20", port: 1884, want: "tcp://192.168.1.20:1884"},
		{host: "2001:db8::10", port: 1883, want: "tcp://[2001:db8::10]:1883"},
	}
	for _, tt := range tests {
		if got := brokerURI(tt.host, tt.port); got != tt.want {
			t.Fatalf("brokerURI(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

func TestRunnerDefaultConfiguration(t *testing.T) {
	console := &fakeConsole{
		terminal: true,
		lines:    []string{"broker.local", "", "", "yes"},
		secrets:  []string{""},
	}
	files := &fakeWriter{}
	err := NewRunner(console, files).Run("/tmp/candidate.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if files.path != "/tmp/candidate.yaml" || files.perm != 0o600 {
		t.Fatalf("candidate path/mode = %q/%#o", files.path, files.perm)
	}
	var got GeneratedConfig
	if err := yaml.Unmarshal(files.data, &got); err != nil {
		t.Fatalf("generated YAML: %v", err)
	}
	if got.MQTT.Broker != "tcp://broker.local:1883" {
		t.Fatalf("broker = %q", got.MQTT.Broker)
	}
	if got.MQTT.Username != "" || got.MQTT.Password != "" {
		t.Fatalf("default credentials = %q/%q", got.MQTT.Username, got.MQTT.Password)
	}
	if !strings.Contains(console.output.String(), "Password: not configured") {
		t.Fatalf("summary missing redacted password status: %s", console.output.String())
	}
	output := console.output.String()
	if strings.Contains(output, "Client ID: zimaos-monitor") {
		t.Fatalf("summary still advertises shared Client ID: %s", output)
	}
	if !strings.Contains(output, "Client and device IDs: derived per machine at startup") {
		t.Fatalf("summary missing machine-specific identity guidance: %s", output)
	}
	if strings.Contains(string(files.data), "client_id:") ||
		strings.Contains(string(files.data), "device:") {
		t.Fatalf("generated YAML must omit runtime identity fields:\n%s", files.data)
	}
}

func TestRunnerCancellationAndPreconditions(t *testing.T) {
	t.Run("not terminal", func(t *testing.T) {
		err := NewRunner(&fakeConsole{}, &fakeWriter{}).Run("/tmp/candidate.yaml")
		if !errors.Is(err, ErrNotTerminal) {
			t.Fatalf("error = %v, want ErrNotTerminal", err)
		}
	})
	t.Run("declined", func(t *testing.T) {
		console := &fakeConsole{terminal: true, lines: []string{"broker.local", "", "", "n"}, secrets: []string{""}}
		files := &fakeWriter{}
		err := NewRunner(console, files).Run("/tmp/candidate.yaml")
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if files.data != nil {
			t.Fatal("candidate written after decline")
		}
	})
	t.Run("existing output", func(t *testing.T) {
		console := &fakeConsole{terminal: true, lines: []string{"broker.local", "", "", "yes"}, secrets: []string{""}}
		files := &fakeWriter{err: fs.ErrExist}
		if err := NewRunner(console, files).Run("/tmp/existing.yaml"); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("error = %v, want fs.ErrExist", err)
		}
	})
}

func TestRunnerAuthenticatedConfiguration(t *testing.T) {
	password := "  p:#'\"\\ä  "
	console := &fakeConsole{
		terminal: true,
		lines:    []string{"2001:db8::10", "2883", "mqtt user", "yes"},
		secrets:  []string{password},
	}
	files := &fakeWriter{}
	if err := NewRunner(console, files).Run("/tmp/auth.yaml"); err != nil {
		t.Fatal(err)
	}
	var got GeneratedConfig
	if err := yaml.Unmarshal(files.data, &got); err != nil {
		t.Fatal(err)
	}
	if got.MQTT.Broker != "tcp://[2001:db8::10]:2883" {
		t.Fatalf("broker = %q", got.MQTT.Broker)
	}
	if got.MQTT.Username != "mqtt user" || got.MQTT.Password != password {
		t.Fatalf("credentials did not round trip")
	}
	output := console.output.String()
	if strings.Contains(output, password) {
		t.Fatal("password leaked into setup output")
	}
	if !strings.Contains(output, "Password: configured") {
		t.Fatalf("summary missing configured status: %s", output)
	}
}

func TestRunnerPasswordWithoutUsername(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		console := &fakeConsole{
			terminal: true,
			lines:    []string{"broker.local", "", "", "yes", "yes"},
			secrets:  []string{"secret"},
		}
		files := &fakeWriter{}
		if err := NewRunner(console, files).Run("/tmp/no-user.yaml"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(console.output.String(), "without a username") {
			t.Fatal("missing password-without-username warning")
		}
	})
	t.Run("declined", func(t *testing.T) {
		console := &fakeConsole{
			terminal: true,
			lines:    []string{"broker.local", "", "", "no"},
			secrets:  []string{"secret"},
		}
		files := &fakeWriter{}
		if err := NewRunner(console, files).Run("/tmp/no-user.yaml"); !errors.Is(err, ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if files.data != nil {
			t.Fatal("candidate written after warning decline")
		}
	})
}

func TestOSCandidateWriterRefusesExistingAndSymlinkPaths(t *testing.T) {
	dir := t.TempDir()
	writer := osCandidateWriter{}
	path := filepath.Join(dir, "candidate.yaml")
	if err := writer.WriteExclusive(path, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o, want 0600", info.Mode().Perm())
	}
	if err := writer.WriteExclusive(path, []byte("overwrite"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("existing output error = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "safe" {
		t.Fatalf("existing output changed to %q", got)
	}

	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("target-safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "candidate-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteExclusive(link, []byte("overwrite"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("symlink output error = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "target-safe" {
		t.Fatalf("symlink target changed to %q", got)
	}
}

type fakeConsole struct {
	terminal bool
	lines    []string
	secrets  []string
	output   strings.Builder
}

func (f *fakeConsole) IsTerminal() bool { return f.terminal }

func (f *fakeConsole) ReadLine(prompt string) (string, error) {
	f.output.WriteString(prompt)
	if len(f.lines) == 0 {
		return "", errors.New("unexpected line prompt")
	}
	value := f.lines[0]
	f.lines = f.lines[1:]
	return value, nil
}

func (f *fakeConsole) ReadSecret(prompt string) (string, error) {
	f.output.WriteString(prompt)
	if len(f.secrets) == 0 {
		return "", errors.New("unexpected secret prompt")
	}
	value := f.secrets[0]
	f.secrets = f.secrets[1:]
	return value, nil
}

func (f *fakeConsole) Printf(format string, args ...any) {
	f.output.WriteString(fmt.Sprintf(format, args...))
}

type fakeWriter struct {
	path string
	data []byte
	perm fs.FileMode
	err  error
}

func (f *fakeWriter) WriteExclusive(path string, data []byte, perm fs.FileMode) error {
	if f.err != nil {
		return f.err
	}
	f.path = path
	f.data = append([]byte(nil), data...)
	f.perm = perm
	return nil
}
