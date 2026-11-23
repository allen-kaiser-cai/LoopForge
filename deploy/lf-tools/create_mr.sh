#!/bin/bash
# 上线 MR：先同步基线，再创建 MR（去重、无差异跳过、失败告警但不终止）
set -uo pipefail
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
[ -z "$REPO" ] && { echo "未找到 git 仓库" >&2; exit 1; }
cd "$REPO"
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
OUT="./delivery.md"
GL_TOKEN="${AGENTFLOW_GIT_TOKEN:-}"
ORIGIN_URL=$(git remote get-url origin 2>/dev/null || echo "")
SERVER="${AGENTFLOW_GIT_SERVER:-}"
PROJECT_PATH="${AGENTFLOW_GIT_PROJECT_PATH:-}"
if [ -z "$SERVER" ] || [ -z "$PROJECT_PATH" ]; then
  PARSED=$(python3 -c "import sys,urllib.parse;u=sys.argv[1];p=urllib.parse.urlparse(u);print(p.scheme+chr(58)+chr(47)+chr(47)+p.netloc);print(p.path.lstrip(chr(47)).removesuffix(chr(46)+chr(103)+chr(105)+chr(116)))" "$ORIGIN_URL" 2>/dev/null)
  SERVER="${SERVER:-$(echo "$PARSED" | sed -n 1p)}"
  PROJECT_PATH="${PROJECT_PATH:-$(echo "$PARSED" | sed -n 2p)}"
fi
SERVER="${SERVER:-http://gitlabnew.inter.wcansoft.com}"
ENC_PATH=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$PROJECT_PATH")

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
    uid=','.join([users.get('caiyihua',''),users.get('majunjie','')]).strip(',')
    tok=json.loads(urllib.request.urlopen(urllib.request.Request(f'https://oapi.dingtalk.com/gettoken?appkey={appkey}&appsecret={secret}')).read())['access_token']
    body=json.dumps({"agent_id":appkey,"msg":{"msgtype":"markdown","markdown":{"title":"LoopForge 上线MR需要人工处理","text":text}},"to_all_user":False,"userid_list":uid}).encode()
    urllib.request.urlopen(urllib.request.Request(f'https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2?access_token={tok}',data=body,headers={'Content-Type':'application/json'}))
    print("alert sent")
except Exception as e:
    print("alert failed",e)
PY
}
{
  echo "# 上线 MR 处理结果"
  echo
} > "$OUT"
git fetch -q origin "+refs/heads/$BASE:refs/remotes/origin/$BASE" "+refs/heads/$WORK:refs/remotes/origin/$WORK" || git fetch -q origin || true
git update-ref -d "refs/remotes/origin/$BASE" 2>/dev/null || true
git fetch -q origin "$BASE" 2>/dev/null || true
# 1) 无差异：无需上线（祖先判断 + git cherry 兜底）
UNPICKED=$(git cherry "origin/$BASE" "origin/$WORK" 2>/dev/null | grep -c '^+' || true)
if git merge-base --is-ancestor "origin/$WORK" "origin/$BASE" 2>/dev/null || [ "${UNPICKED:-1}" -eq 0 ]; then
  echo "- 结论：开发分支已在基线中，**无需上线**" >> "$OUT"
  echo "no diff, skip MR"; exit 0
fi
DIFF=$(git diff --name-only "origin/$BASE...origin/$WORK" | wc -l)
if [ "$DIFF" -eq 0 ]; then
  echo "- 结论：与基线无差异，**无需上线**" >> "$OUT"
  echo "no diff, skip MR"; exit 0
fi
# 2) 同步基线，确保可创建 MR
git checkout -q -B "lf-deliver-$WORK" "origin/$WORK" 2>/dev/null || true
if ! git merge --no-edit "origin/$BASE" >/tmp/mr_merge.log 2>&1; then
  echo "- 结论：**基线同步冲突，需人工处理**" >> "$OUT"
  echo '' >> "$OUT"; echo '```' >> "$OUT"; tail -30 /tmp/mr_merge.log >> "$OUT"; echo '```' >> "$OUT"
  alert "### 上线 MR 需要人工处理

工作分支：$WORK
基线：$BASE
原因：同步基线时冲突，MR 未创建。请人工解决后重试发布段。"
  exit 0
fi
git push -q origin "lf-deliver-$WORK:$WORK" 2>/dev/null || true
# 3) 去重：已有同源同目标 MR
EXIST=$(curl -s -m 30 -H "PRIVATE-TOKEN: $GL_TOKEN" "$SERVER/api/v4/projects/$ENC_PATH/merge_requests?source_branch=$WORK&target_branch=$BASE&state=opened" 2>/dev/null)
IID=$(echo "$EXIST" | python3 -c "import json,sys
try:
    d=json.load(sys.stdin); print(d[0]['iid'] if d else '')
except Exception: print('')")
if [ -n "$IID" ]; then
  echo "- 结论：复用已存在的上线 MR **!$IID**" >> "$OUT"
  echo "reuse MR !$IID"; exit 0
fi
# 4) 创建 MR
TITLE="修复($BASE)：$(git log -1 --format=%s "origin/$WORK" | head -c 80)"
RESP=$(curl -s -m 30 -X POST -H "PRIVATE-TOKEN: $GL_TOKEN" \
  --data-urlencode "source_branch=$WORK" --data-urlencode "target_branch=$BASE" \
  --data-urlencode "title=$TITLE" --data-urlencode "remove_source_branch=false" \
  "$SERVER/api/v4/projects/$ENC_PATH/merge_requests" 2>/dev/null)
NEW_IID=$(echo "$RESP" | python3 -c "import json,sys
try: print(json.load(sys.stdin).get('iid',''))
except Exception: print('')")
if [ -n "$NEW_IID" ]; then
  echo "- 结论：已创建上线 MR **!$NEW_IID**" >> "$OUT"
  echo "- 目标分支：$BASE" >> "$OUT"
  echo "created MR !$NEW_IID"; exit 0
fi
echo "- 结论：**MR 创建失败，需人工处理**" >> "$OUT"
echo '' >> "$OUT"; echo '```' >> "$OUT"; echo "$RESP" | head -20 >> "$OUT"; echo '```' >> "$OUT"
alert "### 上线 MR 创建失败

工作分支：$WORK
基线：$BASE
接口响应：$(echo "$RESP" | head -c 300)"
exit 0
