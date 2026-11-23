#!/bin/bash
# 合并前启动检查：使用 daily profile 启动服务，确认能正常起来
set -uo pipefail
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
[ -z "$REPO" ] && { echo "未找到 git 仓库" >&2; exit 1; }
cd "$REPO"
OUT="./startup-report.md"
PROFILE="${STARTUP_PROFILE:-daily}"
TIMEOUT="${STARTUP_TIMEOUT:-240}"
JAR=$(find . -path '*/target/*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*original*.jar' 2>/dev/null | grep -v '/common/' | grep -v '/sdk/' | head -1)
if [ -z "$JAR" ]; then
  # 先尝试打包
  mvn -B -q -DskipTests package >/tmp/startup_pkg.log 2>&1 || true
  JAR=$(find . -path '*/target/*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*original*.jar' 2>/dev/null | grep -v '/common/' | grep -v '/sdk/' | head -1)
fi
{
  echo "# 启动检查报告"
  echo
  echo "- 仓库：$REPO"
  echo "- profile：$PROFILE"
  echo "- jar：${JAR:-未找到}"
  echo
} > "$OUT"
if [ -z "$JAR" ]; then
  echo "未找到可启动 jar，启动检查失败" >> "$OUT"
  exit 1
fi
LOG=/tmp/startup_$(date +%s).log
java -jar "$JAR" --spring.profiles.active="$PROFILE" --server.port=0 > "$LOG" 2>&1 &
PID=$!
STARTED=0
for i in $(seq 1 $TIMEOUT); do
  if grep -qE "Started .* in .* seconds|Tomcat started on port" "$LOG" 2>/dev/null; then STARTED=1; break; fi
  if grep -qE "APPLICATION FAILED TO START|BUILD FAILURE" "$LOG" 2>/dev/null; then break; fi
  if ! kill -0 $PID 2>/dev/null; then break; fi
  sleep 1
done
kill $PID 2>/dev/null; wait $PID 2>/dev/null
if [ $STARTED -eq 1 ]; then
  echo "## 结论：服务启动成功 ✅" >> "$OUT"
  grep -E "Started .* in .* seconds|Tomcat started on port" "$LOG" | tail -3 >> "$OUT"
  exit 0
fi
echo "## 结论：服务启动失败 ❌" >> "$OUT"
echo '' >> "$OUT"; echo '```' >> "$OUT"; tail -80 "$LOG" >> "$OUT"; echo '```' >> "$OUT"
exit 1
