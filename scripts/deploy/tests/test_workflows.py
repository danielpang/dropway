"""Exercise migration/deployment workflow guards without secrets or network."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = ROOT / '.github/workflows'
SHA = 'a' * 40


def job_source(workflow, job):
    source = (WORKFLOWS / workflow).read_text()
    return re.split(r'^  [a-z-]+:\n', source.split(f'  {job}:\n', 1)[1],
                    maxsplit=1, flags=re.M)[0]


def shell(job):
    return textwrap.dedent(re.search(r'        run: \|\n((?:          .*\n|\n)+)', job)[1])


class WorkflowTests(unittest.TestCase):
    def test_deployment_only_follows_automatic_successful_migrations(self):
        source = (WORKFLOWS / 'deploy-hosted.yml').read_text()
        self.assertIn('workflows: [migrate]', source)
        self.assertNotIn('workflow_dispatch:', source)
        self.assertNotIn('migrate-environment.yml', source)
        self.assertNotIn('scripts/deploy/migrate.sh', source)
        self.assertNotIn('DATABASE_OWNER_URL', source)
        self.assertIn("github.event.workflow_run.conclusion == 'success'", source)
        self.assertIn("github.event.workflow_run.event == 'workflow_run'", source)
        self.assertIn('github.event.workflow_run.head_repository.full_name == github.repository', source)
        self.assertIn('needs: [revision, build]', source)
        self.assertIn('secrets: inherit', source)

    def test_migrations_are_direct_environment_jobs(self):
        source = (WORKFLOWS / 'migrate.yml').read_text()
        self.assertIn('workflows: [ci]', source)
        self.assertIn("github.event.workflow_run.event == 'push'", source)
        self.assertIn("needs.migrate-development.result == 'success'", source)
        self.assertNotIn('uses: ./.github/workflows/', source)
        for target, prefix in (('development', 'DEV'), ('production', 'PROD')):
            job = job_source('migrate.yml', f'migrate-{target}')
            self.assertIn(f'environment: {target}', job)
            self.assertIn(f'APP_DSN: ${{{{ secrets.{prefix}_DATABASE_OWNER_URL }}}}', job)
            self.assertIn(f'BILLING_DSN: ${{{{ secrets.{prefix}_BILLING_OWNER_URL }}}}', job)
            self.assertIn('ref: ${{ needs.revision.outputs.sha }}', job)
            self.assertIn('run: scripts/deploy/migrate.sh', job)
            self.assertLess(job.index('Validate database secrets'), job.index('actions/setup-go'))
            for values, missing in (({}, 'DATABASE'), ({'APP_DSN': 'private-canary'}, 'BILLING')):
                result = subprocess.run(['bash', '-c', shell(job)], capture_output=True, text=True,
                                        env={**os.environ, 'APP_DSN': '', 'BILLING_DSN': '', **values})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f'{prefix}_{missing}_OWNER_URL', result.stderr)
                self.assertNotIn('private-canary', result.stderr + result.stdout)
            result = subprocess.run(['bash', '-c', shell(job)], capture_output=True, text=True,
                                    env={**os.environ, 'APP_DSN': 'app', 'BILLING_DSN': 'billing'})
            self.assertEqual(result.returncode, 0, result.stderr)

    def revision(self, workflow, **values):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            for name, variable in (('git', 'MAIN_SHA'), ('gh', 'MIGRATIONS_PASSED')):
                path = directory / name
                path.write_text(f'#!/bin/sh\nprintf "%s\\n" "${variable}"\n')
                path.chmod(0o755)
            output = directory / 'output'
            env = {**os.environ, 'PATH': f'{directory}:{os.environ["PATH"]}',
                   'GITHUB_OUTPUT': str(output), 'MAIN_SHA': SHA, 'RUN_SHA': SHA,
                   'CI_SHA': SHA, 'EVENT': 'workflow_run', 'MIGRATED_SHA': SHA,
                   'MIGRATIONS_PASSED': 'true', 'GITHUB_REPOSITORY': 'test/repo',
                   'MIGRATION_RUN_ID': '123', **values}
            result = subprocess.run(['bash', '-c', shell(job_source(workflow, 'revision'))],
                                    env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            return output.read_text() if output.exists() else ''

    def test_migration_revision_matches_ci_run_and_main(self):
        self.assertEqual(self.revision('migrate.yml'), f'sha={SHA}\n')
        self.assertEqual(self.revision('migrate.yml', CI_SHA='b' * 40), '')
        self.assertEqual(self.revision('migrate.yml', MAIN_SHA='b' * 40), '')
        self.assertEqual(self.revision('migrate.yml', EVENT='workflow_dispatch', CI_SHA=''), f'sha={SHA}\n')

    def test_deploy_requires_actual_migrations_and_current_revision(self):
        self.assertEqual(self.revision('deploy-hosted.yml'), f'sha={SHA}\n')
        self.assertEqual(self.revision('deploy-hosted.yml', MAIN_SHA='b' * 40), '')
        self.assertEqual(self.revision('deploy-hosted.yml', MIGRATIONS_PASSED='false'), '')
        source = job_source('deploy-hosted.yml', 'revision')
        self.assertIn('length == 2 and all(.[]; .conclusion == "success")', source)
