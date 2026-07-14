#!/bin/sh
# Install or upgrade zimaos-monitor on a ZimaOS device.
# Run as root:  sudo ./install.sh
set -e

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: must be run as root (sudo ./install.sh)" >&2
    exit 1
fi

INSTALL_DIR=/opt/zimaos-monitor
SYSTEMD_DIR=/etc/systemd/system
UNIT=zimaos-monitor.service

SRC_DIR=$(cd "$(dirname "$0")" && pwd)

# Locate the binary. The release tarball ships it as `zimaos-monitor`;
# `make build-linux` produces `zimaos-monitor-linux-amd64`.
if [ -f "$SRC_DIR/zimaos-monitor" ]; then
    BIN_SRC="$SRC_DIR/zimaos-monitor"
elif [ -f "$SRC_DIR/zimaos-monitor-linux-amd64" ]; then
    BIN_SRC="$SRC_DIR/zimaos-monitor-linux-amd64"
else
    echo "ERROR: zimaos-monitor binary not found in $SRC_DIR" >&2
    exit 1
fi

# Locate the unit file. Release tarball ships it flat; repo checkout has it under systemd/.
if [ -f "$SRC_DIR/$UNIT" ]; then
    UNIT_SRC="$SRC_DIR/$UNIT"
elif [ -f "$SRC_DIR/systemd/$UNIT" ]; then
    UNIT_SRC="$SRC_DIR/systemd/$UNIT"
else
    echo "ERROR: $UNIT not found in $SRC_DIR" >&2
    exit 1
fi

if [ ! -f "$SRC_DIR/config.yaml" ]; then
    echo "ERROR: config.yaml not found in $SRC_DIR" >&2
    exit 1
fi

install -d "$INSTALL_DIR"
install -m755 "$BIN_SRC" "$INSTALL_DIR/zimaos-monitor"
install -m644 "$UNIT_SRC" "$SYSTEMD_DIR/$UNIT"
# Remove the transient updater unit shipped by pre-release development builds.
rm -f "$SYSTEMD_DIR/zimaos-monitor-update.service"

FIRST_INSTALL=0
if [ ! -f "$INSTALL_DIR/config.yaml" ]; then
    install -m644 "$SRC_DIR/config.yaml" "$INSTALL_DIR/config.yaml"
    FIRST_INSTALL=1
fi

systemctl daemon-reload

if [ "$FIRST_INSTALL" = "1" ]; then
    systemctl enable "$UNIT"
    cat <<EOF

zimaos-monitor installed — service enabled but NOT started yet.

Next steps:
  sudo nano $INSTALL_DIR/config.yaml
  sudo systemctl start zimaos-monitor
  sudo journalctl -u zimaos-monitor -f
EOF
else
    systemctl restart "$UNIT"
    echo "zimaos-monitor upgraded and restarted."
    echo "Logs: sudo journalctl -u zimaos-monitor -f"
fi
