#!/bin/bash
# 合并前编译检查（多仓库）：Maven 仓库编译 Java；前端仓库执行 npm 构建；无变更仓库跳过
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
FRONT_SCRIPT="${AGENTFLOW_FRONT_BUILD_SCRIPT:-build}"
found=0; worst=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  OUT="./${repo}/compile-report.md"
  if ! repo_changed "$repo" "$BASE" "$WORK"; then
    { echo "# 编译检查报告"; echo; echo "- 仓库：${repo}"; echo "- 结论：无代码变更/分支不存在，跳过"; } > "$OUT"; continue
  fi
  if [ -f "${repo}/pom.xml" ]; then
    MODULES=$(cd "$repo" && git diff --name-only "origin/${BASE}...origin/${WORK}" 2>/dev/null | awk -F/ '{print $1}' | sort -u | while read m; do [ -d "$m" ] && [ -f "$m/pom.xml" ] && echo "$m"; done | tr '\n' ',' | sed 's/,$//')
    [ -z "$MODULES" ] && MODULES="."
    LOG=$(mktemp)
    ( cd "$repo" && mvn -B -q -pl "$MODULES" -am -DskipTests package ) > "$LOG" 2>&1
    RC=$?
    {
      echo "# 编译检查报告"; echo; echo "- 仓库：${repo}"; echo "- 类型：Maven"; echo "- 模块：\`${MODULES}\`"; echo
      if [ $RC -eq 0 ]; then echo "## 结论：编译通过 ✅"; else echo "## 结论：编译失败 ❌"; echo; echo '```'; tail -60 "$LOG"; echo '```'; fi
    } > "$OUT"
    rm -f "$LOG"; [ $RC -ne 0 ] && worst=1
  elif [ -f "${repo}/package.json" ]; then
    LOG=$(mktemp)
    (
      cd "$repo" || exit 1
      if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
      npm run "$FRONT_SCRIPT"
    ) > "$LOG" 2>&1
    RC=$?
    {
      echo "# 编译检查报告"; echo; echo "- 仓库：${repo}"; echo "- 类型：Node 前端"; echo "- 构建命令：\`npm run ${FRONT_SCRIPT}\`"; echo
      if [ $RC -eq 0 ]; then echo "## 结论：前端构建通过 ✅"; else echo "## 结论：前端构建失败 ❌"; echo; echo '```'; tail -80 "$LOG"; echo '```'; fi
    } > "$OUT"
    rm -f "$LOG"; [ $RC -ne 0 ] && worst=1
  else
    { echo "# 编译检查报告"; echo; echo "- 仓库：${repo}"; echo "- 结论：非 Maven/Node 项目，跳过"; } > "$OUT"
  fi
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
exit $worst
