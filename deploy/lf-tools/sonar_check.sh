#!/bin/bash
# 云池宿主机执行：对每个仓库拉 SonarQube 问题数并与上次对比
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
SONAR_URL="${SONAR_URL:-http://192.168.104.86:9000}"
SONAR_USER="${SONAR_USERNAME:-admin}"
SONAR_PASS="${SONAR_PASSWORD:-Wn@admin1234}"
STATE_DIR=/opt/lf-tools/state
mkdir -p "$STATE_DIR"
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
key_of() {
  case "$1" in
    sspinteractactivity-project) echo "ssp-v2_ssp-interact-activity_43c584a9-fee1-40e3-b862-38a6fc9aae39" ;;
    ssporder-project) echo "ssp-v2_ssp-order_0d20628e-4edc-4c53-a753-ba740ff98647" ;;
    sspuser-project) echo "ssp-v2_ssp-user_b270b131-f7cd-4af9-a795-d9f19d4e0205" ;;
    sspchannel-project) echo "ssp-v2_ssp-channel_948b7155-5651-4261-9958-f25510aa8e41" ;;
    sspmerchant-project) echo "ssp-v2_ssp-merchant_54945e11-8ea1-4b67-80e5-05d3538d36f2" ;;
    sspmall-project) echo "ssp-v2_ssp-mall_aec2f4b5-3888-488d-8b4e-f4ea4b3a84b3" ;;
    sspcustomized-project) echo "ssp-v2_ssp-customized_13dcaea3-05ed-439e-8782-f104fe3c5e20" ;;
    sspgoods-project) echo "ssp-v2_ssp-goods_03ac183c-c14d-40a6-83af-d875b7f58d01" ;;
    sspmarketingactivity-project) echo "ssp-v2_ssp-marketing-activity_9fe20422-40a1-4ef9-9dc4-6db49e15df72" ;;
    ssppopular-project) echo "ssp-v2_ssp-popular_7055d6f4-3bb2-4536-ba15-a43b6bd414f5" ;;
    sspstoredcard-project) echo "ssp-v2_ssp-stored-card_77a997a1-7ff2-41ae-80c6-605ae4cb3034" ;;
    sspwidget-project) echo "ssp-v2_ssp-widget_df3b9ca8-10d8-49c5-aaa7-53289d0f0dde" ;;
    sspdatareport-project) echo "ssp-datareport" ;;
    *) echo "" ;;
  esac
}
found=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  KEY=$(key_of "$repo")
  if ! repo_changed "$repo" "${AGENTFLOW_BASE_BRANCH:-master}" "${AGENTFLOW_WORK_BRANCH:-HEAD}"; then { echo "# SonarQube 扫描报告"; echo; echo "- 仓库：${repo}"; echo "- 结论：无代码变更/分支不存在，跳过"; } > "./${repo}/sonar-report.md"; continue; fi
  OUT="./${repo}/sonar-report.md"
  {
    echo "# SonarQube 扫描报告"
    echo
    echo "- 仓库：${repo}"
    echo "- 项目 key：${KEY:-（未配置，跳过）}"
    echo
  } > "$OUT"
  if [ -z "$KEY" ]; then
    echo "未配置 Sonar 项目 key，跳过。" >> "$OUT"; continue
  fi
  CUR=$(curl -s -m 20 -u "${SONAR_USER}:${SONAR_PASS}" "${SONAR_URL}/api/measures/component?component=${KEY}&metricKeys=bugs,vulnerabilities,code_smells" 2>/dev/null | python3 -c "import json,sys
try:
  d=json.load(sys.stdin); m={x['metric']:x.get('value') for x in d['component']['measures']}; print(int(m.get('bugs',0))+int(m.get('vulnerabilities',0))+int(m.get('code_smells',0)))
except Exception: print(-1)")
  if [ "$CUR" = "-1" ]; then
    echo "拉取 Sonar 指标失败（网络或权限），需人工确认。" >> "$OUT"; continue
  fi
  STATE_FILE="${STATE_DIR}/${repo}.issues"
  PREV=$(cat "$STATE_FILE" 2>/dev/null || echo "$CUR")
  echo "- 上次问题数：${PREV}" >> "$OUT"
  echo "- 本次问题数：${CUR}" >> "$OUT"
  if [ "$CUR" -gt "$PREV" ]; then
    echo "" >> "$OUT"; echo "> ⚠️ 问题数增加 $((CUR-PREV))，已通知开发。" >> "$OUT"
  fi
  echo "$CUR" > "$STATE_FILE"
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
exit 0
