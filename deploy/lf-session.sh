#!/bin/bash
# LoopForge 终端接管辅助命令（随 Runner 安装脚本分发）
set -u
usage() { cat <<'U'
用法：
  lf-session sessions                     # 列出本机交互会话
  lf-session attach <runId> <nodeId>      # 进入交互会话
  lf-session submit [--decision pass|rework] [--note "备注"]
U
}
find_ws() {
  d="$PWD"
  while [ "$d" != "/" ]; do
    [ -d "$d/.agentflow/interactive" ] && { echo "$d"; return; }
    [ -d "$d/workspace/.agentflow/interactive" ] && { echo "$d/workspace"; return; }
    d=$(dirname "$d")
  done
  for sub in "$PWD/workspace" "$PWD"/*/workspace; do
    [ -d "$sub/.agentflow/interactive" ] && { echo "$sub"; return; }
  done
}
case "${1:-}" in
  attach) exec tmux attach -t "lf-$2-$3" ;;
  sessions) tmux ls 2>/dev/null | grep '^lf-' || echo "没有正在进行的交互会话" ;;
  submit)
    if [ ! -t 0 ]; then
      echo "提交必须在交互式终端中进行（Agent 没有 TTY，不允许代提交）" >&2
      exit 1
    fi
    shift; dec=""; note=""
    while [ $# -gt 0 ]; do case "$1" in --decision) dec="${2:-}"; shift 2;; --note) note="${2:-}"; shift 2;; *) shift;; esac; done
    ws=$(find_ws)
    if [ -z "$ws" ]; then echo "未找到交互工作区（.agentflow/interactive），请 cd 到 ~/.agentflow-runner/workspace/runs/<runId>/workspace 后重试" >&2; exit 1; fi
    if [ -z "$dec" ]; then
      echo "提交结果：1) pass 通过  2) rework 返工"
      read -r -p "输入 1/2: " c
      if [ "$c" = "1" ]; then dec=pass; else dec=rework; fi
      read -r -p "备注（可空）: " note
    fi
    python3 - "$ws" "$dec" "$note" <<'PY'
import json,os,sys
ws,dec,note=sys.argv[1],sys.argv[2],sys.argv[3]
path=os.path.join(ws,'.agentflow','interactive','submit.json')
os.makedirs(os.path.dirname(path),exist_ok=True)
json.dump({"decision":dec,"note":note},open(path,'w',encoding='utf-8'),ensure_ascii=False)
print(f"已提交：{path}  decision={dec}")
PY
    ;;
  *) usage ;;
esac
