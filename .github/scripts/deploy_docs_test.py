from contextlib import chdir, redirect_stdout
from io import StringIO
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from deploy_docs import Deployment, deploy, main, plan_deployment, stamp_docs


def snapshot(version, release=None, aliases=()):
    entry = {"version": version, "title": version, "aliases": list(aliases)}
    if release is not None:
        entry["properties"] = {"release": release}
    return entry


class DeploymentPolicyTests(unittest.TestCase):
    def test_stable_minor_path_and_numeric_ordering(self):
        versions = [snapshot("1.1", "1.1.9", ("latest",))]
        self.assertEqual(
            plan_deployment("1.1.10", False, versions), Deployment("1.1", True)
        )
        self.assertEqual(
            plan_deployment("1.10.0", False, versions), Deployment("1.10", True)
        )

    def test_old_patch_skipped_and_equal_patch_can_be_repaired(self):
        for entry in (snapshot("1.1", "1.1.10"), snapshot("1.1.10")):
            with self.subTest(entry=entry):
                self.assertIsNone(plan_deployment("1.1.2", False, [entry]))
                self.assertEqual(
                    plan_deployment("1.1.10", False, [entry]),
                    Deployment("1.1", True),
                )

    def test_older_minor_cannot_take_latest_even_without_an_alias(self):
        for entry in (snapshot("1.2", "1.2.0"), snapshot("1.2.0")):
            with self.subTest(entry=entry):
                self.assertEqual(
                    plan_deployment("1.1.12", False, [entry]),
                    Deployment("1.1", False),
                )

    def test_prereleases_remain_separate_and_do_not_affect_stable_ordering(self):
        versions = [
            snapshot("dev"),
            snapshot("1.0-rc1"),
            snapshot("2.0.0-rc.1", "2.0.0-rc.1"),
        ]
        self.assertEqual(
            plan_deployment("2.0.0-rc.2", True, versions),
            Deployment("2.0.0-rc.2", False),
        )
        self.assertEqual(
            plan_deployment("1.1.2", False, versions), Deployment("1.1", True)
        )

    def test_rejects_bad_tags_and_mislabeled_prereleases(self):
        for tag, prerelease in (
            ("", False), ("1.1", False), ("v1.1.2", False),
            ("01.1.2", False), ("1.1.2-rc.1", False),
            ("1.1.2+build", False), ("1.1.2", True),
            ("dev", True), ("1.1.2-01", True),
        ):
            with self.subTest(tag=tag, prerelease=prerelease):
                with self.assertRaises(ValueError):
                    plan_deployment(tag, prerelease, [])

    def test_unknown_or_inconsistent_minor_provenance_fails_closed(self):
        for entry in (
            snapshot("1.1"), snapshot("1.1", "1.2.0"),
            snapshot("1.1", "1.1.2-rc.1"),
        ):
            with self.subTest(entry=entry):
                with self.assertRaises(ValueError):
                    plan_deployment("1.1.2", False, [entry])

    def test_invalid_latest_state_fails_closed(self):
        for versions in (
            [snapshot("dev", aliases=("latest",))],
            [snapshot("2.0.0-rc.1", aliases=("latest",))],
            [snapshot("1.1.1", aliases=("latest",)),
             snapshot("1.2.0", aliases=("latest",))],
        ):
            with self.subTest(versions=versions):
                with self.assertRaises(ValueError):
                    plan_deployment("1.1.2", False, versions)

    def test_bad_prerelease_flag_fails_before_accessing_published_state(self):
        with (
            patch("sys.argv", [
                "deploy_docs.py", "deploy", "--tag", "1.1.2", "--prerelease", "yes",
            ]),
            patch("sys.stderr", new=StringIO()),
            patch("deploy_docs.subprocess.run") as run,
        ):
            with self.assertRaises(SystemExit) as error:
                main()
            self.assertEqual(error.exception.code, 2)
            run.assert_not_called()

    def test_stamp_preserves_full_release_for_install_and_crd_urls(self):
        with tempfile.TemporaryDirectory() as directory:
            docs = Path(directory)
            page = docs / "install.md"
            template = (
                "helm install documentdb --version <release-version>\n"
                "https://raw.githubusercontent.com/documentdb/"
                "documentdb-kubernetes-operator/<release-version>/"
                "operator/documentdb-helm-chart/crds/documentdb.io_documentdbs.yaml\n"
            )
            for version in ("1.1.2", "1.2.0-rc.1"):
                page.write_text(template, encoding="utf-8")
                stamp_docs(version, docs)
                self.assertEqual(
                    page.read_text(encoding="utf-8"),
                    template.replace("<release-version>", version),
                )
            with self.assertRaises(ValueError):
                stamp_docs("1.1", docs)


class MikeDeploymentTests(unittest.TestCase):
    """Exercise the real mike CLI against a disposable local remote, never GitHub."""

    def run_command(self, *command):
        result = subprocess.run(
            command, cwd=self.repo, env=self.env, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        if result.returncode:
            self.fail(f"{command!r} failed:\n{result.stdout}")
        return result.stdout.strip()

    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        self.repo = root / "repo"
        self.repo.mkdir()
        self.env = {
            **os.environ,
            "GIT_AUTHOR_NAME": "Docs test",
            "GIT_AUTHOR_EMAIL": "docs@example.invalid",
            "GIT_COMMITTER_NAME": "Docs test",
            "GIT_COMMITTER_EMAIL": "docs@example.invalid",
        }
        self.run_command("git", "init", "--bare", str(root / "remote.git"))
        self.run_command("git", "init", "-b", "main")
        self.run_command("git", "remote", "add", "origin", str(root / "remote.git"))
        (self.repo / "mkdocs.yml").write_text(
            "site_name: Test\nextra:\n  version:\n    provider: mike\n",
            encoding="utf-8",
        )
        self.docs = self.repo / "docs"
        self.docs.mkdir()
        self.page = self.docs / "index.md"
        self.page.write_text("# Legacy patch snapshot\n", encoding="utf-8")
        self.run_command("git", "add", ".")
        self.run_command("git", "commit", "-m", "Test fixture")
        self.run_command("mike", "deploy", "1.1.1", "latest", "--push")
        self.run_command("mike", "set-default", "latest", "--push")

    def versions(self):
        return json.loads(
            self.run_command("git", "show", "origin/gh-pages:versions.json")
        )

    def publish(self, tag, prerelease=False):
        self.page.write_text(
            "# Release docs\n\nhelm install documentdb --version <release-version>\n",
            encoding="utf-8",
        )
        stamp_docs(tag, self.docs)
        with chdir(self.repo), patch.dict(os.environ, self.env):
            with redirect_stdout(StringIO()):
                deploy(tag, prerelease)

    def test_publication_order_aliases_and_legacy_urls(self):
        legacy = self.run_command("git", "rev-parse", "origin/gh-pages:1.1.1")
        self.publish("1.1.2")
        self.assertIn(snapshot("1.1", "1.1.2", ("latest",)), self.versions())
        html = self.run_command("git", "show", "origin/gh-pages:1.1/index.html")
        self.assertIn("--version 1.1.2", html)

        self.publish("1.1.10")
        newest = self.run_command("git", "rev-parse", "origin/gh-pages")
        self.publish("1.1.3")
        self.assertEqual(
            self.run_command("git", "rev-parse", "origin/gh-pages"), newest
        )
        latest_redirect = self.run_command("git", "show", "origin/gh-pages:index.html")
        self.publish("2.0.0-rc.1", prerelease=True)
        self.publish("1.0.5")
        self.assertIn(snapshot("1.1", "1.1.10", ("latest",)), self.versions())
        self.assertIn(snapshot("1.0", "1.0.5"), self.versions())
        self.assertIn(snapshot("2.0.0-rc.1", "2.0.0-rc.1"), self.versions())
        self.assertEqual(
            self.run_command("git", "show", "origin/gh-pages:index.html"),
            latest_redirect,
        )

        self.publish("1.2.0")
        self.publish("1.1.11")
        self.run_command("mike", "deploy", "dev", "--push")
        versions = self.versions()
        self.assertIn(snapshot("1.2", "1.2.0", ("latest",)), versions)
        self.assertIn(snapshot("1.1", "1.1.11"), versions)
        self.assertIn(snapshot("dev"), versions)
        self.assertIn(snapshot("1.1.1"), versions)
        self.assertEqual(
            self.run_command("git", "rev-parse", "origin/gh-pages:1.1.1"), legacy
        )
        self.assertEqual(
            self.run_command("git", "show", "origin/gh-pages:latest"), "1.2"
        )


if __name__ == "__main__":
    unittest.main()
