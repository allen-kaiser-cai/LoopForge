#!/bin/bash
# 云池宿主机执行：对每个仓库的开发分支相对基线 diff 做 Open Code Review
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
found=0
worst=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  OUT="./${repo}/ocr-report.md"
  if ! repo_changed "$repo" "$BASE" "$WORK"; then { echo "# Open Code Review 报告"; echo; echo "- 仓库：${repo}"; echo "- 结论：无代码变更/分支不存在，跳过"; } > "$OUT"; continue; fi
  echo "OCR 审核：${repo} ${BASE} -> ${WORK}"
  {
    echo "# Open Code Review 报告"
    echo
    echo "- 仓库：${repo}"
    echo "- 基线：${BASE}"
    echo "- 开发分支：${WORK}"
    echo
  } > "$OUT"
  if ! command -v ocr >/dev/null 2>&1; then
    echo "OCR 未安装，跳过审核" >> "$OUT"; continue
  fi
  TMP=$(mktemp)
  ( cd "$repo" && ocr review --from "$BASE" --to "$WORK" --format text ) > "$TMP" 2>&1
  RC=$?
  cat "$TMP" >> "$OUT"
  if grep -qiE "Error: invalid|Error: unknown|not logged|unauthorized|API key" "$TMP"; then
    echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**OCR 执行失败（配置/鉴权问题），需人工检查。**" >> "$OUT"
    rm -f "$TMP"; worst=3; continue
  fi
  if [ $RC -ne 0 ]; then
    echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**发现严重问题，需要打回开发修正。**" >> "$OUT"
    [ $worst -lt 2 ] && worst=2
  else
    echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**未发现严重问题，通过。**" >> "$OUT"
  fi
  rm -f "$TMP"
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
exit $worst
