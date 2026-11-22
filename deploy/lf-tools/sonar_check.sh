#!/bin/bash
# 云池宿主机执行：拉 SonarQube 问题数，与上一次分析对比；增加则告警
set -uo pipefail
SONAR_URL="${SONAR_URL:-http://192.168.104.86:9000}"
SONAR_USER="${SONAR_USERNAME:-admin}"
SONAR_PASS="${SONAR_PASSWORD:-Wn@admin1234}"
STATE_DIR=/opt/lf-tools/state
mkdir -p "$STATE_DIR"
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
REPO="${REPO:-unknown}"
cd "$REPO" 2>/dev/null || true
case "$REPO" in
  sspinteractactivity-project) KEY="ssp-v2_ssp-interact-activity_43c584a9-fee1-40e3-b862-38a6fc9aae39" ;;
  ssporder-project) KEY="ssp-v2_ssp-order_0d20628e-4edc-4c53-a753-ba740ff98647" ;;
  sspuser-project) KEY="ssp-v2_ssp-user_b270b131-f7cd-4af9-a795-d9f19d4e0205" ;;
  sspchannel-project) KEY="ssp-v2_ssp-channel_948b7155-5651-4261-9958-f25510aa8e41" ;;
  sspmerchant-project) KEY="ssp-v2_ssp-merchant_54945e11-8ea1-4b67-80e5-05d3538d36f2" ;;
  sspmall-project) KEY="ssp-v2_ssp-mall_aec2f4b5-3888-488d-8b4e-f4ea4b3a84b3" ;;
  sspcustomized-project) KEY="ssp-v2_ssp-customized_13dcaea3-05ed-439e-8782-f104fe3c5e20" ;;
  sspgoods-project) KEY="ssp-v2_ssp-goods_03ac183c-c14d-40a6-83af-d875b7f58d01" ;;
  sspmarketingactivity-project) KEY="ssp-v2_ssp-marketing-activity_9fe20422-40a1-4ef9-9dc4-6db49e15df72" ;;
  ssppopular-project) KEY="ssp-v2_ssp-popular_7055d6f4-3bb2-4536-ba15-a43b6bd414f5" ;;
  sspstoredcard-project) KEY="ssp-v2_ssp-stored-card_77a997a1-7ff2-41ae-80c6-605ae4cb3034" ;;
  sspwidget-project) KEY="ssp-v2_ssp-widget_df3b9ca8-10d8-49c5-aaa7-53289d0f0dde" ;;
  sspdatareport-project) KEY="ssp-datareport" ;;
  *) KEY="" ;;
esac
OUT="./sonar-report.md"
{
  echo "# SonarQube 扫描报告"
  echo
  echo "- 仓库：$REPO"
  echo "- 项目 key：${KEY:-未配置}"
} > "$OUT"
if [ -z "$KEY" ]; then echo "未配置 Sonar 项目映射，跳过" >> "$OUT"; exit 0; fi
RESP=$(curl -s -m 30 -u "$SONAR_USER:$SONAR_PASS" "$SONAR_URL/api/issues/search?componentKeys=$KEY&resolved=false&ps=1&facets=severities" || true)
TOTAL=$(echo "$RESP" | python3 -c "import json,sys;print(json.load(sys.stdin).get('total',0))" 2>/dev/null || echo 0)
echo "" >> "$OUT"; echo "- 当前未解决问题数：**$TOTAL**" >> "$OUT"
STATE="$STATE_DIR/sonar-$(echo "$KEY" | md5sum | cut -c1-12).json"
PREV=$(python3 -c "import json;print(json.load(open('$STATE')).get('total',''))" 2>/dev/null || echo "")
if [ -n "$PREV" ]; then
  echo "- 上一次分析问题数：$PREV" >> "$OUT"
  if [ "$TOTAL" -gt "$PREV" ]; then
    DIFF=$((TOTAL-PREV))
    echo "" >> "$OUT"
    echo "> ⚠️ 问题数增加 **$DIFF** 个，需要关注。" >> "$OUT"
    python3 - "$DIFF" "$TOTAL" "$PREV" "$REPO" <<'PY'
import json,os,sys,urllib.request
diff,total,prev,repo=sys.argv[1:5]
try:
    env={}
    for line in open('/opt/lf-notify/env'):
        line=line.strip()
        if '=' in line and not line.startswith('#'):
            k,v=line.split('=',1); env[k]=v
    appkey=env.get('DING_APPKEY',''); secret=env.get('DING_APPSECRET','')
    users=json.load(open('/opt/lf-notify/ding-users.json'))
    uid=users.get('caiyihua','')
    if not (appkey and secret and uid): raise SystemExit(0)
    tok=json.loads(urllib.request.urlopen(urllib.request.Request(
        f'https://oapi.dingtalk.com/gettoken?appkey={appkey}&appsecret={secret}')).read())['access_token']
    text=f"### SonarQube 问题数增加\n\n仓库：{repo}\n\n当前：{total}（上次 {prev}，增加 {diff}）\n\n请开发关注新增问题。"
    body=json.dumps({"agent_id":appkey,"msg":{"msgtype":"markdown","markdown":{"title":"Sonar 告警","text":text}},"to_all_user":False,"userid_list":uid}).encode()
    urllib.request.urlopen(urllib.request.Request(
        f'https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2?access_token={tok}',
        data=body, headers={'Content-Type':'application/json'}))
    print('sonar alert sent')
except Exception as e:
    print('sonar alert skipped:',e)
PY
  fi
fi
python3 -c "import json;json.dump({'total':$TOTAL},open('$STATE','w'))"
echo "" >> "$OUT"; echo "> 对比基准：同一 Sonar 项目上一次分析。" >> "$OUT"
exit 0
