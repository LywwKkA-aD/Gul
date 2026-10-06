#!/usr/bin/env python3
"""Run the pinned Go scanner with one narrowly reviewed metadata correction.

GO-2026-5288 currently marks every core/v2 version and symbol as affected.
The upstream advisory limits the QUIC/sniff OOM to <=2.8.1, and release 2.8.2
explicitly fixes it. Gul pins 2.13.0 and compiles client/obfs, not server/sniff.
This exception must be reviewed again when either Hysteria module changes.

Sources reviewed 2026-10-06:
https://pkg.go.dev/vuln/GO-2026-5288
https://github.com/HyNetworks/hysteria/security/advisories/GHSA-9fw6-xgg2-mq9q
https://github.com/HyNetworks/hysteria/releases/tag/app/v2.8.2

The v1.7.0 JSON protocol has no completion message. JSON mode exits zero even
with findings. Completion therefore requires normal subprocess exit, valid
JSON through EOF, config/SBOM records, and matching non-test package roots.
"""

import argparse
import json
import subprocess
import sys


SCANNER_VERSION = "v1.7.0"
REVIEWED_ID = "GO-2026-5288"
CORE = "github.com/apernet/hysteria/core/v2"
EXTRAS = "github.com/apernet/hysteria/extras/v2"
PINNED_VERSION = "v2.13.0"
FIX_URL = "https://github.com/HyNetworks/hysteria/releases/tag/app/v2.8.2"
FORBIDDEN_PACKAGES = (CORE + "/server", EXTRAS + "/sniff")


class AuditError(Exception):
    """A scanner or graph result cannot safely be interpreted."""


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise AuditError("duplicate JSON object key")
        result[key] = value
    return result


def json_records(text):
    def invalid_constant(_):
        raise AuditError("nonstandard JSON constant")

    decoder = json.JSONDecoder(object_pairs_hook=unique_object, parse_constant=invalid_constant)
    records = []
    offset = 0
    while offset < len(text):
        if text[offset].isspace():
            offset += 1
            continue
        try:
            record, offset = decoder.raw_decode(text, offset)
        except (ValueError, RecursionError) as exc:
            raise AuditError("malformed or truncated JSON stream") from exc
        if not isinstance(record, dict) or not record:
            raise AuditError("expected a nonempty JSON object")
        records.append(record)
    if not records:
        raise AuditError("empty JSON stream")
    return tuple(records)


def require_string(value, description, allow_empty=False):
    if not isinstance(value, str) or (not allow_empty and not value):
        raise AuditError("invalid " + description)
    return value


def parse_scan(text):
    records = json_records(text)
    if set(records[0]) != {"config"}:
        raise AuditError("scanner configuration must be the first record")
    config = records[0]["config"]
    expected = {"protocol_version": "v1.0.0", "scanner_name": "govulncheck",
                "scanner_version": SCANNER_VERSION, "scan_level": "symbol", "scan_mode": "source"}
    if not isinstance(config, dict) or any(config.get(key) != value for key, value in expected.items()):
        raise AuditError("expected pinned govulncheck v1.7.0 source/symbol JSON protocol")
    sboms, findings, advisory_ids = [], [], set()
    for index, record in enumerate(records):
        if len(record) != 1:
            raise AuditError("scanner record must contain exactly one event")
        kind, value = next(iter(record.items()))
        if not isinstance(value, dict):
            raise AuditError("invalid scanner event")
        if kind == "config":
            if index != 0:
                raise AuditError("duplicate scanner configuration")
        elif kind == "SBOM":
            sboms.append(value)
        elif kind == "osv":
            advisory_ids.add(require_string(value.get("id"), "advisory ID"))
        elif kind == "finding":
            findings.append(value)
        elif kind != "progress":
            raise AuditError("unknown scanner event")
    if len(sboms) != 1:
        raise AuditError("expected exactly one scanner SBOM")
    sbom = sboms[0]
    roots = sbom.get("roots")
    modules = sbom.get("modules")
    if not isinstance(roots, list) or not roots or not isinstance(modules, list) or not modules:
        raise AuditError("scanner SBOM has no package roots or modules")
    for root in roots:
        require_string(root, "package root")
    module_versions = {}
    for module in modules:
        if not isinstance(module, dict):
            raise AuditError("invalid SBOM module")
        path = require_string(module.get("path"), "module path")
        version = require_string(module.get("version", ""), "module version", allow_empty=True)
        if path in module_versions and module_versions[path] != version:
            raise AuditError("conflicting SBOM module versions")
        module_versions[path] = version
    for finding in findings:
        advisory = require_string(finding.get("osv"), "finding ID")
        if advisory not in advisory_ids:
            raise AuditError("finding has no corresponding advisory record")
        trace = finding.get("trace")
        if not isinstance(trace, list) or not trace:
            raise AuditError("finding has no trace")
        for frame in trace:
            if not isinstance(frame, dict):
                raise AuditError("invalid finding frame")
            require_string(frame.get("module"), "finding module")
            for key in ("version", "package", "function", "receiver"):
                require_string(frame.get(key, ""), "finding " + key, allow_empty=True)
            if frame.get("function") and not frame.get("package"):
                raise AuditError("symbol finding has no package")
        require_string(finding.get("fixed_version", ""), "fixed version", allow_empty=True)
    return frozenset(roots), module_versions, tuple(findings)


def inspect_graph(text, expected_roots):
    records = json_records(text)
    packages, modules = set(), {}
    for record in records:
        packages.add(require_string(record.get("ImportPath"), "compiled package"))
        if record.get("Error") or record.get("DepsErrors"):
            raise AuditError("go list reported incomplete dependencies")
        module = record.get("Module")
        if module is not None:
            if not isinstance(module, dict):
                raise AuditError("invalid compiled module")
            path = require_string(module.get("Path"), "compiled module path")
            value = (module.get("Version", ""), module.get("Replace") is not None)
            if path in modules and modules[path] != value:
                raise AuditError("conflicting compiled module versions")
            modules[path] = value
    roots = frozenset(record["ImportPath"] for record in records if not record.get("DepOnly", False))
    if roots != expected_roots:
        raise AuditError("go list package roots do not match the scanner scope")
    return frozenset(packages), modules


def reviewed_scope(sbom_modules, packages, compiled_modules):
    return (all(sbom_modules.get(module) == PINNED_VERSION and
                compiled_modules.get(module) == (PINNED_VERSION, False) for module in (CORE, EXTRAS)) and
            not any(package == forbidden or package.startswith(forbidden + "/")
                    for package in packages for forbidden in FORBIDDEN_PACKAGES))


def report(findings, eligible, output):
    grouped = {}
    for finding in findings:
        frame = finding["trace"][0]
        level = 2 if frame.get("function") else 1 if frame.get("package") else 0
        key = (finding["osv"], frame["module"], frame.get("version", ""))
        grouped[key] = max(grouped.get(key, -1), level)
    blocked, reviewed, informational = 0, 0, 0
    for (advisory, module, version), level in sorted(grouped.items()):
        label = f"{advisory}: {module}@{version or '(unversioned)'}"
        if level < 2:
            informational += 1
            print(f"INFO ({'package' if level else 'module'} only): {label}", file=output)
        elif eligible and (advisory, module, version) == (REVIEWED_ID, CORE, PINNED_VERSION):
            reviewed += 1
            print(f"REVIEWED metadata false positive: {label}; client/obfs only, no replacements.", file=output)
            print(f"  Upstream fixed this server/sniff issue in 2.8.2: {FIX_URL}", file=output)
        else:
            blocked += 1
            print(f"FAIL reachable finding: {label}; https://pkg.go.dev/vuln/{advisory}", file=output)
    print(f"Go vulnerability scan: {blocked} blocking, {reviewed} reviewed, {informational} nonblocking findings.", file=output)
    return 3 if blocked else 0


def command_result(command, run, output):
    result = run(command, text=True, capture_output=True, check=False)
    if result.stderr.strip():
        print(result.stderr.strip(), file=output)
    if result.returncode != 0:
        raise AuditError(f"{command[0]} did not complete successfully (exit {result.returncode})")
    return result.stdout


def check(patterns, executable="govulncheck", *, run=None, output=None):
    run = run or subprocess.run
    output = output or sys.stdout
    try:
        if not patterns or any(not pattern or pattern.startswith("-") for pattern in patterns):
            raise AuditError("provide non-test Go package patterns, without scanner flags")
        print("Running pinned govulncheck v1.7.0 and verifying compiled dependency scope...", file=output, flush=True)
        scan_text = command_result([executable, "-json", "-scan=symbol", "-mode=source", "-test=false", *patterns], run, output)
        roots, sbom_modules, findings = parse_scan(scan_text)
        graph_text = command_result(["go", "list", "-deps", "-json", *patterns], run, output)
        packages, compiled_modules = inspect_graph(graph_text, roots)
        return report(findings, reviewed_scope(sbom_modules, packages, compiled_modules), output)
    except (AuditError, OSError, UnicodeError) as exc:
        print(f"ERROR: {exc}", file=output)
        return 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--govulncheck", default="govulncheck", help="path to installed govulncheck v1.7.0")
    parser.add_argument("packages", nargs="+", help="Go package patterns to scan")
    args = parser.parse_args()
    return check(args.packages, args.govulncheck)


if __name__ == "__main__":
    sys.exit(main())
