# Runs inside the pinned official ZAP image. Secret input arrives over stdin,
# never Docker argv/env, and only lives on the container's disposable tmpfs.
import json
import os
from pathlib import Path
import subprocess
import sys

os.umask(0o077)
config = json.load(sys.stdin)
Path('/tmp/wolf-zap.json').write_text(json.dumps(config))
Path('/tmp/wolf-zap-hook.py').write_text('''
import json
import re
from pathlib import Path
from urllib.parse import urlsplit

def zap_started(zap, target):
    config = json.loads(Path('/tmp/wolf-zap.json').read_text())
    proxy = urlsplit(config['proxy'])
    if zap.network.set_http_proxy(proxy.hostname, proxy.port, username=proxy.username, password=proxy.password) != 'OK':
        raise RuntimeError('Cannot configure approved-target proxy')
    if zap.network.set_http_proxy_auth_enabled(True) != 'OK' or zap.network.set_http_proxy_enabled(True) != 'OK':
        raise RuntimeError('Cannot enable approved-target proxy')
    for exclusion in zap.network.get_http_proxy_exclusions:
        zap.network.remove_http_proxy_exclusion(exclusion['host'])
    url = urlsplit(config['target']['url'])
    scope = '^' + re.escape(url.scheme + '://' + url.netloc) + r'(?:/|$).*'
    for name, value in (config.get('headers') or {}).items():
        if zap.replacer.add_rule('Wolf ' + name, True, 'REQ_HEADER', False, name, value, url=scope) != 'OK':
            raise RuntimeError('Cannot configure auth header')

def zap_pre_shutdown(zap):
    Path('/tmp/wolf-zap-report.json').write_text(zap.core.jsonreport())
''')
t = config['target']
script = {'baseline': 'zap-baseline.py', 'full': 'zap-full-scan.py', 'api': 'zap-api-scan.py'}[t['mode']]
args = [script, '-t', t['api_spec_url'] if t['mode'] == 'api' else t['url'],
        '--hook', '/tmp/wolf-zap-hook.py', '-T', '5', '-s',
        '-z', '-dir /tmp/zap-home -config autoupdate.checkOnStart=false -config autoupdate.downloadNewRelease=false']
if t['mode'] == 'baseline':
    args += ['--autooff', '-m', '1']
elif t['mode'] == 'api':
    args += ['-f', t['api_format'], '-S']  # API imports are passive by default.
elif not t['active_approved']:
    raise RuntimeError('Active scan approval required')
# Packaged scripts may echo responses or auth configuration on failure. Only
# publish the structured report; Wolf reports failures without secret-bearing logs.
result = subprocess.run(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
report = Path('/tmp/wolf-zap-report.json')
print(json.dumps({'exit_code': result.returncode, 'report': json.loads(report.read_text()) if report.exists() else None}))
