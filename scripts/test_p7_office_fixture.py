from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
import zipfile
import base64
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from p7_complex_cases import cases
from p7_office_evaluator import evaluate_documents_case
from p7_office_fixture import FIXED_DECLARATION, MASTER_DECLARATION


class P7OfficeFixtureTests(unittest.TestCase):
    def setUp(self) -> None:
        self.case = next(case for case in cases() if case.domain == "documents")

    def test_fixture_contains_real_template_features(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            self.case.builder(root)
            manifest = json.loads((root / "fixture-manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(set(manifest["templates"]), {
                "security-report-template.docx",
                "remediation-data-template.xlsx",
                "review-template.pptx",
            })
            with zipfile.ZipFile(root / "templates" / "security-report-template.docx") as archive:
                document = archive.read("word/document.xml").decode("utf-8")
                self.assertIn("TOC", document)
                self.assertIn(FIXED_DECLARATION, document)
                self.assertIn("w:headerReference", document)
                self.assertIn("w:footerReference", document)
            with zipfile.ZipFile(root / "templates" / "remediation-data-template.xlsx") as archive:
                data = archive.read("xl/worksheets/sheet1.xml").decode("utf-8")
                summary = archive.read("xl/worksheets/sheet2.xml").decode("utf-8")
                self.assertIn("autoFilter", data)
                self.assertIn("state=\"frozen\"", data)
                self.assertIn("pageSetup", data)
                self.assertIn("<f>SUM(", summary)
            with zipfile.ZipFile(root / "templates" / "review-template.pptx") as archive:
                presentation = archive.read("ppt/presentation.xml").decode("utf-8")
                master = archive.read("ppt/slideMasters/slideMaster1.xml").decode("utf-8")
                self.assertIn('cx="12192000"', presentation)
                self.assertIn('cy="6858000"', presentation)
                self.assertIn(MASTER_DECLARATION, master)

    def test_independent_evaluator_rejects_unfinished_outputs(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            self.case.builder(root)
            result = evaluate_documents_case(root)
            self.assertFalse(result["passed"])
            self.assertTrue(any("report.docx" in error for error in result["errors"]))

    def test_evaluator_rejects_placeholder_pdf_and_preview(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            self.case.builder(root)
            deliverables = root / "deliverables"
            deliverables.mkdir()
            replacements = {
                "{{CLIENT_NAME}}": "星河科技",
                "{{REPORT_TITLE}}": "Alpha 平台安全整改交付报告",
                "{{REPORT_DATE}}": "2026-08-18",
                "{{EXECUTIVE_SUMMARY}}": "已完成整改并通过复测。",
                "{{FINDING_ID}}": "SEC-001 / SEC-002",
                "{{FINDING_SEVERITY}}": "高 / 中",
                "{{FINDING_EVIDENCE}}": "授权与审计证据。",
                "{{FIX_RECOMMENDATION}}": "完成独立复测。",
                "{{PRESENTATION_TITLE}}": "星河科技 · Alpha 平台安全整改交付",
                "{{PRESENTATION_SUBTITLE}}": "授权范围内的整改与复测",
                "{{REMEDIATION_SUMMARY}}": "两项整改已完成。",
                "{{NEXT_ACTIONS}}": "按周期复核授权边界。",
            }
            for source, output in (
                ("security-report-template.docx", "report.docx"),
                ("remediation-data-template.xlsx", "data.xlsx"),
                ("review-template.pptx", "review.pptx"),
            ):
                self._fill_template(root / "templates" / source, deliverables / output, replacements)
            (deliverables / "report.pdf").write_bytes(b"%PDF-1.4\n/Type /Page\n%%EOF\n")
            preview = deliverables / "preview"
            preview.mkdir()
            preview.joinpath("page-1.png").write_bytes(base64.b64decode(
                "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR4nGMAAQAABQABDQottAAAAABJRU5ErkJggg=="
            ))
            result = evaluate_documents_case(root)
        self.assertFalse(result["passed"])
        self.assertTrue(any("preview" in error.lower() or "预览" in error for error in result["errors"]), result)
        self.assertTrue(any("report.pdf" in error for error in result["errors"]), result)

    @unittest.skipUnless(os.name == "nt" and shutil.which("powershell.exe"), "PowerPoint COM requires Windows")
    def test_fixture_pptx_opens_in_powerpoint(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            self.case.builder(root)
            path = str(root / "templates" / "review-template.pptx").replace("'", "''")
            script = (
                "$ErrorActionPreference='Stop';"
                "$app=New-Object -ComObject PowerPoint.Application;"
                "try{$deck=$app.Presentations.Open('" + path + "',$true,$false,$false);"
                "if($deck.Slides.Count -ne 3){exit 2};$deck.Close()}finally{$app.Quit()}"
            )
            completed = subprocess.run(
                ["powershell.exe", "-NoProfile", "-Command", script],
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                encoding="utf-8",
                errors="replace",
                timeout=45,
                check=False,
            )
        self.assertEqual(completed.returncode, 0, completed.stdout)

    @staticmethod
    def _fill_template(source: Path, output: Path, replacements: dict[str, str]) -> None:
        with zipfile.ZipFile(source) as src, zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as dst:
            for name in src.namelist():
                data = src.read(name)
                if name.lower().endswith(".xml"):
                    text = data.decode("utf-8")
                    for old, new in replacements.items():
                        text = text.replace(old, new)
                    data = text.encode("utf-8")
                dst.writestr(name, data)


if __name__ == "__main__":
    unittest.main()
