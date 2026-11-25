#!/bin/bash
# 判断仓库相对基线是否有变更（含分支是否存在）
# 用法：repo_changed <repo> <base> <work>；返回 0=有变更，1=无变更/分支不存在
repo_changed() {
  local repo="$1" base="$2" work="$3"
  (
    cd "$repo" 2>/dev/null || exit 1
    git fetch -q origin "$base" "$work" >/dev/null 2>&1
    git rev-parse --verify "origin/$work" >/dev/null 2>&1 || exit 1
    git diff --quiet "origin/$base...origin/$work" 2>/dev/null && exit 1 || exit 0
  )
}
