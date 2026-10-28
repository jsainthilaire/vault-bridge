"""Exercise the demo's process boundary without starting an LLM session."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("manual_test", Path(__file__).with_name("manual-test.py"))
demo = importlib.util.module_from_spec(spec)
spec.loader.exec_module(demo)


class ManualTestScriptTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="vault-bridge-script-tests-")
        self.addCleanup(self.temporary.cleanup)
        base = Path(self.temporary.name).resolve()
        self.repo = base / 'repo "quoted" $(not-a-command)'
        self.repo.mkdir()
        self.vault = base / "Vault notes 'quoted'"
        for project in ("vault-bridge", "agent-rig"):
            root = self.vault / "03-Projects" / project
            root.mkdir(parents=True)
            (root / "index.md").write_text(f"# {project}\nKeep this note unchanged.\n", encoding="utf-8")
        (self.repo / ".codex").mkdir()
        (self.repo / ".codex/config.toml").write_text("# existing client settings\n", encoding="utf-8")
        (self.repo / "vault-bridge.local.yaml").write_text("# existing vault config\n", encoding="utf-8")
        self.original = self.snapshot()
        self.calls = []
        self.config_paths = []

    def snapshot(self):
        base = Path(self.temporary.name)
        return {str(path.relative_to(base)): path.read_bytes() for path in base.rglob("*") if path.is_file()}

    def fake_process(self, command, *, cwd, env, check):
        self.assertEqual(cwd, self.repo)
        self.assertFalse(check)
        self.assertIsInstance(command, list)
        self.calls.append((command, env))
        if "--config" in command:
            config = Path(command[command.index("--config") + 1])
            self.config_paths.append(config)
            with config.open(encoding="utf-8") as file:
                data = json.load(file)
            self.assertEqual(data, {
                "version": 1, "vault_root": str(self.vault),
                "projects": {"vault-bridge": {"folder": "03-Projects/vault-bridge", "shared_read_paths": []}},
            })
            if os.name != "nt":
                self.assertEqual(stat.S_IMODE(config.stat().st_mode), 0o600)
        if "test" in command:
            self.assertEqual(command[1:], [
                "test", "./cmd/vault-bridge", "-run", "^TestExistingVaultStdioAcceptance$", "-count=1", "-v",
            ])
            self.assertEqual(env["VAULT_BRIDGE_ACCEPTANCE_PROJECT"], "vault-bridge")
            self.assertEqual(env["VAULT_BRIDGE_ACCEPTANCE_BINARY"], str(self.repo / ".bin/vault-bridge"))
            self.assertEqual(env["VAULT_BRIDGE_ACCEPTANCE_DENIED"], "03-Projects/agent-rig/index.md")
            self.assertEqual(Path(env["VAULT_BRIDGE_ACCEPTANCE_CONFIG"]), self.config_paths[-1])
            self.assertTrue(self.config_paths[-1].exists())
        if "-c" in command:
            setting = command[command.index("-c") + 1]
            prefix = f"mcp_servers.{demo.SERVER_NAME}={{command="
            self.assertTrue(setting.startswith(prefix))
            # Decode the actual quoted values rather than matching a hand-built
            # expected command; shell metacharacters must remain literal data.
            binary, end = json.JSONDecoder().raw_decode(setting[len(prefix):])
            rest = setting[len(prefix) + end:]
            self.assertTrue(rest.startswith(",args="))
            args, _ = json.JSONDecoder().raw_decode(rest[len(",args="):])
            self.assertEqual(binary, str(self.repo / ".bin/vault-bridge"))
            self.assertEqual(args, [
                "serve", "--config", str(self.config_paths[-1]), "--project", "vault-bridge", "--transport", "stdio",
            ])
            self.assertTrue(self.config_paths[-1].exists(), "temporary config expired before Codex exited")
        return subprocess.CompletedProcess(command, 0)

    def invoke(self, *arguments, runner=None, available=None, tty=True):
        if available is None:
            available = {"go": "/tools/go", "codex": "/tools/codex"}
        output, diagnostics = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(diagnostics), \
                patch.object(demo, "REPO_ROOT", self.repo), \
                patch.object(demo.shutil, "which", side_effect=available.get), \
                patch.object(demo.subprocess, "run", side_effect=runner or self.fake_process), \
                patch.object(sys.stdin, "isatty", return_value=tty), \
                patch.object(sys.stdout, "isatty", return_value=tty):
            status = demo.main(["--vault", str(self.vault), *arguments])
        return status, output.getvalue(), diagnostics.getvalue()

    def assert_preserved_and_cleaned(self):
        self.assertEqual(self.snapshot(), self.original)
        for config in self.config_paths:
            self.assertFalse(config.exists())
            self.assertFalse(config.parent.exists())

    def test_check_only_needs_no_codex_or_terminal(self):
        status, output, errors = self.invoke("--check-only", available={"go": "/tools/go"}, tty=False)
        self.assertEqual((status, errors), (0, ""))
        self.assertEqual(len(self.calls), 3)
        self.assertIn("SDK preflight passed", output)
        self.assertIn("A Codex connection has not been tested", output)
        self.assertFalse(any(command[0] == "/tools/codex" for command, _ in self.calls))
        self.assert_preserved_and_cleaned()

    def test_interactive_launch_has_session_only_scope_and_prompt(self):
        status, output, errors = self.invoke()
        self.assertEqual((status, errors), (0, ""))
        self.assertEqual(len(self.calls), 5)
        settings, _ = self.calls[3]
        self.assertEqual(settings[-4:], ["mcp", "get", demo.SERVER_NAME, "--json"])
        launch, env = self.calls[-1]
        self.assertIsNone(env, "Codex must retain the user's existing environment")
        self.assertEqual(launch[:7], [
            "/tools/codex", "-C", str(self.repo), "--sandbox", "read-only", "--no-alt-screen", "-c",
        ])
        prompt = launch[-1]
        for expected in (demo.SERVER_NAME, "project_context", "document_list", "document_read", "document_search",
                         "feature_list", "decision_list", "03-Projects/agent-rig/index.md", "permission_denied"):
            self.assertIn(expected, prompt)
        self.assertIn("Do not edit files", prompt)
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", launch)
        self.assertIn("Temporary test configuration removed", output)
        self.assert_preserved_and_cleaned()

    def test_failed_preflight_never_launches_codex(self):
        for stage in ("build", "config", "test"):
            with self.subTest(stage=stage):
                self.calls.clear()
                def fail_stage(command, **kwargs):
                    result = self.fake_process(command, **kwargs)
                    if stage in command:
                        result.returncode = 7
                    return result
                status, output, errors = self.invoke(runner=fail_stage)
                self.assertEqual(status, 7)
                self.assertIn("failed (exit 7)", errors)
                self.assertNotIn("SDK preflight passed", output)
                self.assertFalse(any(command[0] == "/tools/codex" for command, _ in self.calls))
                self.assert_preserved_and_cleaned()

    def test_invalid_inputs_fail_before_running_any_command(self):
        for flags, message in (
            (["--project", "../escape"], "lowercase kebab-case"),
            (["--project", "x" * 65], "64 bytes"),
            (["--denied-project", "not/a/project"], "lowercase kebab-case"),
            (["--denied-project", "vault-bridge"], "must differ"),
            (["--project", "missing"], "Missing project index"),
            (["--denied-project", "missing"], "Missing project index"),
            (["--vault", str(self.vault / "absent")], "Vault does not exist"),
            (["--vault", str(self.vault / "03-Projects/vault-bridge/index.md")], "must be a directory"),
        ):
            with self.subTest(flags=flags):
                status, _, errors = self.invoke(*flags, "--check-only")
                self.assertEqual(status, 1)
                self.assertIn(message, errors)
                self.assertEqual(self.calls, [])
                self.assert_preserved_and_cleaned()

    def test_missing_go_and_codex_have_actionable_errors(self):
        for flags, available, message in (
            (["--check-only"], {}, "Go 1.25 or later"),
            ([], {"go": "/tools/go"}, "Codex CLI is required"),
        ):
            with self.subTest(flags=flags):
                status, _, errors = self.invoke(*flags, available=available)
                self.assertEqual(status, 1)
                self.assertIn(message, errors)
                self.assertEqual(self.calls, [])

    def test_noninteractive_default_stops_before_build(self):
        status, _, errors = self.invoke(tty=False)
        self.assertEqual(status, 1)
        self.assertIn("--check-only", errors)
        self.assertEqual(self.calls, [])

    def test_interrupt_during_check_or_codex_removes_temporary_config(self):
        for stage in ("test", "interactive"):
            with self.subTest(stage=stage):
                def interrupt(command, **kwargs):
                    result = self.fake_process(command, **kwargs)
                    if (stage == "test" and "test" in command) or (stage == "interactive" and "--sandbox" in command):
                        raise KeyboardInterrupt()
                    return result
                status, _, errors = self.invoke(runner=interrupt)
                self.assertEqual(status, 130)
                self.assertIn("interrupted", errors)
                self.assert_preserved_and_cleaned()

    def test_codex_exit_and_startup_failures_are_not_reported_as_success(self):
        for failure in ("settings", "session", "signal"):
            with self.subTest(failure=failure):
                self.calls.clear()
                def failed_codex(command, **kwargs):
                    result = self.fake_process(command, **kwargs)
                    if command[0] == "/tools/codex":
                        if failure == "settings" or "--sandbox" in command:
                            result.returncode = -15 if failure == "signal" else 42
                    return result
                status, output, errors = self.invoke(runner=failed_codex)
                self.assertEqual(status, 143 if failure == "signal" else 42)
                self.assertIn("failed", errors)
                self.assertNotIn("Codex exited.", output)
                if failure == "settings":
                    self.assertFalse(any("--sandbox" in command for command, _ in self.calls))
                self.assert_preserved_and_cleaned()

    def test_permission_failure_is_compact_and_cleans_config(self):
        def no_permission(command, **kwargs):
            result = self.fake_process(command, **kwargs)
            if "test" in command:
                raise PermissionError("SDK process could not start")
            return result
        status, _, errors = self.invoke("--check-only", runner=no_permission)
        self.assertEqual(status, 1)
        self.assertIn("SDK process could not start", errors)
        self.assertNotIn("Traceback", errors)
        self.assert_preserved_and_cleaned()

    def test_defaults_are_based_on_script_location(self):
        with patch.object(demo, "REPO_ROOT", self.repo):
            args = demo.parse_args([])
        self.assertEqual(args.vault, self.repo.parent / "DevVault")
        self.assertEqual(args.project, "vault-bridge")
        self.assertEqual(args.denied_project, "agent-rig")
        self.assertFalse(args.check_only)


if __name__ == "__main__":
    unittest.main()
