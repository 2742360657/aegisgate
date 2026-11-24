import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

CHECK = Path(__file__).resolve().parents[1] / "check_public.py"
PRIVATE = "/" + "home" + "/sample-owner/project"


class PublicBoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="aegisgate-boundary-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
        self.git("init", "-q")
        self.git("config", "user.name", "Test Developer")
        self.git("config", "user.email", "developer@example.org")

    def git(self, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=self.root, env=self.env, stderr=subprocess.PIPE).decode().strip()

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def check(self, mode, stdin=None):
        return subprocess.run([sys.executable, str(CHECK), mode], cwd=self.root, env=self.env, input=stdin, text=True, capture_output=True)

    def test_index_is_scanned_instead_of_working_copy(self):
        self.write("README.md", "portable documentation\n")
        self.git("add", "README.md")
        self.write("README.md", PRIVATE)
        self.assertEqual(self.check("--staged").returncode, 0)

    def test_unstaged_cleanup_cannot_hide_private_index(self):
        self.write("README.md", PRIVATE)
        self.git("add", "README.md")
        self.write("README.md", "portable documentation\n")
        result = self.check("--staged")
        self.assertEqual(result.returncode, 1)
        self.assertIn("personal home path", result.stderr)
        self.assertNotIn(PRIVATE, result.stderr)

    def test_local_only_filename_is_rejected(self):
        self.write(".private/context.md", "no personal content")
        self.git("add", ".private/context.md")
        self.assertEqual(self.check("--staged").returncode, 1)

    def test_private_file_name_and_token_are_rejected(self):
        self.write(".env", "TOKEN=" + "ghp_" + "a" * 36)
        self.git("add", ".env")
        result = self.check("--staged")
        self.assertEqual(result.returncode, 1)
        self.assertIn("GitHub token", result.stderr)
        self.assertNotIn("a" * 36, result.stderr)

    def test_history_catches_deleted_private_content(self):
        self.write("README.md", PRIVATE)
        self.git("add", "README.md")
        self.git("commit", "-qm", "docs: add draft")
        self.write("README.md", "portable\n")
        self.git("add", "README.md")
        self.git("commit", "-qm", "docs: remove draft")
        self.assertEqual(self.check("--staged").returncode, 0)
        self.assertEqual(self.check("--history").returncode, 1)
        tip = self.git("rev-parse", "HEAD")
        result = self.check("--pre-push", f"refs/heads/main {tip} refs/heads/main {'0' * 40}\n")
        self.assertEqual(result.returncode, 1)

    def test_commit_message_is_also_scanned(self):
        self.write("README.md", "portable\n")
        self.git("add", "README.md")
        self.git("commit", "-qm", "docs: notes at " + PRIVATE)
        self.assertEqual(self.check("--history").returncode, 1)

    def test_ref_deletion_does_not_scan_unsent_history(self):
        result = self.check("--pre-push", f"(delete) {'0' * 40} refs/heads/old {'1' * 40}\n")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_invalid_push_input_fails_closed(self):
        self.assertEqual(self.check("--pre-push", "bad input\n").returncode, 2)

    def test_windows_machine_paths_are_blocked_but_http_urls_are_allowed(self):
        for private in ["D" + ":" + "\\" + "projects", "\\" * 2 + "wsl.localhost" + "\\" + "Linux"]:
            self.write("README.md", private)
            self.git("add", "README.md")
            self.assertEqual(self.check("--staged").returncode, 1)
        self.write("README.md", "https://example.org and http://127.0.0.1:8080")
        self.git("add", "README.md")
        self.assertEqual(self.check("--staged").returncode, 0)

    def test_existing_hooks_are_not_replaced(self):
        existing = self.root / ".git/hooks/pre-commit"
        existing.write_text("preserve this custom hook")
        install = CHECK.with_name("install-hooks.sh")
        result = subprocess.run(["bash", str(install)], cwd=self.root, env=self.env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(existing.read_text(), "preserve this custom hook")

    def test_installed_hook_blocks_a_private_commit(self):
        hook_root = self.root / ".githooks"
        hook_root.mkdir()
        source_hooks = CHECK.parents[1] / ".githooks"
        for name in ["pre-commit", "pre-push"]:
            (hook_root / name).write_bytes((source_hooks / name).read_bytes())
        target = self.root / "scripts/check_public.py"
        target.parent.mkdir()
        target.write_bytes(CHECK.read_bytes())
        install = CHECK.with_name("install-hooks.sh")
        subprocess.run(["bash", str(install)], cwd=self.root, env=self.env, capture_output=True, check=True)
        self.write("README.md", PRIVATE)
        self.git("add", "README.md")
        result = subprocess.run(["git", "commit", "-qm", "docs: draft"], cwd=self.root, env=self.env, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"BLOCK", result.stderr)

    def test_placeholder_email_is_an_explicit_nonblocking_note(self):
        self.git("config", "user.email", "developer@localhost")
        self.write("README.md", "portable\n")
        self.git("add", "README.md")
        self.git("commit", "-qm", "docs: initial")
        result = self.check("--history")
        self.assertEqual(result.returncode, 0)
        self.assertIn("review author identity", result.stderr)


if __name__ == "__main__":
    unittest.main()
