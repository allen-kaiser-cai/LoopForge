#!/bin/bash
# 生成「上线内容清单」摘要（参考上线内容清单.md 模板，可自动判断的项自动填写）
set -uo pipefail
cd "${AGENTFLOW_WORKSPACE:-$PWD}" 2>/dev/null || true
REPO=""
for d in */; do [ -d "${d}.git" ] && { REPO="${d%/}"; break; }; done
[ -z "$REPO" ] && { echo "未找到 git 仓库" >&2; exit 1; }
cd "$REPO"
BASE="${AGENTFLOW_BASE_BRANCH:-master}"
WORK="${AGENTFLOW_WORK_BRANCH:-HEAD}"
OUT="./release-summary.md"
git fetch -q origin "$BASE" "$WORK" 2>/dev/null || true
B="origin/$BASE"; W="origin/$WORK"
version_of(){
  git show "$1:pom.xml" 2>/dev/null | python3 -c "
import sys,re
xml=sys.stdin.read()
def tag(name):
    m=re.search(r'<%s>([^<]+)</%s>'%(name,name), xml)
    return m.group(1).strip() if m else ''
v=tag('revision')
if not v or '\$' in v:
    # 取根 project 的 version 或 properties 中的 revision
    m=re.search(r'<properties>.*?</properties>', xml, re.S)
    if m:
        mm=re.search(r'<revision>([^<]+)</revision>', m.group(0))
        if mm: v=mm.group(1).strip()
    if not v or '\$' in v:
        mm=re.search(r'</artifactId>\s*<version>([^<]+)</version>', xml)
        if mm: v=mm.group(1).strip()
print(v)
" 2>/dev/null
}
V_OLD=$(version_of "$B"); V_NEW=$(version_of "$W")
COMMITS=$(git log --no-merges --format='- %h %s（%an）' "$B..$W" 2>/dev/null | head -20)
COUNT=$(git rev-list --count --no-merges "$B..$W" 2>/dev/null || echo 0)
AHEAD=$(git rev-list --count "$B..$W" 2>/dev/null || echo 0)
BEHIND=$(git rev-list --count "$W..$B" 2>/dev/null || echo 0)
STAT=$(git diff --stat "$B...$W" 2>/dev/null | tail -1)
FILES=$(git diff --name-only "$B...$W" 2>/dev/null)
SQL_CHANGED=$(echo "$FILES" | grep -Ei '\.sql$|mapper.*\.xml$|entity/|/DO/|@TableField' | head -5)
NACOS_CHANGED=$(git diff "$B...$W" -- '*.yml' '*.yaml' '*.properties' 2>/dev/null | grep -E '^\+.*(@Value|@NacosValue|@ConfigurationProperties|[a-z-]+:)' | head -5)
XXL_CHANGED=$(git diff "$B...$W" 2>/dev/null | grep -cE '^\+.*@XxlJob' || true)
SNAP=$(git show "$W:pom.xml" 2>/dev/null | grep -c SNAPSHOT || true)
REMOTE=$(git remote get-url origin 2>/dev/null | sed -E 's#^https?://[^/]+/##; s#\.git$##')
MR_LIST=$(curl -s -m 20 -H "PRIVATE-TOKEN: ${AGENTFLOW_GIT_TOKEN:-}" \
  "http://gitlabnew.inter.wcansoft.com/api/v4/projects/$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$REMOTE")/merge_requests?source_branch=$(python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=''))" "$WORK")&state=opened" 2>/dev/null)
MR_LINK=$(echo "$MR_LIST" | python3 -c "import json,sys
try:
 d=json.load(sys.stdin); print(d[0]['web_url']+' (+!'+str(d[0]['iid'])+')' if d else '（待创建）')
except Exception: print('（待创建）')")
{
echo "# 上线内容清单｜$(git log -1 --format=%s "$W" | head -c 60)"
echo
echo "## 基本信息"
echo
echo "- **上线日期**：$(date +%F)（流程生成，实际以上线为准）"
echo "- **需求名称**：$(git log -1 --format=%s "$W" | head -c 120)"
echo "- **负责人**：以工作项责任人为准"
echo "- **生产分支**：\`origin/$BASE\`"
echo "- **任务类型**：$(echo "$WORK" | grep -qE '^feature' && echo 'feature（MINOR+1）' || echo 'hotfix（PATCH+1）')"
echo
echo "## 项目与分支"
echo
echo "| # | 项目 | 分支 | 生产分支 | 版本 | MR 链接 | 状态 |"
echo "|---|---|---|---|---|---|---|"
echo "| 1 | $REMOTE | $WORK | $BASE | ${V_NEW:-?} | $MR_LINK | 见 MR 状态 |"
echo
echo "- 相对 \`origin/$BASE\` 领先 **$AHEAD** 个 commit，落后 **$BEHIND** 个"
echo "- 提交记录："
echo "$COMMITS" | sed 's/^/  /'
echo
echo "## 版本与依赖发布记录"
echo
echo "| 项 | 内容 |"
echo "|---|---|"
echo "| 版本变更 | ${V_OLD:-?} → ${V_NEW:-?} |"
echo "| SNAPSHOT 残留 | $([ "${SNAP:-0}" -eq 0 ] && echo '无残留 ✅' || echo "发现 $SNAP 处 ⚠️") |"
echo "| 私服发布/下游依赖 | 需按项目实际情况确认 |"
echo
echo "## SQL 变更"
echo
if [ -z "$SQL_CHANGED" ]; then echo "- [x] 无 SQL/Mapper/实体变更（已扫描 diff 确认）"; else echo "- [ ] 存在疑似 SQL/实体变更，请确认："; echo "$SQL_CHANGED" | sed 's/^/  - /'; fi
echo
echo "## Nacos / 配置文件变更"
echo
if [ -z "$NACOS_CHANGED" ]; then echo "- [x] 未发现 yml/properties/配置注解变更"; else echo "- [ ] 存在配置变更，请确认："; echo "$NACOS_CHANGED" | sed 's/^/  - /'; fi
echo
echo "## 核心流程配置依赖（需人工确认）"
echo
echo "- [ ] 是否有必须与代码成对执行的配置变更（如渠道配置、开关、字典）？"
echo "- [ ] 若有，请写明配置项、环境、回滚方式（参考历史清单）"
echo
echo "## XXL-Job 配置变更"
echo
echo "$([ "${XXL_CHANGED:-0}" -eq 0 ] && echo '- [x] 无新增 @XxlJob' || echo "- [ ] 发现 $XXL_CHANGED 处 @XxlJob 变更，请确认")"
echo
echo "## 影响范围"
echo
echo "| 维度 | 内容 |"
echo "|---|---|"
echo "| 变更统计 | ${STAT:-无} |"
echo "| 涉及文件 | 见下（最多 15 个） |"
echo "| 接口/业务流程/数据/外部依赖 | 需人工确认（方案文档 solution.md 有说明） |"
echo
echo '```'
echo "$FILES" | head -15
echo '```'
echo
echo "## 回滚方案"
echo
echo "- **代码回滚**：对上线 MR 的合并提交执行 Revert，或回退到 ${V_OLD:-上一稳定版本} 重新出包部署"
echo "- **配置回滚**：如本次有配置变更，需同步回滚（务必成对）"
echo "- **数据修复**：无表结构变更时通常无需处理"
echo
echo "## 上线检查清单"
echo
echo "- [x] 版本号：${V_OLD:-?} → ${V_NEW:-?}"
echo "- [x] SNAPSHOT 检查：$([ "${SNAP:-0}" -eq 0 ] && echo 通过 || echo 待处理)"
echo "- [x] 合并基线：生成摘要时已 fetch \`origin/$BASE\`（落后 $BEHIND 个 commit）"
echo "- [ ] 编译/单测：见开发段 implement 节点结果"
echo "- [ ] 测试验收：见发布段测试验收审批"
echo "- [ ] SQL/Nacos/XXL/配置：见上文扫描结果，需人工确认"
echo
echo "## 上线注意"
echo
echo "1. 上线前确认基线已同步（落后 $BEHIND 个 commit，若落后较多请先合并基线）"
echo "2. 上线后做一次关键路径验证"
echo "3. 若存在配置变更，必须与代码成对执行"
echo "4. 失败时优先检查 MR 状态、分支权限与配置项"
} > "$OUT"
echo "已生成 $OUT"
