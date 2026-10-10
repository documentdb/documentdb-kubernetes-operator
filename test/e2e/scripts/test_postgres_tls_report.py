"""Regression checks using report records emitted by the pinned Ginkgo CLI."""
import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


class AcceptanceReportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.directory.cleanup)
        cls.root = Path(__file__).resolve().parents[1]
        cls.report = Path(cls.directory.name) / "report.json"
        subprocess.run(
            [
                "ginkgo", "--dry-run", "--procs=1",
                f"--json-report={cls.report}",
                "./tests/multicluster_postgres_tls",
            ],
            cwd=cls.root, check=True, stdout=subprocess.DEVNULL,
        )
        cls.dry_run = json.loads(cls.report.read_text(encoding="utf-8"))
        cls.passing = copy.deepcopy(cls.dry_run)
        cls.passing[0]["SuiteConfig"]["DryRun"] = False

    def check_report(self, report, passes):
        self.report.write_text(json.dumps(report), encoding="utf-8")
        result = subprocess.run(
            ["python3", str(self.root / "scripts/check-postgres-tls-report.py"), str(self.report)],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode == 0, passes, result.stdout + result.stderr)

    def test_all_five_pass(self):
        self.check_report(self.passing, True)

    def test_dry_run_is_not_runtime_evidence(self):
        self.check_report(self.dry_run, False)

    def test_empty_and_filtered_reports(self):
        for count in (0, 4):
            with self.subTest(count=count):
                report = copy.deepcopy(self.passing)
                report[0]["SpecReports"] = report[0]["SpecReports"][:count]
                self.check_report(report, False)

    def test_skipped_failed_pending_rotation(self):
        for state in ("skipped", "failed", "pending"):
            with self.subTest(state=state):
                report = copy.deepcopy(self.passing)
                report[0]["SpecReports"][-1]["State"] = state
                self.check_report(report, False)

    def test_duplicate_or_missing_names(self):
        report = copy.deepcopy(self.passing)
        report[0]["SpecReports"][-1]["LeafNodeText"] = report[0]["SpecReports"][0]["LeafNodeText"]
        self.check_report(report, False)
        report[0]["SpecReports"][-1]["LeafNodeText"] = "unrelated spec"
        self.check_report(report, False)

    def test_suite_failure_and_multiple_suites(self):
        report = copy.deepcopy(self.passing)
        report[0]["SuiteSucceeded"] = False
        self.check_report(report, False)
        self.check_report(self.passing + self.passing, False)

    def test_malformed_reports(self):
        for report in ([], {}, [{"SuiteSucceeded": True}], [{"SpecReports": None}]):
            with self.subTest(report=report):
                self.check_report(report, False)
        self.report.write_text("not JSON", encoding="utf-8")
        result = subprocess.run(
            ["python3", str(self.root / "scripts/check-postgres-tls-report.py"), str(self.report)],
            capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
