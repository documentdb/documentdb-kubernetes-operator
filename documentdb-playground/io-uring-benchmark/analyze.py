#!/usr/bin/env python3
"""Summarize io_uring micro-benchmark results.

Reads Locust ``*_stats.csv`` files produced by 50-run-matrix.sh (one per
io_method x workload x selectivity x repeat), takes the per-cell ``Aggregated``
row, computes the median across repeats, and prints a comparison table with the
throughput speedup of each io_method relative to ``sync``.

Layout expected (any depth — we glob recursively):
    <root>/.../<io_method>/<workload>_sel<sel>_r<rep>_stats.csv

Usage:
    python3 analyze.py <results-root> [--out summary.csv]
"""
import argparse
import csv
import glob
import os
import re
import statistics
import sys
from collections import defaultdict

CELL_RE = re.compile(r"(?P<wl>.+)_sel(?P<sel>[^_]+)_r(?P<rep>\d+)_stats\.csv$")
SEL_ORDER = ["unique", "5", "10", "50", "100", "1k", "2k", "3k", "4k", "5k"]
METHOD_ORDER = ["sync", "worker", "io_uring"]


def _f(row, *keys):
    """First parseable float among the given column names, else None."""
    for k in keys:
        if k in row and row[k] not in ("", "N/A", None):
            try:
                return float(row[k])
            except ValueError:
                pass
    return None


def parse_cell(path):
    """Return (method, workload, selectivity, metrics) or None."""
    m = CELL_RE.search(os.path.basename(path))
    if not m:
        return None
    method = os.path.basename(os.path.dirname(path))
    with open(path, newline="") as fh:
        agg = None
        for row in csv.DictReader(fh):
            if row.get("Name") == "Aggregated":
                agg = row
                break
        if agg is None:
            return None
    metrics = {
        "rps": _f(agg, "Requests/s"),
        "p50": _f(agg, "50%", "Median Response Time"),
        "p95": _f(agg, "95%"),
        "p99": _f(agg, "99%"),
        "count": _f(agg, "Request Count"),
    }
    return method, m["wl"], m["sel"], metrics


def median_or_none(vals):
    vals = [v for v in vals if v is not None]
    return statistics.median(vals) if vals else None


def sel_key(sel):
    return SEL_ORDER.index(sel) if sel in SEL_ORDER else len(SEL_ORDER)


def method_key(method):
    return METHOD_ORDER.index(method) if method in METHOD_ORDER else len(METHOD_ORDER)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root", help="results root directory")
    ap.add_argument("--out", default=None, help="write summary CSV here")
    args = ap.parse_args()

    files = glob.glob(os.path.join(args.root, "**", "*_stats.csv"), recursive=True)
    # Skip locust's per-cell history files, keep only the summary stats files.
    files = [f for f in files if not f.endswith("_stats_history.csv")]
    if not files:
        sys.exit(f"no *_stats.csv files found under {args.root}")

    # (workload, sel, method) -> list of per-repeat metric dicts
    cells = defaultdict(list)
    for path in files:
        parsed = parse_cell(path)
        if parsed:
            method, wl, sel, metrics = parsed
            cells[(wl, sel, method)].append(metrics)

    # (workload, sel, method) -> median metrics
    agg = {}
    for key, runs in cells.items():
        agg[key] = {m: median_or_none([r[m] for r in runs]) for m in ("rps", "p50", "p95", "p99", "count")}

    methods = sorted({k[2] for k in agg}, key=method_key)
    combos = sorted({(k[0], k[1]) for k in agg}, key=lambda t: (t[0], sel_key(t[1])))

    rows = []
    header = ["workload", "selectivity"]
    for mth in methods:
        header += [f"{mth}_rps", f"{mth}_p95ms", f"{mth}_p99ms"]
    header += [f"speedup_{mth}_vs_sync" for mth in methods if mth != "sync"]

    for wl, sel in combos:
        row = {"workload": wl, "selectivity": sel}
        base = agg.get((wl, sel, "sync"), {}).get("rps")
        for mth in methods:
            md = agg.get((wl, sel, mth), {})
            row[f"{mth}_rps"] = md.get("rps")
            row[f"{mth}_p95ms"] = md.get("p95")
            row[f"{mth}_p99ms"] = md.get("p99")
        for mth in methods:
            if mth == "sync":
                continue
            cur = agg.get((wl, sel, mth), {}).get("rps")
            row[f"speedup_{mth}_vs_sync"] = (cur / base) if (cur and base) else None
        rows.append(row)

    def fmt(v, nd=1):
        return "—" if v is None else f"{v:.{nd}f}"

    widths = {h: max(len(h), 10) for h in header}
    print("  ".join(h.ljust(widths[h]) for h in header))
    print("  ".join("-" * widths[h] for h in header))
    for row in rows:
        cells_out = []
        for h in header:
            v = row.get(h)
            if v is None:
                cells_out.append("—".ljust(widths[h]))
            elif h.startswith("speedup"):
                cells_out.append((fmt(v, 2) + "x").ljust(widths[h]))
            elif isinstance(v, float):
                cells_out.append(fmt(v).ljust(widths[h]))
            else:
                cells_out.append(str(v).ljust(widths[h]))
        print("  ".join(cells_out))

    if args.out:
        with open(args.out, "w", newline="") as fh:
            w = csv.DictWriter(fh, fieldnames=header)
            w.writeheader()
            for row in rows:
                w.writerow({k: ("" if row.get(k) is None else row.get(k)) for k in header})


if __name__ == "__main__":
    main()
