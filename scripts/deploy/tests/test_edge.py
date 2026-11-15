"""Exercise the edge Worker deploy guard without network or Cloudflare access."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
GOOD_DRY_RUN = '\n'.join([
    'env.ROUTES (fb5abe1e)                 KV Namespace',
    'env.LIMITS (8fb5e7ec)                 KV Namespace',
    'env.BUCKET (dropway-content)          R2 Bucket',
    'env.RATE_LIMITER (600 requests/60s)   Rate Limit',
    '--dry-run: exiting now.',
])
FAKE_PNPM = r'''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ['CALL_LOG'], 'a') as f:
    f.write(json.dumps(['pnpm', *args]) + '\n')
if '--dry-run' in args:
    print(os.environ['DRY_RUN_OUTPUT'])
    sys.exit(int(os.environ.get('DRY_RUN_EXIT', '0')))
'''
TOML = 'name = "{name}"\nmain = "src/index.ts"\n'


class EdgeDeployTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cwd = Path(self.temp.name)
        self.worker = self.cwd / 'edge/serving-worker'
        self.worker.mkdir(parents=True)
        self.write_toml('dropway-production')
        bin_path = self.cwd / 'bin'
        bin_path.mkdir()
        pnpm = bin_path / 'pnpm'
        pnpm.write_text(FAKE_PNPM)
        pnpm.chmod(0o755)
        self.log = self.cwd / 'calls'
        self.env = {**os.environ, 'PATH': f'{bin_path}:{os.environ["PATH"]}', 'CALL_LOG': str(self.log),
                    'SOURCE_SHA': 'a' * 40, 'CLOUDFLARE_API_TOKEN': 'fake-token',
                    'DRY_RUN_OUTPUT': GOOD_DRY_RUN, 'GITHUB_STEP_SUMMARY': str(self.cwd / 'summary')}

    def write_toml(self, name):
        (self.worker / 'wrangler.toml').write_text(TOML.format(name=name))

    def run_script(self):
        result = subprocess.run(['bash', str(ROOT / 'scripts/deploy/edge.sh')], cwd=self.cwd,
                                env=self.env, capture_output=True, text=True)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        deployed = [c for c in calls if 'deploy' in c and '--dry-run' not in c]
        return result, deployed

    def test_deploys_tagged_revision_after_clean_dry_run(self):
        result, deployed = self.run_script()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(deployed, [['pnpm', 'exec', 'wrangler', 'deploy', '--tag', 'a' * 12,
                                     '--message', 'Deploy ' + 'a' * 40]])

    def test_missing_rate_limiter_binding_blocks_deploy(self):
        self.env['DRY_RUN_OUTPUT'] = GOOD_DRY_RUN.replace('env.RATE_LIMITER', 'env.OTHER')
        result, deployed = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('RATE_LIMITER', result.stdout)
        self.assertEqual(deployed, [])

    def test_ignored_config_fields_block_deploy(self):
        self.env['DRY_RUN_OUTPUT'] = 'Unexpected fields found in top-level field: "ratelimit"\n' + GOOD_DRY_RUN
        result, deployed = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(deployed, [])

    def test_failed_dry_run_blocks_deploy(self):
        self.env['DRY_RUN_EXIT'] = '1'
        result, deployed = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(deployed, [])

    def test_wrong_worker_name_stops_before_wrangler(self):
        self.write_toml('dropway-serving-worker')
        result, _ = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())

    def test_missing_token_stops_before_wrangler(self):
        self.env['CLOUDFLARE_API_TOKEN'] = ''
        result, _ = self.run_script()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('CLOUDFLARE_API_TOKEN', result.stderr)
        self.assertFalse(self.log.exists())


class EdgeWorkflowTests(unittest.TestCase):
    def test_edge_deploys_only_after_hosted_services(self):
        source = (ROOT / '.github/workflows/deploy-hosted.yml').read_text()
        job = source.split('  deploy-edge:\n', 1)[1]
        self.assertIn('    needs: [revision, deploy-production]\n', job)
        # Inherit deploy-production's gating; an `if` on revision outputs can be
        # silently false forever if those outputs change.
        self.assertNotIn('\n    if:', job)
        self.assertIn('    environment: production\n', job)
        self.assertIn('ref: ${{ needs.revision.outputs.sha }}', job)
        self.assertIn('CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}', job)
        self.assertIn('run: scripts/deploy/edge.sh', job)

    def test_edge_job_node_satisfies_wrangler(self):
        # The job once ran Node 20 while wrangler required >= 22, so the deploy
        # failed in CI only. Read wrangler's requirement from the lockfile.
        pkg = json.loads((ROOT / 'edge/serving-worker/package.json').read_text())
        version = pkg['devDependencies']['wrangler']
        lock = (ROOT / 'pnpm-lock.yaml').read_text()
        entry = lock.split(f'\n  wrangler@{version}:\n', 1)[1].split('\n\n', 1)[0]
        required = int(re.search(r"node: '>=(\d+)", entry)[1])
        source = (ROOT / '.github/workflows/deploy-hosted.yml').read_text()
        job = source.split('  deploy-edge:\n', 1)[1]
        node = int(re.search(r'node-version: "(\d+)"', job)[1])
        self.assertGreaterEqual(node, required)


if __name__ == '__main__':
    unittest.main()
