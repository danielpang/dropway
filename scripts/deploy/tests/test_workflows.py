"""Check reusable workflow wiring and run the actual early validation shell."""
import os
from pathlib import Path
import re
import subprocess
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = ROOT / '.github/workflows'


class WorkflowTests(unittest.TestCase):
    def test_all_deployment_callers_inherit_secrets(self):
        calls = 0
        for name in ('deploy-hosted.yml', 'migrate.yml'):
            source = (WORKFLOWS / name).read_text()
            for job in re.split(r'^  [a-z-]+:\n', source, flags=re.M)[1:]:
                if 'uses: ./.github/workflows/' in job:
                    calls += 1
                    self.assertIn('    secrets: inherit\n', job, name)
        self.assertEqual(calls, 4)

    def validation(self, target, **values):
        source = (WORKFLOWS / 'migrate-environment.yml').read_text()
        validation, remaining = source.split('        run: |\n', 1)[1].split('      - uses:', 1)
        self.assertIn('actions/setup-go', remaining)
        env = {**os.environ, 'TARGET': target, 'CHECK_DEPLOY': 'false',
               'DEV_APP_DSN': '', 'DEV_BILLING_DSN': '',
               'PROD_APP_DSN': '', 'PROD_BILLING_DSN': '', **values}
        return subprocess.run(['bash', '-c', textwrap.dedent(validation)],
                              env=env, capture_output=True, text=True)

    def test_requires_both_owner_urls_before_installing_dependencies(self):
        for target, prefix in (('development', 'DEV'), ('production', 'PROD')):
            with self.subTest(target=target):
                result = self.validation(target)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f'{prefix}_DATABASE_OWNER_URL', result.stderr)
                result = self.validation(target, **{f'{prefix}_APP_DSN': 'private-canary'})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f'{prefix}_BILLING_OWNER_URL', result.stderr)
                self.assertNotIn('private-canary', result.stderr + result.stdout)

    def test_only_target_environment_secrets_are_required(self):
        for target, prefix in (('development', 'DEV'), ('production', 'PROD')):
            with self.subTest(target=target):
                result = self.validation(target, **{f'{prefix}_APP_DSN': 'app',
                                                    f'{prefix}_BILLING_DSN': 'billing'})
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_unknown_environment(self):
        self.assertNotEqual(self.validation('preview').returncode, 0)
