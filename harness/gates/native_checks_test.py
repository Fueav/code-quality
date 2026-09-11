import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location('checks', Path(__file__).with_name('native_checks.py'))
checks = importlib.util.module_from_spec(spec); spec.loader.exec_module(checks)

class SelectionTests(unittest.TestCase):
    def test_go_change_includes_consumers_but_not_unrelated_packages(self):
        packages = [
            {'ImportPath':'m/core','Dir':'/repo/core','Deps':[]},
            {'ImportPath':'m/api','Dir':'/repo/api','Deps':['m/core']},
            {'ImportPath':'m/other','Dir':'/repo/other','Deps':[]},
        ]
        self.assertEqual(checks.go_packages({'core/a.go'}, packages, Path('/repo')), ['m/api','m/core'])

    def test_test_only_dependency_is_included(self):
        packages = [{'ImportPath':'m/a','Dir':'/repo/a'}, {'ImportPath':'m/b','Dir':'/repo/b','TestImports':['m/a']}]
        self.assertEqual(checks.go_packages({'a/a.go'}, packages, Path('/repo')), ['m/a','m/b'])

    def test_deleted_or_unmapped_go_package_falls_back_to_all(self):
        self.assertEqual(checks.go_packages({'old/a.go'}, [], Path('/repo')), ['./...'])

    def test_embedded_input_selects_owner(self):
        self.assertEqual(checks.go_packages({'api/data/value.json'}, [{'ImportPath':'m/api','Dir':'/repo/api','EmbedFiles':['data/value.json']}], Path('/repo')), ['m/api'])

    def test_python_groups_are_selected_independently(self):
        self.assertEqual(checks.python_groups({'internal/eval/gates.go'}), [])
        self.assertEqual(checks.python_groups({'pilot/live/live_watch.py'}), ['live-test'])
        self.assertEqual(checks.python_groups({'pilot/mining/aggregate.py'}), ['mining-test'])
        self.assertEqual(set(checks.python_groups({'pilot/qualification_run.py'})), {'qualification-test','live-test','mining-test'})

if __name__ == '__main__': unittest.main()
