#!/bin/bash
# 生成《上线内容清单》（多仓库）：每个仓库一份，汇总版本、提交、变更范围
set -uo pipefail
[ -f /opt/lf-tools/skip_unchanged.sh ] && . /opt/lf-tools/skip_unchanged.sh
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
found=0
for d in */; do
  [ -d "${d}.git" ] || continue
  found=1
  repo="${d%/}"
  OUT="./${repo}/release-summary.md"
  if ! repo_changed "$repo" "$BASE" "$WORK"; then { echo "# 上线内容清单｜${repo}"; echo; echo "- 结论：无代码变更/分支不存在，跳过"; } > "$OUT"; continue; fi
  (
    cd "$repo"
    git fetch -q origin "$BASE" "$WORK" 2>/dev/null || true
    B="origin/$BASE"; W="origin/$WORK"
    version_of(){ git show "$1:pom.xml" 2>/dev/null | python3 -c "
import sys,re
xml=sys.stdin.read()
m=re.search(r'<revision>([^<]+)</revision>',xml)
v=m.group(1).strip() if m else ''
if not v or '\$' in v:
    mm=re.search(r'</artifactId>\\s*<version>([^<]+)</version>',xml)
    v=mm.group(1).strip() if mm else ''
print(v)
" 2>/dev/null; }
    V_OLD=$(version_of "$B"); V_NEW=$(version_of "$W")
    COUNT=$(git rev-list --count --no-merges "$B..$W" 2>/dev/null || echo 0)
    COMMITS=$(git log --no-merges --format='- %h %s（%an）' "$B..$W" 2>/dev/null | head -20)
    STAT=$(git diff --stat "$B...$W" 2>/dev/null | tail -1)
    FILES=$(git diff --name-only "$B...$W" 2>/dev/null)
    SQL_CHANGED=$(echo "$FILES" | grep -Ei '\.sql$|mapper.*\.xml$|entity/|/DO/' | head -5)
    echo "# 上线内容清单｜${repo}"
    echo
    echo "## 项目与分支"
    echo
    echo "- 仓库：${repo}"
    echo "- 生产分支：\`origin/${BASE}\`"
    echo "- 上线分支：\`${WORK}\`"
    echo "- 版本变化：**${V_OLD:-?} → ${V_NEW:-?}**"
    echo "- 提交数：${COUNT}"
    echo
    echo "## 升级内容"
    echo
    [ -n "$COMMITS" ] && echo '```' && echo "$COMMITS" && echo '```' || echo "（无新增提交）"
    echo
    echo "## 变更范围"
    echo
    echo "${STAT:-无统计}"
    echo
    echo "## SQL 变更"
    echo
    if [ -z "$SQL_CHANGED" ]; then echo "- [x] 无 SQL/实体变更（扫描确认）"; else echo "- [ ] 疑似变更，请确认："; echo "$SQL_CHANGED" | sed 's/^/  - /'; fi
    echo
    echo "## 回滚方案"
    echo
    echo "- 代码回滚：回退到 ${V_OLD:-上一稳定版本} 重新部署"
  ) > "$OUT"
done
[ $found -eq 0 ] && { echo "未找到 git 仓库" >&2; exit 1; }
# 根目录再生成一份汇总，兼容根级契约
{
  echo "# 上线内容清单（汇总）"
  echo
  echo "- 基线：${BASE}"
  echo "- 上线分支：${WORK}"
  echo
  for f in */release-summary.md; do [ -f "$f" ] && echo "## $(dirname "$f")" && sed -n '1,20p' "$f" | sed '1d' && echo; done
} > ./release-summary.md
echo "已生成多仓库上线内容清单"
