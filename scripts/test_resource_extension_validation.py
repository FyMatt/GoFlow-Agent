import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_resource_links as validation


class ResourceExtensionValidationTests(unittest.TestCase):
    def test_minimal_unknown_domain_does_not_need_team_or_preset(self):
        known = {key: set() for key in validation.LIST_FIELDS}
        known["agents"] = {"notes-agent"}
        known["workflows"] = {"notes-workflow"}
        document = "name: notes-kit\nagents: [notes-agent]\nworkflows: [notes-workflow]\n"
        self.assertEqual(validation.validate_document("custom", document, known, require_complete=True), [])
        broken = document.replace("notes-workflow", "missing-workflow")
        self.assertTrue(any("missing resource" in issue for issue in validation.validate_document("custom", broken, known, require_complete=True)))

    def test_new_mcp_source_is_discovered_without_name_table(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "mcp_servers/custom_adapter/main.go"
            source.parent.mkdir(parents=True)
            source.write_text('Name: "check_notes", Description: "检查材料"', encoding="utf-8")
            with patch.object(validation, "ROOT", root):
                self.assertEqual(validation.known_tools(), {"custom_adapter/check_notes"})

    def test_embedded_workflow_can_be_an_entry_reference(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            source = root / "internal/agent/templates/workflows/custom-entry.yaml"
            source.parent.mkdir(parents=True)
            source.write_text("name: custom-entry\n", encoding="utf-8")
            with patch.object(validation, "ROOT", root):
                self.assertIn("custom-entry", validation.known_resources()["workflows"])


if __name__ == "__main__":
    unittest.main()
