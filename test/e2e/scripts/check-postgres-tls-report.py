#!/usr/bin/env python3
"""Reject green-but-empty, filtered, skipped, or incomplete acceptance runs."""
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    reports = json.load(source)

if len(reports) != 1 or not reports[0]["SuiteSucceeded"] or reports[0]["SuiteConfig"]["DryRun"]:
    sys.exit("Expected one successful, non-dry-run acceptance suite")

specs = [
    spec
    for suite in reports
    for spec in suite["SpecReports"]
    if spec["LeafNodeType"] == "It"
]
required = [
    "preserves all PostgreSQL certificate fields",
    "untrusted server CA",
    "missing hostname SAN",
    "untrusted replication client certificate",
    "rotates server and replication client certificates",
]
if len(specs) != len(required):
    sys.exit(f"Expected exactly {len(required)} acceptance specs, found {len(specs)}")
for name in required:
    matching = [spec for spec in specs if name in spec["LeafNodeText"]]
    if len(matching) != 1 or matching[0]["State"] != "passed":
        sys.exit(f"Required PostgreSQL TLS acceptance spec did not pass: {name}")
print("All five multi-cluster PostgreSQL TLS acceptance specs executed and passed.")
