#!/bin/bash
# 云池宿主机执行：对开发分支相对基线的 diff 做 Open Code Review
set -uo pipefail
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
[ -z "$REPO" ] && { echo "未找到 git 仓库" >&2; exit 1; }
cd "$REPO"
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
echo "OCR 审核：$BASE -> $WORK"
OUT="./ocr-report.md"
{
  echo "# Open Code Review 报告"
  echo
  echo "- 仓库：$REPO"
  echo "- 基线：$BASE"
  echo "- 开发分支：$WORK"
  echo
} > "$OUT"
if ! command -v ocr >/dev/null 2>&1; then
  echo "OCR 未安装，跳过审核" | tee -a "$OUT"; exit 0
fi
TMP=$(mktemp)
ocr review --from "$BASE" --to "$WORK" --format text > "$TMP" 2>&1
RC=$?
cat "$TMP" >> "$OUT"
if grep -qiE "Error: invalid|Error: unknown|not logged|unauthorized|API key" "$TMP"; then
  echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**OCR 执行失败（配置/鉴权问题），需人工检查。**" >> "$OUT"
  rm -f "$TMP"; exit 3
fi
if [ $RC -ne 0 ]; then
  echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**发现严重问题，需要打回开发修正。**" >> "$OUT"
  rm -f "$TMP"; exit 2
fi
echo "" >> "$OUT"; echo "## 结论" >> "$OUT"; echo "**未发现严重问题，通过。**" >> "$OUT"
rm -f "$TMP"; exit 0
