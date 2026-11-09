import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
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


if __name__ == "__main__":
    unittest.main()
