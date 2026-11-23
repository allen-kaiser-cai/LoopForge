#!/bin/bash
# 合并前编译检查：编译主代码 + 测试代码（不执行单测），失败则阻断合并
set -uo pipefail
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
[ -z "$REPO" ] && { echo "未找到 git 仓库" >&2; exit 1; }
cd "$REPO"
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
OUT="./compile-report.md"
git fetch -q origin "$BASE" "$WORK" 2>/dev/null || true
# 从 diff 识别需要编译的模块
MODULES=$(git diff --name-only "origin/$BASE...origin/$WORK" 2>/dev/null | awk -F/ '{print $1}' | sort -u | while read d; do [ -d "$d" ] && [ -f "$d/pom.xml" ] && echo "$d"; done | tr '\n' ',' | sed 's/,$//')
if [ -z "$MODULES" ]; then MODULES="."; fi
echo "编译检查模块：$MODULES"
LOG=$(mktemp)
mvn -B -q -pl "$MODULES" -am -DskipTests package > "$LOG" 2>&1
RC=$?
{
  echo "# 编译检查报告"
  echo
  echo "- 仓库：$REPO"
  echo "- 分支：$WORK（基线 $BASE）"
  echo "- 检查模块：\`$MODULES\`"
  echo "- 命令：\`mvn -B -pl $MODULES -am -DskipTests package\`"
  echo
  if [ $RC -eq 0 ]; then
    echo "## 结论：编译通过 ✅"
  else
    echo "## 结论：编译失败 ❌"
    echo
    echo '```'
    tail -60 "$LOG"
    echo '```'
  fi
} > "$OUT"
if [ $RC -ne 0 ]; then echo "编译失败，阻断合并" >&2; fi
rm -f "$LOG"
exit $RC
