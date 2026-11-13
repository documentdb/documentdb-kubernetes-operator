from io import BytesIO, StringIO
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from check_doc_links import check_link, DOCS_ROOT, main


class DocumentationLinkTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.site = Path(self.temp.name)

    def page(self, path, html):
        target = self.site / path / "index.html"
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(html, encoding="utf-8")

    def test_follows_compatibility_redirect_and_preserves_anchor(self):
        self.page(
            "preview/getting-started",
            '<meta http-equiv="refresh" content="0; url=../../v1/getting-started/">',
        )
        self.page("v1/getting-started", '<h1 id="prerequisites">Before you start</h1>')
        url = DOCS_ROOT + "latest/preview/getting-started/#prerequisites"
        self.assertEqual(
            check_link(url, self.site),
            DOCS_ROOT + "latest/v1/getting-started/#prerequisites",
        )

    def test_http_200_redirect_to_missing_page_fails(self):
        self.page("preview", '<meta http-equiv="refresh" content="0; url=../missing/">')
        with self.assertRaises(FileNotFoundError):
            check_link(DOCS_ROOT + "latest/preview/", self.site)

    def test_rejects_missing_anchor(self):
        self.page("v1", '<h1 id="installation">Installation</h1>')
        with self.assertRaisesRegex(ValueError, "missing anchor"):
            check_link(DOCS_ROOT + "latest/v1/#missing", self.site)

    def test_rejects_redirect_loop(self):
        self.page("v1", '<meta http-equiv="refresh" content="0; url=../v1/">')
        with self.assertRaisesRegex(ValueError, "redirect loop"):
            check_link(DOCS_ROOT + "latest/v1/", self.site)

    def test_rejects_development_fallback(self):
        self.page("v1", '<meta http-equiv="refresh" content="0; url=../../dev/v1/">')
        with self.assertRaisesRegex(ValueError, "development"):
            check_link(DOCS_ROOT + "latest/v1/", self.site)

    def test_checks_http_redirect_destination(self):
        response = BytesIO(b"<h1>Development docs</h1>")
        response.geturl = lambda: DOCS_ROOT + "dev/v1/"
        with patch("check_doc_links.urlopen", return_value=response):
            with self.assertRaisesRegex(ValueError, "development"):
                check_link(DOCS_ROOT + "latest/")

    def test_checks_anchor_added_by_http_redirect(self):
        response = BytesIO(b'<h1 id="installation">Installation</h1>')
        response.geturl = lambda: DOCS_ROOT + "latest/v1/#missing"
        with patch("check_doc_links.urlopen", return_value=response):
            with self.assertRaisesRegex(ValueError, "missing anchor"):
                check_link(DOCS_ROOT + "latest/")

    def test_readme_development_link_cannot_hide_among_release_links(self):
        readme = self.site / "README.md"
        readme.write_text(
            f"[Docs]({DOCS_ROOT}latest/) [Install]({DOCS_ROOT}dev/v1/)",
            encoding="utf-8",
        )
        response = BytesIO(b"<h1>Released docs</h1>")
        response.geturl = lambda: DOCS_ROOT + "latest/"
        errors = StringIO()
        with (
            patch("sys.argv", ["check_doc_links.py", "--readme", str(readme)]),
            patch("check_doc_links.urlopen", return_value=response),
            patch("sys.stdout", new=StringIO()),
            patch("sys.stderr", new=errors),
        ):
            self.assertEqual(main(), 1)
        self.assertIn("development", errors.getvalue())


if __name__ == "__main__":
    unittest.main()
