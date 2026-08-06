#!/bin/sh
# Install or upgrade zimaos-monitor on a ZimaOS device.
# Run as root: sudo ./install.sh
set -eu

UNIT=zimaos-monitor.service
TESTING=${ZIMAOS_MONITOR_TESTING:-0}
TEST_ROOT=${ZIMAOS_MONITOR_TEST_ROOT:-}

if [ "$TESTING" = "1" ]; then
    [ -n "$TEST_ROOT" ] || {
        echo "ERROR: ZIMAOS_MONITOR_TEST_ROOT is required in test mode" >&2
        exit 1
    }
    INSTALL_DIR=$TEST_ROOT/opt/zimaos-monitor
    SYSTEMD_DIR=$TEST_ROOT/etc/systemd/system
    SYSTEMCTL=${ZIMAOS_MONITOR_SYSTEMCTL:-systemctl}
else
    if [ -n "$TEST_ROOT" ] || [ -n "${ZIMAOS_MONITOR_SYSTEMCTL:-}" ]; then
        echo "ERROR: test overrides require ZIMAOS_MONITOR_TESTING=1" >&2
        exit 1
    fi
    INSTALL_DIR=/opt/zimaos-monitor
    SYSTEMD_DIR=/etc/systemd/system
    SYSTEMCTL=systemctl
fi

SRC_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONFIG_PATH=$INSTALL_DIR/config.yaml
BIN_PATH=$INSTALL_DIR/zimaos-monitor
UNIT_PATH=$SYSTEMD_DIR/$UNIT
BIN_SRC=
UNIT_SRC=
WORK_DIR=
CANDIDATE_CONFIG=
HAD_BIN=0
HAD_UNIT=0
WAS_ACTIVE=0
WAS_ENABLED=0
CURRENT_STAGED=

cleanup() {
    if [ -n "$CURRENT_STAGED" ]; then
        rm -f -- "$CURRENT_STAGED"
    fi
    if [ -n "$WORK_DIR" ]; then
        rm -f -- "$WORK_DIR/config.yaml" "$WORK_DIR/binary.backup" "$WORK_DIR/unit.backup"
        rmdir -- "$WORK_DIR" 2>/dev/null || true
    fi
}

on_signal() {
    code=$1
    trap - 0 HUP INT TERM
    cleanup
    exit "$code"
}

trap cleanup 0
trap 'on_signal 129' HUP
trap 'on_signal 130' INT
trap 'on_signal 143' TERM

die() {
    echo "ERROR: $*" >&2
    exit 1
}

run_systemctl() {
    "$SYSTEMCTL" "$@"
}

require_root() {
    if [ "$TESTING" != "1" ] && [ "$(id -u)" -ne 0 ]; then
        die "must be run as root (sudo ./install.sh)"
    fi
}

resolve_assets() {
    if [ -f "$SRC_DIR/zimaos-monitor" ]; then
        BIN_SRC=$SRC_DIR/zimaos-monitor
    elif [ -f "$SRC_DIR/zimaos-monitor-linux-amd64" ]; then
        BIN_SRC=$SRC_DIR/zimaos-monitor-linux-amd64
    else
        die "zimaos-monitor binary not found in $SRC_DIR"
    fi

    if [ -f "$SRC_DIR/$UNIT" ]; then
        UNIT_SRC=$SRC_DIR/$UNIT
    elif [ -f "$SRC_DIR/systemd/$UNIT" ]; then
        UNIT_SRC=$SRC_DIR/systemd/$UNIT
    else
        die "$UNIT not found in $SRC_DIR"
    fi
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "$1 command not found"
}

preflight() {
    require_root
    resolve_assets
    require_command install
    require_command mktemp
    require_command mv
    require_command ln
    require_command cp
    require_command chmod
    command -v "$SYSTEMCTL" >/dev/null 2>&1 || die "systemctl command not found"
}

create_work_dir() {
    base_tmp=${TMPDIR:-/tmp}
    WORK_DIR=$(mktemp -d "$base_tmp/zimaos-monitor-install.XXXXXX")
    chmod 700 "$WORK_DIR"
    CANDIDATE_CONFIG=$WORK_DIR/config.yaml
}

stage_file() {
    source_path=$1
    destination_path=$2
    mode=$3
    staged_path=$destination_path.new.$$
    CURRENT_STAGED=$staged_path
    rm -f -- "$staged_path"
    install -m"$mode" "$source_path" "$staged_path"
    mv -f -- "$staged_path" "$destination_path"
    CURRENT_STAGED=
}

stage_release_assets() {
    install -d "$INSTALL_DIR" "$SYSTEMD_DIR"
    stage_file "$BIN_SRC" "$BIN_PATH" 755
    stage_file "$UNIT_SRC" "$UNIT_PATH" 644
    rm -f -- "$SYSTEMD_DIR/zimaos-monitor-update.service"
}

prepare_first_configuration() {
    create_work_dir
    if ! "$BIN_SRC" setup --output "$CANDIDATE_CONFIG"; then
        die "interactive configuration failed; no installation was activated"
    fi
    if [ ! -f "$CANDIDATE_CONFIG" ]; then
        echo "Installation cancelled; no changes made."
        return 1
    fi
    return 0
}

commit_first_configuration() {
    { [ ! -e "$CONFIG_PATH" ] && [ ! -L "$CONFIG_PATH" ]; } ||
        die "configuration appeared during setup; refusing to overwrite $CONFIG_PATH"
    staged_config=$CONFIG_PATH.new.$$
    CURRENT_STAGED=$staged_config
    install -m600 "$CANDIDATE_CONFIG" "$staged_config"
    { [ ! -e "$CONFIG_PATH" ] && [ ! -L "$CONFIG_PATH" ]; } || {
        rm -f -- "$staged_config"
        CURRENT_STAGED=
        die "configuration appeared during setup; refusing to overwrite $CONFIG_PATH"
    }
    if ! ln -- "$staged_config" "$CONFIG_PATH"; then
        rm -f -- "$staged_config"
        CURRENT_STAGED=
        die "configuration appeared during setup; refusing to overwrite $CONFIG_PATH"
    fi
    rm -f -- "$staged_config"
    CURRENT_STAGED=
}

backup_upgrade_assets() {
    create_work_dir
    if [ -e "$BIN_PATH" ]; then
        cp -p -- "$BIN_PATH" "$WORK_DIR/binary.backup"
        HAD_BIN=1
    fi
    if [ -e "$UNIT_PATH" ]; then
        cp -p -- "$UNIT_PATH" "$WORK_DIR/unit.backup"
        HAD_UNIT=1
    fi
    if run_systemctl is-active "$UNIT" >/dev/null 2>&1; then
        WAS_ACTIVE=1
    fi
    if run_systemctl is-enabled "$UNIT" >/dev/null 2>&1; then
        WAS_ENABLED=1
    fi
}

rollback_upgrade() {
    echo "Upgrade activation failed; restoring previous release assets." >&2
    if [ "$HAD_BIN" -eq 1 ]; then
        stage_file "$WORK_DIR/binary.backup" "$BIN_PATH" 755
    else
        rm -f -- "$BIN_PATH"
    fi
    if [ "$HAD_UNIT" -eq 1 ]; then
        stage_file "$WORK_DIR/unit.backup" "$UNIT_PATH" 644
    else
        rm -f -- "$UNIT_PATH"
    fi
    run_systemctl daemon-reload || true
    if [ "$WAS_ENABLED" -eq 1 ]; then
        run_systemctl enable "$UNIT" || true
    else
        run_systemctl disable "$UNIT" || true
    fi
    if [ "$WAS_ACTIVE" -eq 1 ]; then
        run_systemctl restart "$UNIT" || true
    else
        run_systemctl stop "$UNIT" || true
    fi
}

verify_service() {
    run_systemctl is-enabled "$UNIT" >/dev/null 2>&1 &&
        run_systemctl is-active "$UNIT" >/dev/null 2>&1
}

print_recovery() {
    echo "Inspect and recover with:" >&2
    echo "  sudo systemctl status $UNIT --no-pager" >&2
    echo "  sudo journalctl -u $UNIT -n 100 --no-pager" >&2
    echo "  sudo nano $CONFIG_PATH" >&2
    echo "  sudo systemctl restart $UNIT" >&2
}

activate_first_install() {
    run_systemctl daemon-reload &&
        run_systemctl enable --now "$UNIT" &&
        verify_service
}

activate_upgrade() {
    run_systemctl daemon-reload &&
        run_systemctl enable "$UNIT" &&
        run_systemctl restart "$UNIT" &&
        verify_service
}

install_fresh() {
    if ! prepare_first_configuration; then
        return 0
    fi
    install -d "$INSTALL_DIR" "$SYSTEMD_DIR"
    stage_release_assets
    commit_first_configuration
    if ! activate_first_install; then
        echo "ERROR: zimaos-monitor was installed but the service did not become active." >&2
        print_recovery
        return 1
    fi
    echo "zimaos-monitor installed, configured, enabled, and started."
    echo "Logs: sudo journalctl -u $UNIT -f"
}

install_upgrade() {
    backup_upgrade_assets
    stage_release_assets
    chmod 600 "$CONFIG_PATH"
    if ! activate_upgrade; then
        rollback_upgrade
        print_recovery
        return 1
    fi
    echo "zimaos-monitor upgraded and restarted; existing configuration was preserved."
    echo "Logs: sudo journalctl -u $UNIT -f"
}

main() {
    preflight
    if [ -e "$CONFIG_PATH" ] || [ -L "$CONFIG_PATH" ]; then
        [ -f "$CONFIG_PATH" ] && [ ! -L "$CONFIG_PATH" ] ||
            die "existing configuration is not a regular file: $CONFIG_PATH"
        install_upgrade
    else
        install_fresh
    fi
}

main "$@"
