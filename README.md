# zimaos-monitor

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A Go service for ZimaOS that collects system metrics and publishes them via MQTT with Home Assistant autodiscovery. Works on all ZimaSpace devices: ZimaBoard, ZimaBlade, ZimaCube, and others.

![Device page in Home Assistant](screenshots/screenshot-01.png)

---

## Home Assistant Entities

MQTT discovery creates 10 fixed sensors, one sensor per logical CPU core, four sensors per disk, and up to two informational update entities.

### Sensor entities

| Entity | Source | JSON field | Unit |
|--------|--------|------------|------|
| CPU Temperature | `/sys/class/hwmon` (Intel coretemp) | `cpu_temp` | `°C` |
| CPU Power | `/sys/class/powercap` (Intel RAPL) | `cpu_watts` | `W` |
| CPU Usage | gopsutil | `cpu_usage_pct` | `%` |
| CPU Core N Usage | gopsutil | `cpu_core_pct[N]` | `%` |
| RAM Used | gopsutil | `ram_used_pct` | `%` |
| RAM Available | gopsutil | `ram_available_gb` | `GB` |
| RAM Total | gopsutil | `ram_total_gb` | `GB` |
| Load Average 1m | gopsutil | `load_avg_1` | - |
| Load Average 5m | gopsutil | `load_avg_5` | - |
| Load Average 15m | gopsutil | `load_avg_15` | - |
| Uptime | gopsutil | `uptime_seconds` | `s` |
| `<disk> Used %` | gopsutil per mount | `disks.<disk_key>.used_pct` | `%` |
| `<disk> Used` | gopsutil per mount | `disks.<disk_key>.used_gb` | `GB` |
| `<disk> Total` | gopsutil per mount | `disks.<disk_key>.total_gb` | `GB` |
| `<disk> Free` | gopsutil per mount | `disks.<disk_key>.free_gb` | `GB` |

CPU core indexes start at `0`. For discovery templates, `<disk_key>` is derived from the configured disk name, replacing spaces with underscores.

### Update entities

| Entity | State topic | Payload fields | Installable from Home Assistant |
|--------|-------------|----------------|---------------------------------|
| ZimaOS Version | `<device_id>/update` | `installed_version`, `latest_version`, `release_url` | No, informational only |
| zimaos-monitor Update | `<device_id>/monitor/update` | `installed_version`, `latest_version`, `release_url`, `title` | No, run `zimaos-monitor update` on the device |

`ZimaOS Version` is created when `updates.enabled` is `true`; `zimaos-monitor Update` is created when `monitor_updates.enabled` is `true`.

---

## Configuration (`config.yaml`)

All `device` fields and `disks` are **auto-detected** at startup — a minimal config only needs the MQTT broker:

```yaml
mqtt:
  broker: "tcp://YOUR_MQTT_BROKER_IP:1883"
  username: ""
  password: ""

interval: 30s
```

Auto-detected defaults:

| Field | Source |
|-------|--------|
| `device.id` | Sanitized hostname (e.g. `zimaboard2`) |
| `device.name` | `"ZimaOS <hostname>"` |
| `device.model` | `/sys/class/dmi/id/product_name` (e.g. `ZimaBoard2`) |
| `device.manufacturer` | `"Pocho Labs"` |
| `device.serial_number` | `/sys/class/dmi/id/product_serial` (if available) |
| `disks` | All `/media/*` mounts + `/DATA` → `ZimaOS-HD` |

Override any field in `config.yaml` to customize:

```yaml
device:
  name: "Living Room NAS"
  model: "ZimaCube"

disks:
  - path: "/DATA"
    name: "ZimaOS-HD"
  - path: "/media/SSD-Storage"
    name: "SSD"
```

### `updates` section (optional)

Controls the GitHub upstream version check for the ZimaOS Update entity in HA:

```yaml
updates:
  enabled: true       # default true
  check_interval: 6h  # default 6h; minimum 1h enforced
```

### `monitor_updates` section (optional)

Checks stable releases of this project and creates a separate Home Assistant Update entity:

```yaml
monitor_updates:
  enabled: true       # default true
  check_interval: 6h  # default 6h; minimum 1h enforced
```

This entity is informational only. Updates are started locally from the ZimaOS host.

---

## Home Assistant

**Prerequisite:** An MQTT broker must be running and connected to Home Assistant via the [MQTT integration](https://www.home-assistant.io/integrations/mqtt/). The broker address is what you set in `config.yaml` under `mqtt.broker`.

Once running, sensors appear automatically under:

**Settings → Devices & Services → MQTT → Devices → ZimaOS \<hostname\>**

The device card shows:
- CPU Temperature, CPU Power, total CPU Usage, and per-core CPU Usage
- Load Average for 1, 5, and 15 minutes
- RAM Used %, RAM Available, and RAM Total
- Uptime, exported as seconds and rendered by Home Assistant as a duration
- Per-disk Used %, Used, Total, and Free (one set per `/media/*` mount and `/DATA`)
- **ZimaOS Version** — a native informational Update entity showing installed vs. latest stable release, with a link to release notes
- **zimaos-monitor Update** — shows the installed and latest monitor versions and links to
  the release notes. It does not accept installation commands from MQTT; update locally with
  `zimaos-monitor update`.

![Device page in Home Assistant](screenshots/screenshot-01.png)

Clicking the update entity shows the installed and latest versions with a link to the release announcement:

![ZimaOS Version update entity](screenshots/screenshot-02.png)

Each sensor includes full history tracked by Home Assistant:

![CPU Temperature history](screenshots/screenshot-03.png)

---

## MQTT Topics

| Topic | Content |
|-------|---------|
| `<device_id>/state` | JSON payload with all metrics (not retained) |
| `<device_id>/update` | Flat JSON with `installed_version`, `latest_version`, `release_url` for the HA Update entity (not retained) |
| `<device_id>/monitor/update` | Monitor update state with installed/latest versions and release URL (not retained) |
| `homeassistant/sensor/<device_id>/+/config` | HA sensor autodiscovery (retained) |
| `homeassistant/update/<device_id>/zimaos_version/config` | HA Update entity autodiscovery (retained) |
| `homeassistant/update/<device_id>/zimaos_monitor/config` | Monitor Update entity autodiscovery (retained) |

On startup, stale retained discovery topics from previous deployments are automatically purged to prevent duplicate sensors in HA.

### Example payload

```json
{
  "cpu_temp": 42.0,
  "cpu_watts": 3.5,
  "cpu_usage_pct": 18.7,
  "cpu_core_pct": [12.1, 25.3, 17.8, 19.6],
  "load_avg_1": 0.42,
  "load_avg_5": 0.31,
  "load_avg_15": 0.28,
  "uptime_seconds": 86400,
  "ram_used_pct": 28.9,
  "ram_available_gb": 11.4,
  "ram_total_gb": 16.0,
  "disks": {
    "ZimaOS-HD":   { "path": "/DATA",              "used_pct": 12.4, "free_gb": 41.8, "used_gb": 5.6,   "total_gb": 47.4 },
    "SSD-Storage": { "path": "/media/SSD-Storage", "used_pct": 71.5, "free_gb": 145.9, "used_gb": 341.0, "total_gb": 477.0 }
  },
  "zimaos": {
    "installed_version": "1.5.4",
    "latest_version": "1.5.4",
    "release_url": "https://github.com/IceWhaleTech/ZimaOS/releases/tag/1.5.4"
  }
}
```

The monitor update state is published separately to `<device_id>/monitor/update`:

```json
{
  "installed_version": "v0.1.0",
  "latest_version": "v0.2.0",
  "release_url": "https://github.com/Pocho-Labs/zimaos-monitor/releases/tag/v0.2.0",
  "title": "zimaos-monitor"
}
```

---

## Build

```bash
make build          # build for current platform
make build-linux    # cross-compile for Linux x86_64 (ZimaOS)
make run-dry        # run locally without MQTT, prints JSON to stdout
make test           # Go tests + installer syntax and PTY integration tests
make tidy           # go mod tidy
```

---

## Install on ZimaOS

The installer (`scripts/install.sh`) places the binary under `/opt/zimaos-monitor`, installs
the systemd unit, and guides first-time MQTT configuration. Run it from an interactive SSH
terminal as `root`.

On first install it asks:

| Question | Required | Default |
|----------|----------|---------|
| MQTT broker host | Yes | None |
| MQTT broker port | No | `1883` |
| MQTT username | No | Empty |
| MQTT password | No | Empty |

The password is entered with terminal echo disabled and is never included in the confirmation
summary. All other settings retain their documented defaults: client ID `zimaos-monitor`,
30-second publishing, automatic device and disk detection, and update checks every six hours.
After a redacted confirmation, the installer writes
`/opt/zimaos-monitor/config.yaml` as a root-only file, enables the service, starts it, and
verifies that it is active.

First installation stops before changing active files when no terminal is available, input ends,
or confirmation is declined. Rerunning the installer with an existing configuration skips all
questions, preserves its contents, installs new release assets, and restarts the service.

The binary also exposes the configuration-only command used by the installer:

```bash
zimaos-monitor setup --output /path/to/new-config.yaml
```

It requires a terminal, refuses to replace an existing path, and does not contact MQTT, collect
metrics, or operate systemd. Use `install.sh` for the complete supported installation lifecycle.

### Update command

The first release containing the update command must be installed with one of the methods
below. After that, update to the latest stable release directly from the ZimaOS host:

```bash
sudo /opt/zimaos-monitor/zimaos-monitor update
```

The command must run as `root`, requires internet access to GitHub, and always targets the
latest stable release. It does not accept a version argument or provide a force option.
Development builds such as `dev` or `v0.1.0-3-gabcdef` are considered eligible for an
update to the latest stable release. If the installed stable version is already current or
newer, the command exits without changing the installation.

During an update, the command:

1. Fetches the latest stable release metadata from GitHub.
2. Selects the exact `linux-amd64` release archive.
3. Downloads the archive and verifies its SHA-256 digest.
4. Extracts the candidate binary and confirms that its reported version matches the release.
5. Creates a temporary backup and atomically replaces the current executable.
6. Restarts `zimaos-monitor.service` and waits for it to become active.
7. Restores the previous binary and restarts the service if the health check fails.

Only the executable is replaced. The installed `/opt/zimaos-monitor/config.yaml` is not
modified. A lock prevents two update processes from running at the same time, and update
installation cannot be triggered through MQTT or Home Assistant.

After a successful update, verify the installed version and service status:

```bash
/opt/zimaos-monitor/zimaos-monitor --version
systemctl status zimaos-monitor --no-pager
```

Update errors are printed directly by the command. To inspect a service startup or rollback
failure:

```bash
journalctl -u zimaos-monitor -n 100 --no-pager
```

### Option A: Download a release (recommended)

SSH into the ZimaOS device and run:

```bash
cd /tmp
TAG=$(curl -sL https://api.github.com/repos/Pocho-Labs/zimaos-monitor/releases/latest | grep '"tag_name"' | cut -d'"' -f4)
curl -LO "https://github.com/Pocho-Labs/zimaos-monitor/releases/download/${TAG}/zimaos-monitor-${TAG}-linux-amd64.tar.gz"
tar -xzf zimaos-monitor-*.tar.gz
cd zimaos-monitor-*-linux-amd64
sudo ./install.sh
```

On first install, answer the MQTT questions and confirm the displayed settings. The service starts
immediately. On upgrade, `config.yaml` is preserved and no questions are asked.

If startup fails, the validated configuration remains available for correction:

```bash
sudo systemctl status zimaos-monitor.service --no-pager
sudo journalctl -u zimaos-monitor.service -n 100 --no-pager
sudo nano /opt/zimaos-monitor/config.yaml
sudo systemctl restart zimaos-monitor.service
```

### Option B: Deploy a local build (development)

```bash
# On your dev machine:
make build-linux
scp bin/zimaos-monitor-linux-amd64 \
    systemd/zimaos-monitor.service \
    scripts/install.sh \
    config.example.yaml \
    <user>@<zima-host>:/tmp/

# On the ZimaOS device:
ssh <user>@<zima-host>
cd /tmp && sudo ./install.sh
```

### Uninstall

```bash
sudo systemctl disable --now zimaos-monitor
sudo rm -rf /opt/zimaos-monitor /etc/systemd/system/zimaos-monitor.service
sudo systemctl daemon-reload
```

---

## Technical Notes

- **Intel RAPL** requires `/sys/class/powercap/intel-rapl/`. The service runs as `root`. First metric publish always reports 0 W (no previous delta).
- **Disk discovery** only includes `/media/*` mounts and `/DATA` (mapped to `ZimaOS-HD`), matching exactly what the ZimaOS UI shows.
- **HA autodiscovery** is published with `retained: true` and re-published every 10 intervals to survive HA restarts. Stale topics from prior deployments are purged on startup.
- **Update check** queries `https://api.github.com/repos/IceWhaleTech/ZimaOS/releases` and filters out `-alpha`/`-beta`/`-rc` tags (the repo marks all releases as non-prerelease). Rate limit: 60 req/h unauthenticated — well within the 6 h default interval.
- **Monitor update check** queries the latest stable release from
  `Pocho-Labs/zimaos-monitor`. The local update command requires the exact amd64 tarball
  and either GitHub's SHA-256 asset digest or the workflow-generated `.sha256` asset.
- **Uptime** is sent as an integer number of seconds with Home Assistant device class
  `duration`, unit `s`, and state class `measurement`.

---

## Say thanks

If you find this project useful, consider subscribing to my YouTube channel or following me on X — it really helps!

- 📺 [youtube.com/@PochoLabs](https://youtube.com/@PochoLabs)
- 🐦 [x.com/EzeLibrandi](https://x.com/EzeLibrandi)

---

## Contributing

1. Fork the repository
2. Create a branch: `git checkout -b feature/my-change`
3. Copy `config.example.yaml` → `config.yaml` and configure your device
4. Test with `make test` and `make run-dry`
5. Open a pull request

## License

MIT — see [LICENSE](LICENSE).
