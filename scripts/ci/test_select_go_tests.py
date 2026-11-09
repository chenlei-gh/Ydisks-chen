import os
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import select_go_tests
from select_go_tests import is_documentation_path, reverse_dependency_closure


class GoTestSelectionTests(unittest.TestCase):
    def test_reverse_closure_includes_production_and_test_importers(self):
        packages = {
            "xianyu-go/internal/base": {},
            "xianyu-go/internal/service": {
                "Imports": ["xianyu-go/internal/base"],
            },
            "xianyu-go/internal/service_test_helper": {},
            "xianyu-go/internal/server": {
                "TestImports": ["xianyu-go/internal/service"],
                "XTestImports": ["xianyu-go/internal/service_test_helper"],
            },
        }
        selected = reverse_dependency_closure(
            packages, {"xianyu-go/internal/base"}, "xianyu-go"
        )
        self.assertEqual(
            selected,
            {
                "xianyu-go/internal/base",
                "xianyu-go/internal/service",
                "xianyu-go/internal/server",
            },
        )

    def test_in_module_dependency_missing_from_graph_requests_fallback(self):
        packages = {
            "xianyu-go/internal/service": {
                "Imports": ["xianyu-go/internal/missing"],
            }
        }
        self.assertIsNone(
            reverse_dependency_closure(
                packages, {"xianyu-go/internal/service"}, "xianyu-go"
            )
        )

    def test_documentation_paths(self):
        self.assertTrue(is_documentation_path("README.zh-CN.md"))
        self.assertTrue(is_documentation_path("docs/ci.md"))
        self.assertTrue(is_documentation_path("wiki/setup.md"))
        self.assertFalse(is_documentation_path(".github/workflows/ci.md"))
        self.assertFalse(is_documentation_path("internal/server/README.md"))

    @patch.dict(os.environ, {"GITHUB_EVENT_NAME": "pull_request", "PR_BASE_SHA": "base"}, clear=True)
    @patch("select_go_tests.changed_paths", return_value=["docs/ci.md", "README.md"])
    @patch("select_go_tests.full_suite")
    def test_documentation_only_change_skips_go_tests(self, full_suite, changed_paths):
        self.assertEqual(select_go_tests.main(), 0)
        changed_paths.assert_called_once_with("base")
        full_suite.assert_not_called()

    @patch.dict(os.environ, {"GITHUB_EVENT_NAME": "push"}, clear=True)
    @patch("select_go_tests.full_suite", return_value=0)
    def test_non_pr_event_runs_full_suite(self, full_suite):
        self.assertEqual(select_go_tests.main(), 0)
        full_suite.assert_called_once_with("non-PR event")


if __name__ == "__main__":
    unittest.main()
