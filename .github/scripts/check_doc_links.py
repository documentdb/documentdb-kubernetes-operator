#!/usr/bin/env python3
"""Check README and llms.txt release-doc links, including static-hosting redirects."""

import argparse
from html.parser import HTMLParser
from pathlib import Path
import re
import sys
import time
from urllib.parse import unquote, urljoin, urlsplit, urlunsplit
from urllib.request import Request, urlopen


DOCS_ROOT = "https://documentdb.io/documentdb-kubernetes-operator/"


class Page(HTMLParser):
    def __init__(self, html):
        super().__init__()
        self.redirect = None
        self.anchors = set()
        self.feed(html)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if attrs.get("id"):
            self.anchors.add(attrs["id"])
        if tag == "a" and attrs.get("name"):
            self.anchors.add(attrs["name"])
        if tag == "meta" and attrs.get("http-equiv", "").lower() == "refresh":
            match = re.search(
                r"(?:^|;)\s*url\s*=\s*(.+)", attrs.get("content", ""), re.IGNORECASE
            )
            if match:
                self.redirect = match[1].strip().strip("'\"")


def require_release_url(url):
    if not url.startswith(DOCS_ROOT):
        raise ValueError(f"redirect leaves operator documentation: {url}")
    if unquote(urlsplit(url).path).split("/")[2] == "dev":
        raise ValueError(f"released documentation redirects to development: {url}")


def check_link(url, site_dir=None):
    seen = set()
    for _ in range(10):
        require_release_url(url)
        if url in seen:
            raise ValueError(f"redirect loop: {url}")
        seen.add(url)
        fragment = urlsplit(url).fragment
        if site_dir is not None:
            # Treat the candidate build as the selected documentation snapshot.
            relative = unquote(urlsplit(url).path).removeprefix(
                urlsplit(DOCS_ROOT).path
            )
            _, _, relative = relative.partition("/")
            path = (site_dir / relative).resolve()
            if not path.is_relative_to(site_dir.resolve()):
                raise ValueError(f"redirect leaves the built site: {url}")
            if path.is_dir():
                path /= "index.html"
            html = path.read_text(encoding="utf-8")
        else:
            request = Request(url, headers={"User-Agent": "DocumentDB-doc-link-check"})
            with urlopen(request, timeout=15) as response:
                url = response.geturl()
                require_release_url(url)
                fragment = urlsplit(url).fragment or fragment
                html = response.read().decode("utf-8")
        page = Page(html)
        if page.redirect is not None:
            target = urlsplit(urljoin(url, page.redirect))
            url = urlunsplit(target._replace(fragment=target.fragment or fragment))
            continue
        if fragment and unquote(fragment) not in page.anchors:
            raise ValueError(f"missing anchor #{fragment}: {url}")
        return url
    raise ValueError(f"too many redirects: {url}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--readme", type=Path, default=Path("README.md"))
    parser.add_argument("--site-dir", type=Path, help="Check a local MkDocs build")
    parser.add_argument("--attempts", type=int, default=1, help="Bounded deploy retries")
    args = parser.parse_args()
    if args.attempts < 1:
        parser.error("--attempts must be positive")
    sources = [args.readme]
    llms = args.readme.parent / "docs/operator-public-documentation/llms.txt"
    if llms.is_file():
        sources.append(llms)
    links = set(
        re.findall(
            re.escape(DOCS_ROOT) + r"[^\s<>)]*",
            "\n".join(source.read_text(encoding="utf-8") for source in sources),
        )
    )
    if not links:
        parser.error("README contains no released operator-documentation links")
    if args.site_dir is not None:
        links.update(
            DOCS_ROOT + "latest/" + path
            for path in ("v1/", "preview/", "v1/getting-started/", "preview/getting-started/")
        )
    for attempt in range(args.attempts):
        failures = []
        for link in sorted(links):
            try:
                final = check_link(link, args.site_dir)
                print(f"OK {link} -> {final}")
            except (OSError, ValueError) as error:
                failures.append(f"FAIL {link}: {error}")
        if not failures:
            return 0
        if attempt + 1 < args.attempts:
            print("\n".join(failures), file=sys.stderr)
            time.sleep(10)
    print("\n".join(failures), file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
