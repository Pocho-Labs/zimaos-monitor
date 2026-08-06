#!/usr/bin/env python3
import fcntl
import os
import pty
import select
import signal
import shutil
import subprocess
import tempfile
import termios
import time
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[1]


class InstallerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build_dir = Path(tempfile.mkdtemp(prefix="zimaos-monitor-build-"))
        cls.binary = cls.build_dir / "zimaos-monitor"
        subprocess.run(
            ["go", "build", "-o", str(cls.binary), "./cmd/zimaos-monitor"],
            cwd=REPO,
            check=True,
        )

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.build_dir)

    def setUp(self):
        self.temp = Path(tempfile.mkdtemp(prefix="zimaos-monitor-install-test-"))
        self.stage = self.temp / "stage"
        self.root = self.temp / "root"
        self.work_tmp = self.temp / "tmp"
        self.stage.mkdir()
        self.work_tmp.mkdir()
        shutil.copy2(self.binary, self.stage / "zimaos-monitor")
        shutil.copy2(REPO / "scripts/install.sh", self.stage / "install.sh")
        shutil.copy2(
            REPO / "systemd/zimaos-monitor.service",
            self.stage / "zimaos-monitor.service",
        )
        self.systemctl = self.temp / "systemctl"
        self.systemctl.write_text(
            "#!/bin/sh\n"
            'printf "%s\\n" "$*" >> "$ZIMAOS_MONITOR_TEST_ROOT/systemctl.log"\n'
            'if [ "$1" = "daemon-reload" ] && [ ! -f "$ZIMAOS_MONITOR_TEST_ROOT/opt/zimaos-monitor/config.yaml" ]; then exit 9; fi\n'
            'if [ "${ZIMAOS_MONITOR_SYSTEMCTL_FAIL:-}" = "$1" ]; then exit 1; fi\n'
            'case "$1" in is-enabled|is-active) exit 0 ;; esac\n'
            "exit 0\n",
            encoding="utf-8",
        )
        self.systemctl.chmod(0o755)

    def tearDown(self):
        shutil.rmtree(self.temp)

    def env(self, **overrides):
        env = os.environ.copy()
        env.update(
            {
                "ZIMAOS_MONITOR_TESTING": "1",
                "ZIMAOS_MONITOR_TEST_ROOT": str(self.root),
                "ZIMAOS_MONITOR_SYSTEMCTL": str(self.systemctl),
                "TMPDIR": str(self.work_tmp),
            }
        )
        env.update(overrides)
        return env

    def seed_existing_install(self, config_text='mqtt:\n  broker: "tcp://old:1883"\n  password: "keep-secret"\n'):
        install_dir = self.root / "opt/zimaos-monitor"
        unit_dir = self.root / "etc/systemd/system"
        install_dir.mkdir(parents=True)
        unit_dir.mkdir(parents=True)
        (install_dir / "config.yaml").write_text(config_text, encoding="utf-8")
        (install_dir / "config.yaml").chmod(0o644)
        (install_dir / "zimaos-monitor").write_bytes(b"old-binary")
        (install_dir / "zimaos-monitor").chmod(0o755)
        (unit_dir / "zimaos-monitor.service").write_text(
            "old-unit\n", encoding="utf-8"
        )

    def run_noninteractive(self, **env_overrides):
        return subprocess.run(
            ["sh", str(self.stage / "install.sh")],
            cwd=self.stage,
            text=True,
            capture_output=True,
            env=self.env(**env_overrides),
        )

    def run_pty(self, responses, timeout=10, **env_overrides):
        master, slave = pty.openpty()
        initial_attrs = termios.tcgetattr(master)

        def attach_controlling_terminal():
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)

        proc = subprocess.Popen(
            ["sh", str(self.stage / "install.sh")],
            cwd=self.stage,
            stdin=slave,
            stdout=slave,
            stderr=slave,
            env=self.env(**env_overrides),
            close_fds=True,
            preexec_fn=attach_controlling_terminal,
        )
        os.close(slave)
        transcript = bytearray()
        pending = [
            (prompt.encode(), None if answer is None else answer.encode() + b"\n")
            for prompt, answer in responses
        ]
        deadline = time.monotonic() + timeout
        try:
            while time.monotonic() < deadline:
                if pending and pending[0][0] in transcript:
                    _, answer = pending.pop(0)
                    if answer is None:
                        os.killpg(proc.pid, signal.SIGINT)
                    else:
                        os.write(master, answer)
                if proc.poll() is not None:
                    break
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        transcript.extend(os.read(master, 4096))
                    except OSError:
                        break
            if proc.poll() is None:
                try:
                    proc.wait(timeout=0.5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    self.fail(
                        "installer timed out; transcript:\n"
                        + transcript.decode(errors="replace")
                    )
            while True:
                try:
                    chunk = os.read(master, 4096)
                except OSError:
                    break
                if not chunk:
                    break
                transcript.extend(chunk)
        finally:
            final_attrs = termios.tcgetattr(master)
            self.last_echo_restored = bool(final_attrs[3] & termios.ECHO) == bool(
                initial_attrs[3] & termios.ECHO
            )
            os.close(master)
        return proc.returncode, transcript.decode(errors="replace")

    def test_default_fresh_install(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", ""),
                ("MQTT username", ""),
                ("MQTT password", ""),
                ("Install and start", "yes"),
            ]
        )
        self.assertEqual(code, 0, output)
        config = self.root / "opt/zimaos-monitor/config.yaml"
        self.assertTrue(config.exists(), output)
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertIn("tcp://broker.local:1883", config.read_text(encoding="utf-8"))
        calls = (self.root / "systemctl.log").read_text(encoding="utf-8")
        self.assertIn("enable --now zimaos-monitor.service", calls)
        self.assertIn("is-enabled zimaos-monitor.service", calls)
        self.assertIn("is-active zimaos-monitor.service", calls)

    def test_invalid_values_retry(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", ""),
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", "0"),
                ("MQTT broker port", "1883"),
                ("MQTT username", ""),
                ("MQTT password", ""),
                ("Install and start", "yes"),
            ]
        )
        self.assertEqual(code, 0, output)
        self.assertIn("host is required", output)
        self.assertIn("port must be between 1 and 65535", output)

    def test_decline_leaves_no_active_install(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", ""),
                ("MQTT username", ""),
                ("MQTT password", ""),
                ("Install and start", "no"),
            ]
        )
        self.assertEqual(code, 0, output)
        self.assertFalse((self.root / "opt/zimaos-monitor/config.yaml").exists())
        self.assertFalse((self.root / "systemctl.log").exists())
        self.assertEqual(list(self.work_tmp.iterdir()), [])

    def test_first_install_rejects_non_terminal(self):
        result = subprocess.run(
            ["sh", str(self.stage / "install.sh")],
            cwd=self.stage,
            input="broker.local\n",
            text=True,
            capture_output=True,
            env=self.env(),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("terminal", result.stdout + result.stderr)
        self.assertFalse((self.root / "opt/zimaos-monitor/config.yaml").exists())

    def test_authenticated_password_is_hidden_and_preserved(self):
        password = "p:#'\\ä"
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", "2883"),
                ("MQTT username", "mqtt-user"),
                ("MQTT password", password),
                ("Install and start", "yes"),
            ]
        )
        self.assertEqual(code, 0, output)
        self.assertNotIn(password, output)
        config = (self.root / "opt/zimaos-monitor/config.yaml").read_text(
            encoding="utf-8"
        )
        self.assertIn("2883", config)
        self.assertIn("mqtt-user", config)
        self.assertIn(password, config)

    def test_password_without_username_requires_confirmation(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", ""),
                ("MQTT username", ""),
                ("MQTT password", "secret"),
                ("without a username", "no"),
            ]
        )
        self.assertEqual(code, 0, output)
        self.assertIn("without a username", output)
        self.assertNotIn("secret", output)
        self.assertFalse((self.root / "opt/zimaos-monitor/config.yaml").exists())

    def test_interrupt_at_password_restores_echo(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", ""),
                ("MQTT username", ""),
                ("MQTT password", None),
            ]
        )
        self.assertEqual(code, 130, output)
        self.assertTrue(self.last_echo_restored)
        self.assertFalse((self.root / "opt/zimaos-monitor/config.yaml").exists())

    def test_upgrade_preserves_configuration_without_prompts(self):
        self.seed_existing_install()
        config = self.root / "opt/zimaos-monitor/config.yaml"
        original = config.read_bytes()
        result = self.run_noninteractive()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(config.read_bytes(), original)
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertNotIn("MQTT broker host", result.stdout + result.stderr)
        self.assertNotIn("keep-secret", result.stdout + result.stderr)
        self.assertEqual(
            (self.root / "opt/zimaos-monitor/zimaos-monitor").read_bytes(),
            (self.stage / "zimaos-monitor").read_bytes(),
        )
        calls = (self.root / "systemctl.log").read_text(encoding="utf-8")
        self.assertIn("enable zimaos-monitor.service", calls)
        self.assertIn("restart zimaos-monitor.service", calls)

    def test_existing_config_repairs_missing_assets(self):
        config = self.root / "opt/zimaos-monitor/config.yaml"
        config.parent.mkdir(parents=True)
        config.write_text('mqtt:\n  broker: "tcp://old:1883"\n', encoding="utf-8")
        original = config.read_bytes()
        result = self.run_noninteractive()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(config.read_bytes(), original)
        self.assertTrue((self.root / "opt/zimaos-monitor/zimaos-monitor").exists())
        self.assertTrue(
            (self.root / "etc/systemd/system/zimaos-monitor.service").exists()
        )

    def test_upgrade_failure_restores_release_assets(self):
        self.seed_existing_install()
        config = self.root / "opt/zimaos-monitor/config.yaml"
        original_config = config.read_bytes()
        old_binary = (self.root / "opt/zimaos-monitor/zimaos-monitor").read_bytes()
        old_unit = (
            self.root / "etc/systemd/system/zimaos-monitor.service"
        ).read_bytes()
        result = self.run_noninteractive(ZIMAOS_MONITOR_SYSTEMCTL_FAIL="restart")
        output = result.stdout + result.stderr
        self.assertNotEqual(result.returncode, 0, output)
        self.assertEqual(config.read_bytes(), original_config)
        self.assertEqual(
            (self.root / "opt/zimaos-monitor/zimaos-monitor").read_bytes(),
            old_binary,
        )
        self.assertEqual(
            (self.root / "etc/systemd/system/zimaos-monitor.service").read_bytes(),
            old_unit,
        )
        self.assertIn("restoring previous release assets", output)
        self.assertIn("journalctl", output)
        self.assertNotIn("keep-secret", output)

    def test_fresh_activation_failure_retains_config_and_reports_recovery(self):
        code, output = self.run_pty(
            [
                ("MQTT broker host", "broker.local"),
                ("MQTT broker port", ""),
                ("MQTT username", ""),
                ("MQTT password", "failure-secret"),
                ("without a username", "yes"),
                ("Install and start", "yes"),
            ],
            ZIMAOS_MONITOR_SYSTEMCTL_FAIL="enable",
        )
        self.assertNotEqual(code, 0, output)
        config = self.root / "opt/zimaos-monitor/config.yaml"
        self.assertTrue(config.exists())
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertIn("systemctl status", output)
        self.assertIn("journalctl", output)
        self.assertNotIn("failure-secret", output)
        self.assertEqual(list(self.work_tmp.iterdir()), [])

    def test_existing_config_symlink_is_refused(self):
        install_dir = self.root / "opt/zimaos-monitor"
        install_dir.mkdir(parents=True)
        target = self.temp / "outside-config"
        target.write_text("outside-safe", encoding="utf-8")
        (install_dir / "config.yaml").symlink_to(target)
        result = self.run_noninteractive()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a regular file", result.stdout + result.stderr)
        self.assertEqual(target.read_text(encoding="utf-8"), "outside-safe")


if __name__ == "__main__":
    unittest.main(verbosity=2)
