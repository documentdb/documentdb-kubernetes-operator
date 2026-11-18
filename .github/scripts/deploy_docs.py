#!/usr/bin/env python3
"""Publish release docs without downgrading a minor version or the latest alias."""

import argparse
from dataclasses import dataclass
import json
from pathlib import Path
import re
import subprocess


NUMBER = r"(?:0|[1-9][0-9]*)"
STABLE = re.compile(rf"({NUMBER})\.({NUMBER})\.({NUMBER})")
MINOR = re.compile(rf"({NUMBER})\.({NUMBER})")
PRERELEASE_ID = rf"(?:{NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
PRERELEASE = re.compile(
    rf"{NUMBER}\.{NUMBER}\.{NUMBER}-{PRERELEASE_ID}(?:\.{PRERELEASE_ID})*"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
)


@dataclass(frozen=True)
class Deployment:
    version: str
    latest: bool


def stable_version(tag):
    match = STABLE.fullmatch(tag)
    if match is None:
        raise ValueError(f"expected a stable major.minor.patch release, got {tag!r}")
    return tuple(map(int, match.groups()))


def published_releases(versions):
    if not isinstance(versions, list):
        raise ValueError("versions.json must contain a list")
    releases = []
    latest_count = 0
    for entry in versions:
        version = entry["version"]
        if STABLE.fullmatch(version):
            # Existing patch snapshots predate the release property.
            release = stable_version(version)
        elif MINOR.fullmatch(version):
            tag = entry.get("properties", {}).get("release")
            if not isinstance(tag, str):
                raise ValueError(f"minor snapshot {version!r} has no release property")
            release = stable_version(tag)
            if version != f"{release[0]}.{release[1]}":
                raise ValueError(f"minor snapshot {version!r} has mismatched release {tag!r}")
        else:
            release = None
        if "latest" in entry["aliases"]:
            latest_count += 1
            if release is None:
                raise ValueError("latest must point to stable release documentation")
        if release is not None:
            releases.append(release)
    if latest_count > 1:
        raise ValueError("versions.json has multiple latest aliases")
    return releases


def plan_deployment(tag, prerelease, versions):
    releases = published_releases(versions)
    if prerelease:
        if PRERELEASE.fullmatch(tag) is None:
            raise ValueError("prerelease tags must use major.minor.patch-prerelease")
        return Deployment(tag, False)

    release = stable_version(tag)
    if any(other[:2] == release[:2] and other > release for other in releases):
        return None
    return Deployment(
        f"{release[0]}.{release[1]}",
        not releases or release >= max(releases),
    )


def stamp_docs(version, docs=Path("docs/operator-public-documentation")):
    if not (STABLE.fullmatch(version) or PRERELEASE.fullmatch(version)):
        raise ValueError(f"expected a full release version, got {version!r}")
    for path in docs.rglob("*.md"):
        text = path.read_text(encoding="utf-8")
        if "<release-version>" in text:
            path.write_text(text.replace("<release-version>", version), encoding="utf-8")


def deploy(tag, prerelease):
    # Read state after acquiring the workflow's shared Pages deployment lock.
    subprocess.run(["git", "fetch", "origin", "gh-pages"], check=True)
    versions = json.loads(
        subprocess.check_output(
            ["git", "show", "origin/gh-pages:versions.json"], text=True
        )
    )
    plan = plan_deployment(tag, prerelease, versions)
    if plan is None:
        print(f"Skipping {tag}: a newer patch is already published for this minor.")
        return
    command = [
        "mike", "deploy", plan.version, "--title", plan.version,
        "--prop-set-string", f"release={tag}",
    ]
    if plan.latest:
        command.extend(["latest", "--update-aliases"])
    subprocess.run(command, check=True)
    if plan.latest:
        subprocess.run(["mike", "set-default", "latest"], check=True)
    # Publish content, provenance, aliases and the default redirect together.
    subprocess.run(["git", "push", "origin", "gh-pages"], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    stamp = commands.add_parser("stamp", help="Stamp full installation versions")
    stamp.add_argument("--version", required=True)
    publish = commands.add_parser("deploy", help="Publish release documentation")
    publish.add_argument("--tag", required=True)
    publish.add_argument("--prerelease", required=True, choices=("true", "false"))
    args = parser.parse_args()
    try:
        if args.command == "stamp":
            stamp_docs(args.version)
        else:
            deploy(args.tag, args.prerelease == "true")
    except ValueError as error:
        parser.error(str(error))


if __name__ == "__main__":
    main()
