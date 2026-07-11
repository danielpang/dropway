#!/usr/bin/env bash
# Backend end-to-end check for browser folder drag-and-drop deploy.
# Replicates exactly what the dashboard does: sign up -> create+activate org ->
# mint JWT -> create site -> prepare -> verify presign host -> upload blobs ->
# finalize -> publish, plus a CORS preflight against the object store.
# NOT committed as a test; a one-shot operational verifier.
set -euo pipefail
cd /Users/d_pang/projects/dropway

DASH=http://localhost:3000
API=http://localhost:8080
JAR=$(mktemp); STAMP=$(date +%s)
EMAIL="dragdrop-$STAMP@example.com"; PASS="dragdrop-pass-123"
SLUG="dd-$STAMP"; FOLDER=examples/synthwave-sunset
pass=0; fail=0
ok(){ echo "  ✓ $1"; pass=$((pass+1)); }
no(){ echo "  ✗ $1"; fail=$((fail+1)); }

echo "== 1. sign up ($EMAIL) =="
code=$(curl -sS -c "$JAR" -o /tmp/dd_signup.json -w '%{http_code}' -X POST "$DASH/api/auth/sign-up/email" \
  -H 'content-type: application/json' -d "{\"name\":\"Drag Drop\",\"email\":\"$EMAIL\",\"password\":\"$PASS\"}")
[ "$code" = "200" ] && ok "signup 200" || no "signup $code"

# Secure cookies (NODE_ENV=production) won't be resent by curl over http — build the
# Cookie header manually from the jar (ignoring the secure flag).
COOKIE=$(python3 -c "
import sys
out=[]
for ln in open('$JAR'):
    ln=ln.rstrip('\n')
    if not ln or (ln.startswith('#') and not ln.startswith('#HttpOnly_')): continue
    p=ln.split('\t')
    if len(p)>=7: out.append(p[5]+'='+p[6])
print('; '.join(out))
")
[ -n "$COOKIE" ] && ok "session cookie captured" || no "no session cookie"

echo "== 2. create org =="
curl -sS -o /tmp/dd_org.json -w 'HTTP %{http_code}\n' -X POST "$DASH/api/auth/organization/create" \
  -H "cookie: $COOKIE" -H 'content-type: application/json' \
  -d "{\"name\":\"DD Org $STAMP\",\"slug\":\"ddorg-$STAMP\"}"
ORG=$(python3 -c "import json;d=json.load(open('/tmp/dd_org.json'));print(d.get('id') or (d.get('organization') or {}).get('id') or '')")
[ -n "$ORG" ] && ok "org id $ORG" || { no "no org id"; echo "    org.json:"; cat /tmp/dd_org.json; }

echo "== 3. set active org =="
curl -sS -o /dev/null -w 'HTTP %{http_code}\n' -X POST "$DASH/api/auth/organization/set-active" \
  -H "cookie: $COOKIE" -H 'content-type: application/json' -d "{\"organizationId\":\"$ORG\"}"

echo "== 4. mint JWT =="
curl -sS -o /tmp/dd_tok.json -w 'HTTP %{http_code}\n' -H "cookie: $COOKIE" "$DASH/api/auth/token"
JWT=$(python3 -c "import json;print(json.load(open('/tmp/dd_tok.json')).get('token',''))")
[ -n "$JWT" ] && ok "jwt minted (${#JWT} chars)" || no "no jwt"
python3 -c "
import json,base64
t=json.load(open('/tmp/dd_tok.json'))['token']; pl=t.split('.')[1]; pl+='='*(-len(pl)%4)
c=json.loads(base64.urlsafe_b64decode(pl)); print('    claims org_id=%r sub=%r aud=%r iss=%r'%(c.get('org_id'),c.get('sub'),c.get('aud'),c.get('iss')))
import sys; sys.exit(0 if c.get('org_id') else 0)
"

echo "== 5. create site =="
# Omit access_mode → defaults to org_only (Tier b). A brand-new org has
# allow_external_sharing=false, so requesting "public" here is correctly 403'd —
# the dashboard's create dialog likewise creates org_only sites by default.
curl -sS -o /tmp/dd_site.json -w 'HTTP %{http_code}\n' -X POST "$API/v1/sites" \
  -H "authorization: Bearer $JWT" -H 'content-type: application/json' \
  -d "{\"slug\":\"$SLUG\"}"
SITE=$(python3 -c "import json;print(json.load(open('/tmp/dd_site.json')).get('id',''))")
[ -n "$SITE" ] && ok "site id $SITE" || { no "no site id"; cat /tmp/dd_site.json; }

echo "== 6. build manifest + digest from $FOLDER (Go/TS formula) =="
python3 - "$FOLDER" <<'PY'
import hashlib,os,json,sys
root=sys.argv[1]; files=[]
for dp,_,names in os.walk(root):
    for n in names:
        full=os.path.join(dp,n); rel=os.path.relpath(full,root).replace(os.sep,'/')
        data=open(full,'rb').read()
        ct='text/html; charset=utf-8' if rel.endswith('.html') else ('text/css; charset=utf-8' if rel.endswith('.css') else 'application/octet-stream')
        files.append({'path':rel,'sha256':hashlib.sha256(data).hexdigest(),'size':len(data),'content_type':ct})
files.sort(key=lambda f:f['path'])
digest=hashlib.sha256(''.join(f"{f['sha256']}  {f['path']}\n" for f in files).encode()).hexdigest()
json.dump({'manifest':files,'digest':digest},open('/tmp/dd_md.json','w'))
print('    files:',[f['path'] for f in files]); print('    digest:',digest)
PY

echo "== 7. prepare =="
python3 -c "import json;m=json.load(open('/tmp/dd_md.json'));json.dump({'manifest':m['manifest']},open('/tmp/dd_prep_req.json','w'))"
curl -sS -o /tmp/dd_prep.json -w 'HTTP %{http_code}\n' -X POST "$API/v1/sites/$SITE/deployments/prepare" \
  -H "authorization: Bearer $JWT" -H 'content-type: application/json' --data @/tmp/dd_prep_req.json
python3 -c "
import json;d=json.load(open('/tmp/dd_prep.json'));u=d.get('uploads',{});v=list(u.values())
print('    missing:',len(d.get('missing',[])),'| sample url:',(v[0][:70]+'...') if v else '(none)')
open('/tmp/dd_oneurl.txt','w').write(v[0] if v else '')
import sys; sys.exit(0 if (v and 'localhost:9000' in v[0]) else 1)
" && ok "presign host is browser-reachable (localhost:9000)" || no "presign host wrong (S3_PUBLIC_ENDPOINT not applied)"

echo "== 8. upload missing blobs (PUT, no Content-Type — like the browser) =="
python3 - <<'PY'
import json
p=json.load(open('/tmp/dd_prep.json')); m=json.load(open('/tmp/dd_md.json'))
s2p={f['sha256']:f['path'] for f in m['manifest']}
rows=[f"{s}\t{s2p[s]}\t{p['uploads'][s]}" for s in p.get('missing',[])]
open('/tmp/dd_up.tsv','w').write(''.join(r+'\n' for r in rows))  # trailing \n so `while read` sees the last line
PY
upok=1
while IFS=$'\t' read -r sha path url; do
  [ -z "${url:-}" ] && continue
  c=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -T "$FOLDER/$path" -H 'Content-Type:' "$url")
  echo "    PUT $path -> $c"; [ "$c" = "200" ] || upok=0
done < /tmp/dd_up.tsv
[ "$upok" = "1" ] && ok "all blobs uploaded direct to store" || no "a blob PUT failed"

echo "== 9. finalize (server re-hashes blobs + re-derives digest) =="
code=$(curl -sS -o /tmp/dd_fin.json -w '%{http_code}' -X POST "$API/v1/sites/$SITE/deployments" \
  -H "authorization: Bearer $JWT" -H 'content-type: application/json' --data @/tmp/dd_md.json)
VER=$(python3 -c "import json;print(json.load(open('/tmp/dd_fin.json')).get('version_id',''))" 2>/dev/null || true)
[ "$code" = "201" ] && [ -n "$VER" ] && ok "finalize 201, version $VER (digest accepted!)" || { no "finalize $code"; cat /tmp/dd_fin.json; }

echo "== 10. publish (drop -> live) =="
code=$(curl -sS -o /tmp/dd_pub.json -w '%{http_code}' -X POST "$API/v1/sites/$SITE/publish" \
  -H "authorization: Bearer $JWT" -H 'content-type: application/json' -d "{\"version_id\":\"$VER\"}")
LIVE=$(python3 -c "import json;print(json.load(open('/tmp/dd_pub.json')).get('live_url',''))" 2>/dev/null || true)
[ "$code" = "200" ] && [ -n "$LIVE" ] && ok "published live: $LIVE" || { no "publish $code"; cat /tmp/dd_pub.json; }

echo "== 11. CORS preflight on the object store (what the browser sends) =="
ONE=$(cat /tmp/dd_oneurl.txt)
if [ -n "$ONE" ]; then
  hdrs=$(curl -sS -i -X OPTIONS "$ONE" -H 'Origin: http://localhost:3000' -H 'Access-Control-Request-Method: PUT' 2>&1)
  echo "$hdrs" | grep -i 'access-control-allow' | sed 's/^/    /' || true
  echo "$hdrs" | grep -qi 'access-control-allow-origin' && ok "MinIO returns CORS allow-origin" || no "no CORS allow-origin header"
fi

echo ""
echo "RESULT: $pass passed, $fail failed"
rm -f "$JAR"
[ "$fail" = "0" ]
