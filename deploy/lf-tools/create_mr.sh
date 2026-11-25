#!/bin/bash
# 上线 MR（多仓库）：每个仓库同步基线、去重、无差异跳过、失败告警但不终止
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
GL_TOKEN="${AGENTFLOW_GIT_TOKEN:-}"
alert() {
  python3 - "$1" <<'PY'
import json,sys,urllib.request
text=sys.argv[1]
try:
    env={}
    for line in open('/opt/lf-notify/env'):
        line=line.strip()
        if '=' in line and not line.startswith('#'):
            k,v=line.split('=',1); env[k]=v
    appkey=env.get('DING_APPKEY',''); secret=env.get('DING_APPSECRET','')
    users=json.load(open('/opt/lf-notify/ding-users.json'))
    uid=','.join([x for x in (users.get('caiyihua',''),users.get('majunjie','')) if x]).strip(',')
    tok=json.loads(urllib.request.urlopen(urllib.request.Request(f'https://oapi.dingtalk.com/gettoken?appkey={appkey}&appsecret={secret}')).read())['access_token']
    body=json.dumps({"agent_id":appkey,"msg":{"msgtype":"markdown","markdown":{"title":"LoopForge 上线MR需要人工处理","text":text}},"to_all_user":False,"userid_list":uid}).encode()
    urllib.request.urlopen(urllib.request.Request(f'https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2?access_token={tok}',data=body,headers={'Content-Type':'application/json'}))
except Exception as e:
    print('alert failed',e)
PY
}
found=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  OUT="./${repo}/delivery.md"
  if ! repo_changed "$repo" "$BASE" "$WORK"; then { echo "# 上线 MR 处理结果"; echo; echo "- ${repo}：无代码变更/分支不存在，跳过"; } > "$OUT"; continue; fi
  echo "# 上线 MR 处理结果" > "$OUT"; echo >> "$OUT"
  (
    cd "$repo" || exit 0
    ORIGIN_URL=$(git remote get-url origin 2>/dev/null || echo "")
    SERVER="$(python3 -c "import sys,urllib.parse;u=sys.argv[1];p=urllib.parse.urlparse(u);print(p.scheme+'://'+p.netloc)" "$ORIGIN_URL" 2>/dev/null)"
    PROJECT_PATH="$(python3 -c "import sys,urllib.parse;u=sys.argv[1];print(urllib.parse.urlparse(u).path.lstrip('/').removesuffix('.git'))" "$ORIGIN_URL" 2>/dev/null)"
    ENC=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$PROJECT_PATH")
    git fetch -q origin "$BASE" "$WORK" 2>/dev/null || true
    UNPICKED=$(git cherry "origin/$BASE" "origin/$WORK" 2>/dev/null | grep -c '^+' || true)
    if git merge-base --is-ancestor "origin/$WORK" "origin/$BASE" 2>/dev/null || [ "${UNPICKED:-1}" -eq 0 ]; then
      echo "- ${repo}：开发分支已在基线中，**无需上线**" >> "$OUT"; exit 0
    fi
    DIFF=$(git diff --name-only "origin/$BASE...origin/$WORK" | wc -l)
    if [ "$DIFF" -eq 0 ]; then echo "- ${repo}：与基线无差异，**无需上线**" >> "$OUT"; exit 0; fi
    git checkout -q -B "lf-deliver-$WORK" "origin/$WORK" 2>/dev/null || true
    if ! git merge --no-edit "origin/$BASE" >/tmp/mr_merge_$repo.log 2>&1; then
      echo "- ${repo}：基线同步冲突，需人工处理" >> "$OUT"
      alert "### 上线 MR 需要人工处理

仓库：${repo}
分支：${WORK}
基线：${BASE}
原因：同步基线冲突"
      exit 0
    fi
    git push -q origin "lf-deliver-$WORK:$WORK" 2>/dev/null || true
    EXIST=$(curl -s -m 30 -H "PRIVATE-TOKEN: $GL_TOKEN" "$SERVER/api/v4/projects/$ENC/merge_requests?source_branch=$WORK&target_branch=$BASE&state=opened" 2>/dev/null)
    IID=$(echo "$EXIST" | python3 -c "import json,sys
try:
    d=json.load(sys.stdin); print(d[0]['iid'] if d else '')
except Exception: print('')")
    if [ -n "$IID" ]; then echo "- ${repo}：复用上线 MR **!${IID}**" >> "$OUT"; exit 0; fi
    TITLE="修复(${BASE})：$(git log -1 --format=%s "origin/$WORK" | head -c 80)"
    RESP=$(curl -s -m 30 -X POST -H "PRIVATE-TOKEN: $GL_TOKEN" \
      --data-urlencode "source_branch=$WORK" --data-urlencode "target_branch=$BASE" \
      --data-urlencode "title=$TITLE" --data-urlencode "remove_source_branch=false" \
      "$SERVER/api/v4/projects/$ENC/merge_requests" 2>/dev/null)
    NEW_IID=$(echo "$RESP" | python3 -c "import json,sys
try: print(json.load(sys.stdin).get('iid',''))
except Exception: print('')")
    if [ -n "$NEW_IID" ]; then
      echo "- ${repo}：已创建上线 MR **!${NEW_IID}**（目标 ${BASE}）" >> "$OUT"
    else
      echo "- ${repo}：MR 创建失败，需人工处理" >> "$OUT"
      alert "### 上线 MR 创建失败

仓库：${repo}
分支：${WORK}
基线：${BASE}
接口响应：$(echo "$RESP" | head -c 300)"
    fi
  )
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
exit 0
