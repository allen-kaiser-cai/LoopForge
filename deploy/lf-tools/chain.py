#!/usr/bin/env python3
"""LoopForge 三段式自动衔接：澄清→开发→发布；发布失败→重派开发。"""
import json, os, time, urllib.request, urllib.error

BASE = os.environ.get("LF_BASE", "http://127.0.0.1:8090")
TOKEN = os.environ.get("LF_TOKEN", "")
STATE = "/opt/lf-orchestrator/chain-state.json"
SUFFIX = os.environ.get("LF_CHAIN_SUFFIX", "")  # 空=生产版，"-weekend"=周末自测版

CLARIFY = "ssp-bug-clarify" + SUFFIX
DEV = "ssp-bug-dev-v2"
RELEASE = "ssp-bug-release-v2" + SUFFIX

def api(path, method="GET", body=None):
    req = urllib.request.Request(BASE + path, method=method,
        headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"},
        data=json.dumps(body).encode() if body is not None else None)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read().decode() or "{}")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:200]

def load_state():
    try: return json.load(open(STATE))
    except Exception: return {}

def save_state(s):
    json.dump(s, open(STATE, "w"))


def inject_evidence_node(issue_id, issue_title):
    """派发开发段前，把 bug 号写进 fetch-evidence 脚本，保证证据落到执行机工作区。"""
    import re
    m = re.search(r"禅道#(\d+)", issue_title or "")
    if not m:
        return
    bug = m.group(1)
    st, res = api("/api/workflows/" + DEV)
    if st != 200:
        return
    wf = json.loads(res) if isinstance(res, str) else res
    script = ('UHOME=$(eval echo ~$(id -un)); '
              'set -a; . "$UHOME/.lf-tools/zt.env"; set +a; '
              f'python3 "$UHOME/.lf-tools/zt_evidence.py" --bug {bug} --out .agentflow/inputs/evidence/{bug}')
    changed = False
    for n in wf.get("nodes") or []:
        if n.get("id") == "fetch-evidence" and n.get("script") != script:
            n["script"] = script
            changed = True
    if not changed:
        return
    for k in ["createdAt", "updatedAt", "owner", "tags", "publishedRevision"]:
        wf.pop(k, None)
    api("/api/workflows", "PUT", wf)
    st2, res2 = api("/api/workflows/" + DEV)
    if st2 == 200:
        rev = (json.loads(res2) if isinstance(res2, str) else res2).get("revision")
        if rev:
            api(f"/api/workflows/{DEV}/publish", "POST", {"revision": rev})
    print("evidence injected for bug", bug)


def apply_candidate_repos(issue_id, issue):
    """按 bug 内容选择候选仓库，避免所有 bug 都挂同一个默认仓库。"""
    try:
        import lf_orchestrator as orch
        text = " ".join([str(issue.get("title") or ""), str(issue.get("description") or "")])
        names = orch.select_candidate_repos(text)
        if not names:
            return
        st, rows = api("/api/repositories?projectId=" + str(issue.get("projectId") or "ssp"))
        if st != 200:
            return
        items = rows.get("items") if isinstance(rows, dict) else rows
        by_name = {r.get("name"): r.get("id") for r in (items or [])}
        ids = [by_name[n] for n in names if by_name.get(n)]
        if not ids:
            return
        body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
                "description": issue.get("description"), "status": "open",
                "priority": issue.get("priority") or "high", "workflowId": DEV,
                "assigneeUserId": issue.get("assigneeUserId") or 1,
                "branchLine": issue.get("branchLine") or "standard", "repositoryIds": ids}
        st2, _ = api("/api/issues", "PUT", body)
        print("repos applied", names, st2)
    except Exception as e:
        print("repo select skipped:", e)

def dispatch(issue_id, workflow, rerun=False):
    """首次派发不带 rerun；已跑过才带 rerun。自动兼容两种 400/409。"""
    attempts = []
    if rerun:
        attempts = [True, False]
    else:
        attempts = [False, True]
    last = None
    for flag in attempts:
        body = {"workflowId": workflow}
        if flag: body["rerun"] = True
        st, res = api(f"/api/issues/{issue_id}/dispatch", "POST", body)
        if st == 201:
            return st, res
        last = (st, res)
        if st in (400, 409):
            try: api(f"/api/issues/{issue_id}/reopen", "POST", {})
            except Exception: pass
        else:
            break
    return last or (0, "")



def is_rework_failure(run_id):
    st, run = api(f"/api/runs/{run_id}")
    if st != 200:
        return False
    for node in (run.get("nodes") or {}).values():
        if not isinstance(node, dict):
            continue
        msg = ((node.get("error") or "") + " " + (node.get("feedback") or "")).lower()
        if "rework" in msg or "打回" in msg:
            return True
    return False

def rework_to_clarify(issue_id, run_id):
    """开发段被打回（设计发现需求不清）→ 把返工原因写回工作项并重跑澄清段。"""
    st, run = api(f"/api/runs/{run_id}")
    if st != 200:
        return 0, "run fetch failed"
    notes = []
    for nid, node in (run.get("nodes") or {}).items():
        if not isinstance(node, dict):
            continue
        msg = (node.get("feedback") or "") + " " + (node.get("error") or "")
        if any(k in msg for k in ("打回", "返工", "待确认", "rework")):
            notes.append(f"- {nid}: {msg.strip()[:1200]}")
    st, res = api(f"/api/issues/{issue_id}")
    issue = (res.get("issue") or res) if st == 200 else {}
    desc = issue.get("description") or ""
    if notes:
        block = "\n\n## 开发段打回：需澄清的问题\n\n" + "\n".join(notes) + "\n"
        if "开发段打回：需澄清的问题" not in desc:
            desc += block
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": desc, "status": "open", "priority": issue.get("priority") or "high",
            "workflowId": CLARIFY, "assigneeUserId": issue.get("assigneeUserId") or 1,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    api("/api/issues", "PUT", body)
    return dispatch(issue_id, CLARIFY)

def main():
    state = load_state()
    st, res = api("/api/runs?limit=100")
    runs = res.get("items", []) if isinstance(res, dict) else res
    for r in runs:
        rid = r.get("id"); wf = r.get("workflowId"); status = r.get("status")
        if status not in ("succeeded", "failed"): continue
        key = f"{rid}:{status}"
        if state.get(key): continue
        issue_id = (r.get("executionContext") or {}).get("issueId") or ""
        if not issue_id: continue
        st2, ires = api(f"/api/issues/{issue_id}")
        issue = (ires.get("issue") or ires) if st2 == 200 else {}
        cur_wf = issue.get("workflowId")
        if status == "succeeded" and wf == CLARIFY and cur_wf == CLARIFY:
            inject_evidence_node(issue_id, issue.get("title"))
            apply_candidate_repos(issue_id, issue)
            st3, _ = dispatch(issue_id, DEV)
            print("chain clarify->dev", issue_id, st3)
        elif status == "succeeded" and wf == DEV and cur_wf == DEV:
            st3, _ = dispatch(issue_id, RELEASE)
            print("chain dev->release", issue_id, st3)
        elif status == "failed" and wf == DEV and cur_wf == DEV:
            if is_rework_failure(rid):
                st3, _ = rework_to_clarify(issue_id, rid)
                print("chain dev->clarify (rework)", issue_id, st3)
            else:
                st3, _ = dispatch(issue_id, DEV)
                print("chain dev retry (non-rework failure)", issue_id, st3)
        elif status == "failed" and wf == RELEASE and cur_wf == RELEASE:
            st3, _ = dispatch(issue_id, DEV)
            print("chain release->dev (rework)", issue_id, st3)
        state[key] = int(time.time())
        save_state(state)

if __name__ == "__main__":
    try: main()
    except Exception as e:
        print("chain error:", e)
