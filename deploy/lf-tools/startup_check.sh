#!/bin/bash
# 合并前启动检查（多仓库）：后端 jar 用 daily profile 启动；前端静态产物用本地服务验证可访问
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
PROFILE="${STARTUP_PROFILE:-daily}"
TIMEOUT="${STARTUP_TIMEOUT:-240}"
found=0; worst=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  OUT="./${repo}/startup-report.md"
  if ! repo_changed "$repo" "$BASE" "$WORK"; then
    { echo "# 启动检查报告"; echo; echo "- 仓库：${repo}"; echo "- 结论：无代码变更/分支不存在，跳过"; } > "$OUT"; continue
  fi
  JAR=$(cd "$repo" && find . -path '*/target/*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-tests.jar' ! -name '*original*.jar' 2>/dev/null | grep -v '/common/' | grep -v '/sdk/' | head -1)
  DIST=$(cd "$repo" && for p in dist build; do [ -f "$p/index.html" ] && { echo "$p"; break; }; done)
  { echo "# 启动检查报告"; echo; echo "- 仓库：${repo}"; echo "- profile：${PROFILE}"; } > "$OUT"
  if [ -n "$JAR" ]; then
    LOG=/tmp/startup_${repo}_$(date +%s).log
    ( cd "$repo" && java -jar "$JAR" --spring.profiles.active="$PROFILE" --server.port=0 ) > "$LOG" 2>&1 &
    PID=$!; STARTED=0
    for i in $(seq 1 $TIMEOUT); do
      grep -qE "Started .* in .* seconds|Tomcat started on port" "$LOG" 2>/dev/null && { STARTED=1; break; }
      grep -qE "APPLICATION FAILED TO START" "$LOG" 2>/dev/null && break
      kill -0 $PID 2>/dev/null || break
      sleep 1
    done
    kill $PID 2>/dev/null; wait $PID 2>/dev/null
    if [ $STARTED -eq 1 ]; then
      echo "" >> "$OUT"; echo "## 结论：后端服务启动成功 ✅" >> "$OUT"; grep -E "Started .* in .* seconds" "$LOG" | tail -2 >> "$OUT"
    else
      echo "" >> "$OUT"; echo "## 结论：后端服务启动失败 ❌（jar：${JAR}）" >> "$OUT"; echo '```' >> "$OUT"; tail -80 "$LOG" >> "$OUT"; echo '```' >> "$OUT"; worst=1
    fi
  elif [ -n "$DIST" ]; then
    PORT=$(python3 -c "import socket;s=socket.socket();s.bind(('',0));print(s.getsockname()[1]);s.close()")
    ( cd "${repo}/${DIST}" && python3 -m http.server "$PORT" >/tmp/static_${repo}.log 2>&1 ) &
    PID=$!
    OK=0
    for i in $(seq 1 30); do
      if curl -sf -m 3 "http://127.0.0.1:${PORT}/" -o /tmp/static_${repo}.html 2>/dev/null; then OK=1; break; fi
      sleep 1
    done
    kill $PID 2>/dev/null; wait $PID 2>/dev/null
    if [ $OK -eq 1 ]; then
      echo "" >> "$OUT"; echo "## 结论：前端静态产物可正常提供访问 ✅（目录：${DIST}，HTTP 200）" >> "$OUT"
      head -3 /tmp/static_${repo}.html >> "$OUT" 2>/dev/null
    else
      echo "" >> "$OUT"; echo "## 结论：前端静态产物启动/访问失败 ❌（目录：${DIST}）" >> "$OUT"; worst=1
    fi
  else
    echo "" >> "$OUT"; echo "## 结论：未发现可启动 jar 或前端构建产物，跳过" >> "$OUT"
  fi
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
exit $worst
