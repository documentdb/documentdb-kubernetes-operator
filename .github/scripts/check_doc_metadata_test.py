from html.parser import HTMLParser
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from xml.etree import ElementTree


class Metadata(HTMLParser):
    def __init__(self, html):
        super().__init__()
        self.canonical = None
        self.robots = ""
        self.describedby = None
        self.schema = None
        self.json_text = None
        self.feed(html)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "link" and attrs.get("rel") == "canonical":
            self.canonical = attrs.get("href")
        if tag == "link" and attrs.get("rel") == "describedby":
            self.describedby = attrs.get("href")
        if tag == "meta" and attrs.get("name") == "robots":
            self.robots = attrs.get("content", "")
        if tag == "script" and attrs.get("type") == "application/ld+json":
            self.json_text = ""

    def handle_data(self, data):
        if self.json_text is not None:
            self.json_text += data

    def handle_endtag(self, tag):
        if tag == "script" and self.json_text is not None:
            self.schema = json.loads(self.json_text)
            self.json_text = None


class DocumentationMetadataTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.temp.cleanup)
        cls.sites = {}
        root = Path(__file__).resolve().parents[2]
        for version in ("1.1.1", "dev", "1.2.0-rc1"):
            site = Path(cls.temp.name) / version
            result = subprocess.run(
                [sys.executable, "-m", "mkdocs", "build", "--site-dir", str(site)],
                cwd=root,
                env={**os.environ, "MIKE_DOCS_VERSION": version},
                capture_output=True,
                text=True,
                timeout=60,
            )
            if result.returncode:
                raise RuntimeError(result.stdout + result.stderr)
            cls.sites[version] = site

    def test_release_has_versioned_canonicals_schema_and_populated_sitemap(self):
        site = self.sites["1.1.1"]
        url = "https://documentdb.io/documentdb-kubernetes-operator/1.1.1/v1/"
        page = Metadata((site / "v1/index.html").read_text(encoding="utf-8"))
        self.assertEqual(page.canonical, url)
        self.assertEqual(page.schema["url"], url)
        self.assertEqual(page.schema["@type"], "TechArticle")
        self.assertEqual(page.schema["about"]["softwareVersion"], "1.1.1")
        self.assertEqual(
            page.describedby,
            "https://documentdb.io/documentdb-kubernetes-operator/1.1.1/llms.txt",
        )
        self.assertNotIn("noindex", page.robots)
        locations = [
            node.text
            for node in ElementTree.parse(site / "sitemap.xml").iter(
                "{http://www.sitemaps.org/schemas/sitemap/0.9}loc"
            )
        ]
        self.assertIn(url, locations)
        self.assertTrue(all(location.startswith(url.rsplit("v1/", 1)[0]) for location in locations))
        self.assertFalse(any("/preview/" in location for location in locations))

    def test_unreleased_snapshots_are_labeled_noindex_and_not_in_sitemaps(self):
        for version, label in (("dev", "Development"), ("1.2.0-rc1", "Prerelease")):
            with self.subTest(version=version):
                site = self.sites[version]
                html = (site / "v1/index.html").read_text(encoding="utf-8")
                page = Metadata(html)
                self.assertIn("noindex", page.robots)
                self.assertIn(f"{label} documentation", html)
                self.assertIn(f"/{version}/v1/", page.canonical)
                self.assertEqual(
                    list(ElementTree.parse(site / "sitemap.xml").iter(
                        "{http://www.sitemaps.org/schemas/sitemap/0.9}loc"
                    )),
                    [],
                )
        dev = Metadata((self.sites["dev"] / "v1/index.html").read_text(encoding="utf-8"))
        self.assertNotIn("softwareVersion", dev.schema["about"])


if __name__ == "__main__":
    unittest.main()
