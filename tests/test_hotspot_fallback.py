import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "deploy" / "scripts" / "ups-pi-node-hotspot-fallback"
NMCLI_MOCK = ROOT / "tests" / "fixtures" / "nmcli-hotspot-mock"


class HotspotFallbackTests(unittest.TestCase):
    def run_fallback(self, state, connection="--", profile_exists=True):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            log_path = temporary_path / "nmcli.log"
            config_path = temporary_path / "main.conf"
            config_path.write_text(
                "[wifi]\n"
                "hotspot_ssid = Test-Node\n"
                "hotspot_password = test-password\n",
                encoding="utf-8",
            )
            env = os.environ.copy()
            env.update(
                {
                    "UPS_PI_NODE_CONFIG_FILE": str(config_path),
                    "UPS_PI_NODE_NMCLI": str(NMCLI_MOCK),
                    "UPS_PI_NODE_TEST_NMCLI_LOG": str(log_path),
                    "UPS_PI_NODE_TEST_WIFI_STATE": state,
                    "UPS_PI_NODE_TEST_WIFI_CONNECTION": connection,
                    "UPS_PI_NODE_TEST_PROFILE_EXISTS": "1" if profile_exists else "0",
                }
            )
            completed = subprocess.run(
                [str(SCRIPT)],
                env=env,
                capture_output=True,
                check=False,
                text=True,
            )
            calls = log_path.read_text(encoding="utf-8").splitlines()
            return completed, calls

    def test_active_hotspot_is_not_restarted(self):
        completed, calls = self.run_fallback("connected", "ups-pi-node-hotspot")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(calls, ["-t -f DEVICE,STATE,CONNECTION dev status"])

    def test_active_client_connection_is_not_touched(self):
        completed, calls = self.run_fallback("connected", "MyRouter")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(calls, ["-t -f DEVICE,STATE,CONNECTION dev status"])

    def test_connection_attempt_is_not_interrupted(self):
        completed, calls = self.run_fallback("connecting", "MyRouter")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(calls, ["-t -f DEVICE,STATE,CONNECTION dev status"])

    def test_disconnected_interface_activates_existing_hotspot_once(self):
        completed, calls = self.run_fallback("disconnected")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("connection show ups-pi-node-hotspot", calls)
        self.assertTrue(any(call.startswith("connection modify ups-pi-node-hotspot ") for call in calls))
        self.assertEqual(calls[-1], "connection up ups-pi-node-hotspot")

    def test_missing_profile_is_created_before_activation(self):
        completed, calls = self.run_fallback("disconnected", profile_exists=False)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn(
            "connection add type wifi ifname wlan0 con-name ups-pi-node-hotspot autoconnect no ssid Test-Node",
            calls,
        )
        self.assertEqual(calls[-1], "connection up ups-pi-node-hotspot")

    def test_unavailable_interface_does_not_try_activation(self):
        completed, calls = self.run_fallback("unavailable")

        self.assertNotEqual(completed.returncode, 0)
        self.assertEqual(calls, ["-t -f DEVICE,STATE,CONNECTION dev status"])
        self.assertIn("not ready", completed.stderr)


if __name__ == "__main__":
    unittest.main()
