import tempfile
from pathlib import Path
import unittest

from broker import write
from passage_check import check_inputs
from workload import BASELINE, digest


class PassageInputTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / 'artifacts').mkdir()
        self.selected = {'source': BASELINE, 'sha256': digest(BASELINE)}
        # Synthetic qualification solely for adapter validation, not a workload claim.
        self.qualification = {'sha256': digest(BASELINE), 'valid': True, 'qualified': True}
        self.audit = {'passed': True, 'checks': {'fixture': True}, 'qualification': self.qualification}
        write(self.root / 'artifacts/selected.json', self.selected)
        write(self.root / 'artifacts/qualification.json', self.qualification)
        write(self.root / 'host-audit.json', self.audit)

    def tearDown(self):
        self.tmp.cleanup()

    def test_consistent_evidence_accepted(self):
        self.assertEqual(check_inputs(self.root)[0], self.selected)

    def test_changed_source_refused(self):
        self.selected['source'] += '# changed'
        write(self.root / 'artifacts/selected.json', self.selected)
        with self.assertRaisesRegex(ValueError, 'source mismatch'):
            check_inputs(self.root)

    def test_summary_cannot_override_failed_check(self):
        self.audit['checks']['fixture'] = False
        write(self.root / 'host-audit.json', self.audit)
        with self.assertRaisesRegex(ValueError, 'failed checks'):
            check_inputs(self.root)

    def test_other_qualification_refused(self):
        self.audit['qualification'] = {'sha256': 'other'}
        write(self.root / 'host-audit.json', self.audit)
        with self.assertRaisesRegex(ValueError, 'another result'):
            check_inputs(self.root)


if __name__ == '__main__':
    unittest.main()
