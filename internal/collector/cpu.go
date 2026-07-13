package collector

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
)

// CPUCollector reads Intel coretemp temperature and Intel RAPL package power.
type CPUCollector struct {
	raplPath      string
	hwmonTempPath string
	lastEnergy    uint64
	lastTime      time.Time
}

func NewCPUCollector() (*CPUCollector, error) {
	c := &CPUCollector{}

	if path := findCoretempPath(); path != "" {
		c.hwmonTempPath = path
	}

	raplPath := "/sys/class/powercap/intel-rapl/intel-rapl:0/energy_uj"
	if _, err := os.Stat(raplPath); err == nil {
		c.raplPath = raplPath
		if energy, err := readUint64File(raplPath); err == nil {
			c.lastEnergy = energy
			c.lastTime = time.Now()
		}
	}

	// Prime both aggregate and per-core CPU percent counters so the first
	// Collect() call returns accurate deltas rather than 0.
	_, _ = cpu.Percent(0, false)
	_, _ = cpu.Percent(0, true)

	return c, nil
}

// NumLogicalCores returns the number of logical CPU cores (hyperthreads counted).
func NumLogicalCores() int {
	n, _ := cpu.Counts(true)
	return n
}

// Collect returns (cpu package temperature °C, cpu package power watts, cpu usage %, per-core usage %).
// Returns 0/nil for values that cannot be read.
func (c *CPUCollector) Collect() (tempC float64, watts float64, usagePct float64, corePcts []float64) {
	if c.hwmonTempPath != "" {
		if v, err := readUint64File(c.hwmonTempPath); err == nil {
			tempC = float64(v) / 1000.0
		}
	}

	if c.raplPath != "" && !c.lastTime.IsZero() {
		if energy, err := readUint64File(c.raplPath); err == nil {
			elapsed := time.Since(c.lastTime).Seconds()
			if elapsed > 0 && energy >= c.lastEnergy {
				deltaUJ := float64(energy - c.lastEnergy)
				watts = (deltaUJ / 1e6) / elapsed
			}
			c.lastEnergy = energy
			c.lastTime = time.Now()
		}
	}

	if pcts, err := cpu.Percent(0, false); err == nil && len(pcts) > 0 {
		usagePct = pcts[0]
	}

	if pcts, err := cpu.Percent(0, true); err == nil {
		corePcts = pcts
	}

	return
}

// findCoretempPath finds the hwmon sysfs path for the Intel coretemp package sensor.
func findCoretempPath() string {
	entries, err := filepath.Glob("/sys/class/hwmon/hwmon*/name")
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		data, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) != "coretemp" {
			continue
		}
		dir := filepath.Dir(entry)
		// temp1 is the package-level sensor on Intel coretemp
		candidate := filepath.Join(dir, "temp1_input")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func readUint64File(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
