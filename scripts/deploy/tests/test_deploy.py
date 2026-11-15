"""Exercise deployment control flow without database or network access."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
FAKE = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
cmd = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['CALL_LOG'], 'a') as f:
    f.write(json.dumps([cmd, *args]) + '\n')
if cmd == 'flyctl' and args[:2] == ['machine', 'list']:
    print(json.dumps([] if os.environ.get('NO_WORKER') else [{'id':'worker-1','state':'stopped'}]))
if cmd == 'flyctl' and args[:2] == ['ssh', 'console'] and os.environ.get('UNHEALTHY_WORKER'):
    sys.exit(1)
if cmd == 'docker' and args[:2] == ['image', 'inspect']:
    if args[3] == '{{.Id}}':
        print('wrong' if os.environ.get('BAD_IMAGE') and 'mcp' in args[-1] else 'sha256:test')
    else:
        print(json.dumps([args[-1].split(':')[0] + '@sha256:1234']))
if cmd == 'flyctl' and args[:3] == ['deploy', '--app', 'dev-mcp'] and os.environ.get('FAIL_MCP'):
    sys.exit(1)
if cmd == os.environ.get('FAIL_COMMAND'):
    sys.exit(1)
if cmd == 'flyctl' and args[0] == 'deploy' and os.environ.get('FAIL_API') and args[2] == 'dev-api':
    sys.exit(1)
'''


class DeployTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cwd = Path(self.temp.name)
        (self.cwd / 'apps/dashboard').mkdir(parents=True)
        bin_path = self.cwd / 'bin'
        bin_path.mkdir()
        for name in ('docker', 'flyctl', 'curl', 'goose', 'pnpm'):
            p = bin_path / name
            p.write_text(FAKE)
            p.chmod(0o755)
        (self.cwd / 'image-ids.json').write_text(json.dumps(dict.fromkeys(('api', 'mcp', 'room-worker'), 'sha256:test')))
        self.log = self.cwd / 'calls'
        self.env = {**os.environ, 'PATH': f'{bin_path}:{os.environ["PATH"]}', 'CALL_LOG': str(self.log),
                    'DEPLOY_ENVIRONMENT': 'development', 'SOURCE_SHA': 'a' * 40,
                    'FLY_API_APP': 'dev-api', 'FLY_MCP_APP': 'dev-mcp',
                    'FLY_API_TOKEN': 'fake', 'FLY_MCP_API_TOKEN': 'fake',
                    'FLY_WORKER_APP': '', 'APP_DSN': 'fake-app', 'BILLING_DSN': 'fake-billing',
                    'GITHUB_STEP_SUMMARY': str(self.cwd / 'summary')}

    def run_script(self, script):
        result = subprocess.run(['bash', str(ROOT / 'scripts/deploy' / script)], cwd=self.cwd,
                                env=self.env, capture_output=True, text=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return result, calls

    def test_promotes_by_digest_in_order(self):
        self.env.update(FLY_WORKER_APP='dev-worker', FLY_WORKER_API_TOKEN='fake')
        result, calls = self.run_script('fly.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        deploys = [c for c in calls if c[:2] == ['flyctl', 'deploy']]
        self.assertEqual([c[3] for c in deploys], ['dev-api', 'dev-mcp', 'dev-worker'])
        for call in deploys:
            self.assertIn(f'registry.fly.io/{call[3]}@sha256:1234', call)
        self.assertEqual(len([c for c in calls if c[0] == 'curl']), 2)

    def test_production_worker_runs_after_services_and_health_checks(self):
        self.env.update(DEPLOY_ENVIRONMENT='production', FLY_WORKER_API_TOKEN='fake')
        result, calls = self.run_script('fly.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        deploys = [c for c in calls if c[:2] == ['flyctl', 'deploy']]
        self.assertEqual([c[3] for c in deploys], ['dev-api', 'dev-mcp', 'dropway-worker-production'])
        worker_index = calls.index(deploys[-1])
        self.assertTrue(all(i < worker_index for i, c in enumerate(calls) if c[0] == 'curl'))

    def test_worker_is_started_and_checked(self):
        self.env.update(DEPLOY_ENVIRONMENT='production', FLY_WORKER_API_TOKEN='fake')
        result, calls = self.run_script('fly.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(any(c[:3] == ['flyctl', 'machine', 'start'] for c in calls))
        self.assertTrue(any('node /worker/dist/readiness.js' in c for c in calls))

    def test_unhealthy_or_missing_worker_fails_release(self):
        for failure in ('UNHEALTHY_WORKER', 'NO_WORKER'):
            with self.subTest(failure=failure):
                self.env.update(DEPLOY_ENVIRONMENT='production', FLY_WORKER_API_TOKEN='fake')
                self.env[failure] = '1'
                result, _ = self.run_script('fly.sh')
                self.assertNotEqual(result.returncode, 0)
                del self.env[failure]

    def test_production_requires_worker_token_before_any_release(self):
        self.env.update(DEPLOY_ENVIRONMENT='production', FLY_WORKER_API_TOKEN='')
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])

    def test_failed_mcp_prevents_production_worker(self):
        self.env.update(DEPLOY_ENVIRONMENT='production', FLY_WORKER_API_TOKEN='fake', FAIL_MCP='1')
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual([c[3] for c in calls if c[:2] == ['flyctl', 'deploy']], ['dev-api', 'dev-mcp'])

    def test_bad_image_stops_before_any_release(self):
        self.env['BAD_IMAGE'] = '1'
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(any(c[0] == 'flyctl' for c in calls))

    def test_failed_api_prevents_mcp(self):
        self.env['FAIL_API'] = '1'
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len([c for c in calls if c[:2] == ['flyctl', 'deploy']]), 1)

    def test_failed_health_prevents_mcp(self):
        self.env['FAIL_COMMAND'] = 'curl'
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len([c for c in calls if c[:2] == ['flyctl', 'deploy']]), 1)

    def test_missing_worker_token_stops_before_deploy(self):
        self.env.update(FLY_WORKER_APP='dev-worker', FLY_WORKER_API_TOKEN='')
        result, calls = self.run_script('fly.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])

    def test_migration_order_and_billing_ledger(self):
        result, calls = self.run_script('migrate.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual([c[0] for c in calls], ['goose', 'goose', 'pnpm'])
        self.assertIn('db/migrations/app', calls[0])
        self.assertIn('goose_db_version_billing', calls[1])
        self.assertIn('db/migrations/billing', calls[1])

    def test_failed_app_migration_stops_billing_and_identity(self):
        self.env['FAIL_COMMAND'] = 'goose'
        result, calls = self.run_script('migrate.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(calls), 1)


if __name__ == '__main__':
    unittest.main()
