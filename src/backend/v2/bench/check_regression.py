#!/usr/bin/env python3
"""Validate disposable Ginbar API benchmark results and PostgreSQL plan shape."""

import argparse
import json
import math
import re
import sys
from pathlib import Path

CASES = (
    "feed-first-c1",
    "feed-cursor-c1",
    "search-tag-score-c1",
    "around-50000-c1",
    "around-50000-c8",
)
PLAN_NAMES = (
    "first feed page",
    "old cursor feed page",
    "required tag + score",
    "required + excluded tag + score",
    "around post 49999",
)
METRICS = ("p95Ms", "p99Ms", "requestsPerSecond")


def positive(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{name} must be numeric")
    if not math.isfinite(value) or value <= 0:
        raise ValueError(f"{name} must be finite and positive")
    return float(value)


def check_plans(contents):
    problems = []
    parts = re.split(r"(?m)^--- ([^\n]+) ---\s*$", contents)
    sections = dict(zip(parts[1::2], parts[2::2]))
    for name in PLAN_NAMES:
        plan = sections.get(name)
        if plan is None:
            problems.append(f"EXPLAIN section absent: {name}")
            continue
        if len(re.findall(r"Execution Time:\s+[\d.]+ ms", plan)) != 1:
            problems.append(f"{name}: expected exactly one completed EXPLAIN ANALYZE")
        if "Buffers:" not in plan or not re.search(r"\b(Index|Bitmap) (Only )?Scan\b", plan):
            problems.append(f"{name}: buffer evidence or indexed scan absent")
        if re.search(r"\bSeq Scan on (?:posts|media)\b", plan):
            problems.append(f"{name}: sequential scan on hot posts/media table")
        if re.search(r"\b(?:Sort Method: external|Disk:|temp read=|temp written=)", plan):
            problems.append(f"{name}: external sort or temporary spill")
    return problems


def read_budgets(path):
    if path is None:
        return None
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or data.get("schema") != 1:
        raise ValueError("budget file requires schema=1")
    if not isinstance(data.get("provenance"), str) or not data["provenance"].strip():
        raise ValueError("budget file requires accepted measurement provenance")
    cases = data.get("cases")
    if not isinstance(cases, dict) or set(cases) != set(CASES):
        raise ValueError("budget file must define every current case, and no others")
    for name, entry in cases.items():
        if not isinstance(entry, dict) or not entry or set(entry) - {"maxP95Ms", "maxP99Ms", "minRequestsPerSecond"}:
            raise ValueError(f"{name}: invalid budget fields")
        for key, value in entry.items():
            positive(value, f"{name}.{key}")
    return cases


def check_results(folder, count, budgets):
    problems = []
    summary = {}
    actual_names = {path.stem.removeprefix("http-") for path in folder.glob("http-*.json")}
    if actual_names != set(CASES):
        problems.append(f"benchmark cases mismatch: expected={sorted(CASES)} actual={sorted(actual_names)}")
    for name in CASES:
        path = folder / f"http-{name}.json"
        if not path.is_file():
            continue
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
            if data.get("requests") != count or data.get("successes") != count:
                problems.append(f"{name}: expected {count} requests and successes")
            if data.get("errors") != 0 or data.get("statusCodes") != {"200": count}:
                problems.append(f"{name}: HTTP errors/non-200 statuses: {data.get('statusCodes')}")
            metrics = {metric: positive(data[metric], f"{name}.{metric}") for metric in METRICS}
            summary[name] = metrics
            if budgets is not None:
                limits = budgets[name]
                for key, value in limits.items():
                    measured = metrics["requestsPerSecond" if key == "minRequestsPerSecond" else key.removeprefix("max").replace("P", "p", 1)]
                    if (key.startswith("max") and measured > value) or (key.startswith("min") and measured < value):
                        problems.append(f"{name}: {key}={value} violated (measured={measured:.3f})")
        except (ValueError, KeyError, TypeError, json.JSONDecodeError) as exc:
            problems.append(f"{name}: invalid benchmark JSON: {exc}")
    return summary, problems


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("results", type=Path)
    parser.add_argument("--requests", type=int, default=2000)
    parser.add_argument("--budget-file", type=Path)
    args = parser.parse_args()
    if args.requests < 100:
        parser.error("--requests must be >= 100 for percentile sampling")
    try:
        budgets = read_budgets(args.budget_file)
        plans = (args.results / "explain.txt").read_text(encoding="utf-8")
        problems = check_plans(plans)
        summary, checks = check_results(args.results, args.requests, budgets)
        problems.extend(checks)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"v2-perf-regression: ERROR: {exc}", file=sys.stderr)
        return 2
    (args.results / "summary.json").write_text(
        json.dumps({"budgetMode": "enforced" if budgets else "unbudgeted", "cases": summary}, indent=2) + "\n",
        encoding="utf-8",
    )
    for name, metrics in summary.items():
        print(f"{name}: p95={metrics['p95Ms']:.3f}ms p99={metrics['p99Ms']:.3f}ms rps={metrics['requestsPerSecond']:.1f}")
    for problem in problems:
        print(f"v2-perf-regression: FAIL {problem}", file=sys.stderr)
    if problems:
        return 1
    print("v2-perf-regression: PASS shape=bounded/indexed/no-spill http=all-200 budget=" + ("enforced" if budgets else "not-yet-calibrated"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
