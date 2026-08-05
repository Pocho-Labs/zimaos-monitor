package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

var (
	ErrCancelled   = errors.New("setup cancelled")
	ErrNotTerminal = errors.New("interactive setup requires a terminal")
)

// Answers contains values collected during first-time setup. Password must
// never be included in diagnostics or summaries.
type Answers struct {
	BrokerHost string
	BrokerPort uint16
	Username   string
	Password   string
	Confirmed  bool
}

// GeneratedConfig is the minimal persisted configuration produced by setup.
type GeneratedConfig struct {
	MQTT MQTTConfig `yaml:"mqtt"`
}

// MQTTConfig contains only the broker values entered during setup. Runtime
// defaults remain owned by internal/config.
type MQTTConfig struct {
	Broker   string `yaml:"broker"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// Console isolates terminal input, secret input, and output for deterministic
// unit tests.
type Console interface {
	IsTerminal() bool
	ReadLine(prompt string) (string, error)
	ReadSecret(prompt string) (string, error)
	Printf(format string, args ...any)
}

// CandidateWriter writes a new protected candidate without replacing an
// existing path.
type CandidateWriter interface {
	WriteExclusive(path string, data []byte, perm fs.FileMode) error
}

// Runner owns the offline setup flow.
type Runner struct {
	console Console
	files   CandidateWriter
}

func NewRunner(console Console, files CandidateWriter) *Runner {
	return &Runner{console: console, files: files}
}

// Run executes the setup flow and writes a candidate configuration.
func (r *Runner) Run(outputPath string) error {
	if outputPath == "" {
		return errors.New("output path is required")
	}
	if !r.console.IsTerminal() {
		return ErrNotTerminal
	}

	host, err := r.promptHost()
	if err != nil {
		return err
	}
	port, err := r.promptPort()
	if err != nil {
		return err
	}
	username, err := r.console.ReadLine("MQTT username [none]: ")
	if err != nil {
		return inputError(err)
	}
	password, err := r.console.ReadSecret("MQTT password [none]: ")
	if err != nil {
		return inputError(err)
	}

	if password != "" && username == "" {
		answer, err := r.console.ReadLine(
			"Password is configured without a username. Continue? [y/N]: ",
		)
		if err != nil {
			return inputError(err)
		}
		if !affirmative(answer) {
			return ErrCancelled
		}
	}

	answers := Answers{
		BrokerHost: host,
		BrokerPort: port,
		Username:   username,
		Password:   password,
	}
	r.printSummary(answers)
	confirmation, err := r.console.ReadLine("Install and start zimaos-monitor? [y/N]: ")
	if err != nil {
		return inputError(err)
	}
	if !affirmative(confirmation) {
		return ErrCancelled
	}
	answers.Confirmed = true

	contents, err := marshalConfig(answers)
	if err != nil {
		return err
	}
	if err := r.files.WriteExclusive(outputPath, contents, 0o600); err != nil {
		return fmt.Errorf("write candidate configuration: %w", err)
	}
	return nil
}

func (r *Runner) promptHost() (string, error) {
	for {
		value, err := r.console.ReadLine("MQTT broker host (required): ")
		if err != nil {
			return "", inputError(err)
		}
		host, err := normalizeBrokerHost(value)
		if err == nil {
			return host, nil
		}
		r.console.Printf("ERROR: %v.\n", err)
	}
}

func (r *Runner) promptPort() (uint16, error) {
	for {
		value, err := r.console.ReadLine("MQTT broker port [1883]: ")
		if err != nil {
			return 0, inputError(err)
		}
		port, err := parseBrokerPort(value)
		if err == nil {
			return port, nil
		}
		r.console.Printf("ERROR: %v.\n", err)
	}
}

func (r *Runner) printSummary(answers Answers) {
	username := answers.Username
	if username == "" {
		username = "not configured"
	}
	password := "not configured"
	if answers.Password != "" {
		password = "configured"
	}
	r.console.Printf("\nConfiguration summary:\n")
	r.console.Printf("  Broker: %s\n", brokerURI(answers.BrokerHost, answers.BrokerPort))
	r.console.Printf("  Username: %s\n", username)
	r.console.Printf("  Password: %s\n", password)
	r.console.Printf("  Client ID: zimaos-monitor\n")
	r.console.Printf("  Publish interval: 30s\n")
	r.console.Printf("  Device and disks: auto-detected\n")
	r.console.Printf("  Update checks: enabled (6h intervals)\n\n")
}

func normalizeBrokerHost(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", errors.New("MQTT broker host is required")
	}
	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		if len(host) < 3 || host[0] != '[' || host[len(host)-1] != ']' {
			return "", errors.New("MQTT broker host has invalid IPv6 brackets")
		}
		host = host[1 : len(host)-1]
	}
	if strings.Contains(host, "%") {
		return "", errors.New("MQTT broker host does not support scoped IPv6")
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("MQTT broker host contains whitespace or control characters")
		}
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/\\@?#") {
		return "", errors.New("MQTT broker host must not include a scheme, path, or credentials")
	}

	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	if strings.Contains(host, ":") {
		return "", errors.New("MQTT broker host has invalid IPv6 syntax")
	}
	if looksLikeIPv4(host) {
		return "", errors.New("MQTT broker host has invalid IPv4 syntax")
	}
	if err := validateDNSName(host); err != nil {
		return "", err
	}
	return host, nil
}

func looksLikeIPv4(host string) bool {
	if !strings.Contains(host, ".") {
		return false
	}
	for _, r := range host {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func validateDNSName(host string) error {
	if len(host) > 253 {
		return errors.New("MQTT broker hostname is longer than 253 characters")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return errors.New("MQTT broker hostname contains an invalid label")
		}
		for i, r := range label {
			valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
			if !valid || (r == '-' && (i == 0 || i == len(label)-1)) {
				return errors.New("MQTT broker hostname contains an invalid label")
			}
		}
	}
	return nil
}

func parseBrokerPort(value string) (uint16, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1883, nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("MQTT broker port must be between 1 and 65535")
		}
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, errors.New("MQTT broker port must be between 1 and 65535")
	}
	return uint16(port), nil
}

func brokerURI(host string, port uint16) string {
	return (&url.URL{
		Scheme: "tcp",
		Host:   net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10)),
	}).String()
}

func affirmative(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func marshalConfig(answers Answers) ([]byte, error) {
	cfg := GeneratedConfig{
		MQTT: MQTTConfig{
			Broker:   brokerURI(answers.BrokerHost, answers.BrokerPort),
			Username: answers.Username,
			Password: answers.Password,
		},
	}
	contents, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("serialize configuration: %w", err)
	}
	var validation GeneratedConfig
	if err := yaml.Unmarshal(contents, &validation); err != nil {
		return nil, fmt.Errorf("validate generated configuration: %w", err)
	}
	if validation.MQTT.Broker == "" {
		return nil, errors.New("validate generated configuration: broker is empty")
	}
	return contents, nil
}

func inputError(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: input ended before setup completed", ErrCancelled)
	}
	return fmt.Errorf("read setup input: %w", err)
}

// Run uses the production terminal and filesystem implementations.
func Run(outputPath string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%w: open /dev/tty", ErrNotTerminal)
	}
	defer tty.Close()
	console := &terminalConsole{tty: tty, reader: bufio.NewReader(tty)}
	return NewRunner(console, osCandidateWriter{}).Run(outputPath)
}

type terminalConsole struct {
	tty    *os.File
	reader *bufio.Reader
}

func (c *terminalConsole) IsTerminal() bool {
	return term.IsTerminal(int(c.tty.Fd()))
}

func (c *terminalConsole) ReadLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(c.tty, prompt); err != nil {
		return "", err
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func (c *terminalConsole) ReadSecret(prompt string) (string, error) {
	fd := int(c.tty.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", err
	}
	noEcho := *state
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &noEcho); err != nil {
		return "", err
	}
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_ = unix.IoctlSetTermios(fd, unix.TCSETS, state)
		})
	}
	defer restore()

	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case sig := <-sigCh:
			restore()
			signal.Stop(sigCh)
			_ = syscall.Kill(os.Getpid(), sig.(syscall.Signal))
		case <-done:
		}
	}()

	if _, err := fmt.Fprint(c.tty, prompt); err != nil {
		close(done)
		return "", err
	}
	password, err := c.reader.ReadString('\n')
	close(done)
	restore()
	_, _ = fmt.Fprintln(c.tty)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r"), nil
}

func (c *terminalConsole) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(c.tty, format, args...)
}

type osCandidateWriter struct{}

func (osCandidateWriter) WriteExclusive(path string, data []byte, perm fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(perm); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
