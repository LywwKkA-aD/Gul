import importlib.util
import io
import json
import pathlib
import subprocess
import unittest


PATH = pathlib.Path(__file__).with_name("check-go-vulnerabilities.py")
SPEC = importlib.util.spec_from_file_location("check_go_vulnerabilities", PATH)
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)

CORE = "github.com/apernet/hysteria/core/v2"
EXTRAS = "github.com/apernet/hysteria/extras/v2"
ROOT = "github.com/LywwKkA-aD/Gul/internal/hysteria"
KNOWN = "GO-2026-5288"


def stream(records):
    return "\n".join(json.dumps(record) for record in records)


def scan(findings=(), version="v2.13.0", scanner="v1.7.0"):
    return stream([
        {"config": {"protocol_version": "v1.0.0", "scanner_name": "govulncheck",
                    "scanner_version": scanner, "scan_level": "symbol", "scan_mode": "source"}},
        {"SBOM": {"roots": [ROOT], "modules": [
            {"path": CORE, "version": version}, {"path": EXTRAS, "version": "v2.13.0"}]}},
        *[{"osv": {"id": advisory}} for advisory in sorted({item["osv"] for item in findings})],
        *[{"finding": item} for item in findings],
    ])


def finding(advisory=KNOWN, version="v2.13.0", level="symbol"):
    frame = {"module": CORE, "version": version}
    if level != "module":
        frame = {**frame, "package": CORE + "/client"}
    if level == "symbol":
        frame = {**frame, "function": "NewClient"}
    return {"osv": advisory, "trace": [frame]}


def graph(extra_packages=(), core_version="v2.13.0", extras_version="v2.13.0", replace=None):
    def module(path, version):
        value = {"Path": path, "Version": version}
        return {**value, "Replace": {"Path": "/tmp/fork"}} if replace == path else value

    return stream([
        {"ImportPath": ROOT},
        {"ImportPath": CORE + "/client", "DepOnly": True, "Module": module(CORE, core_version)},
        {"ImportPath": EXTRAS + "/obfs", "DepOnly": True, "Module": module(EXTRAS, extras_version)},
        *[{"ImportPath": package, "DepOnly": True} for package in extra_packages],
    ])


class VulnerabilityGateTest(unittest.TestCase):
    def run_gate(self, scan_output, dependency_output=None, scan_exit=0, go_exit=0):
        calls = []
        outputs = [
            subprocess.CompletedProcess([], scan_exit, scan_output, "scanner error" if scan_exit else ""),
            subprocess.CompletedProcess([], go_exit, dependency_output if dependency_output is not None else graph(),
                                        "go list error" if go_exit else ""),
        ]

        def run(command, **kwargs):
            calls.append(command)
            self.assertFalse(kwargs.get("shell", False))
            return outputs[len(calls) - 1]

        output = io.StringIO()
        result = CHECK.check(["./internal/hysteria"], "govulncheck", run=run, output=output)
        return result, output.getvalue(), calls

    def test_exact_reviewed_finding_is_reported_with_source(self):
        result, output, calls = self.run_gate(scan([finding(), finding(level="module")]))
        self.assertEqual(result, 0)
        self.assertIn("REVIEWED", output)
        self.assertIn(KNOWN, output)
        self.assertIn("https://github.com/HyNetworks/hysteria/releases/tag/app/v2.8.2", output)
        self.assertIn("1 reviewed", output)
        self.assertEqual(calls[0], ["govulncheck", "-json", "-scan=symbol", "-mode=source", "-test=false", "./internal/hysteria"])
        self.assertEqual(calls[1], ["go", "list", "-deps", "-json", "./internal/hysteria"])

    def test_other_reachable_vulnerability_fails_even_with_reviewed_finding(self):
        result, output, _ = self.run_gate(scan([finding(), finding("GO-2026-9999")]))
        self.assertNotEqual(result, 0)
        self.assertIn("FAIL", output)
        self.assertIn("GO-2026-9999", output)

    def test_changed_version_fails(self):
        for version in ("v2.8.1", "v2.13.1", ""):
            with self.subTest(version=version):
                result, _, _ = self.run_gate(scan([finding(version=version)], version=version), graph(core_version=version))
                self.assertNotEqual(result, 0)

    def test_compiled_server_or_sniffer_prevents_exception(self):
        for package in (CORE + "/server", CORE + "/server/extra", EXTRAS + "/sniff", EXTRAS + "/sniff/internal/quic"):
            with self.subTest(package=package):
                result, _, _ = self.run_gate(scan([finding()]), graph([package]))
                self.assertNotEqual(result, 0)

    def test_replacements_and_extras_version_prevent_exception(self):
        for dependencies in (graph(replace=CORE), graph(replace=EXTRAS), graph(extras_version="v2.13.1")):
            with self.subTest(dependencies=dependencies):
                result, _, _ = self.run_gate(scan([finding()]), dependencies)
                self.assertNotEqual(result, 0)

    def test_module_and_package_only_findings_remain_nonblocking(self):
        result, output, _ = self.run_gate(scan([finding("GO-2026-9999", level="module"), finding("GO-2026-9998", level="package")]))
        self.assertEqual(result, 0)
        self.assertIn("GO-2026-9999", output)
        self.assertIn("GO-2026-9998", output)
        self.assertNotIn("REVIEWED", output)

    def test_malformed_and_incomplete_output_fails_closed(self):
        for invalid in ("", "{}", "not JSON", scan() + '{"finding":',
                        '{"config":{},"config":{}}',
                        scan() + '{"progress":{"message":NaN}}',
                        stream([{"config": {"protocol_version": "v1.0.0"}}]),
                        scan([{"osv": KNOWN, "trace": []}]),
                        scan([{"osv": KNOWN, "trace": [{"module": CORE, "function": 42}]}])):
            with self.subTest(invalid=invalid):
                result, output, _ = self.run_gate(invalid)
                self.assertNotEqual(result, 0)
                self.assertIn("ERROR", output)

    def test_tool_failure_does_not_pass_even_with_complete_allowed_json(self):
        for exit_code in (1, 2, 3, -9):
            with self.subTest(exit_code=exit_code):
                result, _, _ = self.run_gate(scan([finding()]), scan_exit=exit_code)
                self.assertNotEqual(result, 0)

    def test_wrong_tool_version_fails_closed(self):
        result, _, _ = self.run_gate(scan([finding()], scanner="v1.8.0"))
        self.assertNotEqual(result, 0)

    def test_dependency_inspection_failure_or_scope_mismatch_fails(self):
        for dependencies, exit_code in ((graph(), 1), ("", 0), ("{", 0), ('{"ImportPath":{}}', 0),
                                        (graph().replace(ROOT, ROOT + "-other"), 0)):
            with self.subTest(dependencies=dependencies, exit_code=exit_code):
                result, _, _ = self.run_gate(scan([finding()]), dependencies, go_exit=exit_code)
                self.assertNotEqual(result, 0)


if __name__ == "__main__":
    unittest.main()
