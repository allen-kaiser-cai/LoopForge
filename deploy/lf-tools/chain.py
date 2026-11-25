#!/usr/bin/env python3
"""LoopForge 三段式自动衔接：澄清→开发→发布；发布失败→重派开发。"""
import json
import re, os, time, urllib.request, urllib.error

BASE = os.environ.get("LF_BASE", "http://127.0.0.1:8090")
TOKEN = os.environ.get("LF_TOKEN", "")
STATE = "/opt/lf-orchestrator/chain-state.json"
SUFFIX = os.environ.get("LF_CHAIN_SUFFIX", "")  # 空=生产版，"-weekend"=周末自测版

CLARIFY = "ssp-bug-clarify" + SUFFIX
DEV = "ssp-bug-dev-v2"
RELEASE = "ssp-bug-release-v2" + SUFFIX
# 发布段按 bug 类型分流：前端 bug 走前端审批人，后端 bug 走项目组长
RELEASE_FE = {
    "ssp": "ssp-bug-release-fe",
    "erp": "erp-bug-release-fe",
}
PROJECT_FLOWS = {
    "ssp": {"clarify": "ssp-bug-clarify", "dev": "ssp-bug-dev-v2",
            "release_test": "ssp-bug-release-test",
            "release": "ssp-bug-release-v2", "release_fe": "ssp-bug-release-fe"},
    "erp": {"clarify": "erp-bug-clarify", "dev": "erp-bug-dev-v2",
            "release_test": "erp-bug-release-test",
            "release": "erp-bug-release", "release_fe": "erp-bug-release-fe"},
    "platform": {"clarify": "platform-bug-clarify", "dev": "platform-bug-dev-v2",
            "release_test": "platform-bug-release-test",
                 "release": "platform-bug-release-v2", "release_fe": "platform-bug-release-fe"},
    "robot": {"clarify": "robot-bug-clarify", "dev": "robot-bug-dev-v2",
            "release_test": "robot-bug-release-test",
              "release": "robot-bug-release-v2", "release_fe": "robot-bug-release-fe"},
}

def flows_for(issue):
    pid = str(issue.get("projectId") or "ssp")
    return PROJECT_FLOWS.get(pid, PROJECT_FLOWS["ssp"])

FRONTEND_KEYWORDS = ("前端", "页面", "按钮", "样式", "显示", "白屏", "弹窗", "输入框", "小程序", "H5", "布局")

def is_frontend_issue(issue):
    text = " ".join([str(issue.get("title") or ""), str(issue.get("description") or "")])
    if "[前端bug]" in text or "改派:frontend" in text or "前端改派（自动）" in text:
        return True
    return any(k.lower() in text.lower() for k in FRONTEND_KEYWORDS)


PRODUCTION_MARKERS = ("线上bug", "线上BUG", "线上问题", "【线上", "「线上", "生产环境", "线上环境")

def delivery_scope(issue):
    """按禅道标识判断发布范围：线上问题=production，其余=test（仅测试环境）。"""
    text = (issue.get("title") or "") + " " + (issue.get("description") or "")
    if "[仅测试环境]" in text or "deliveryScope=test" in text:
        return "test"
    if "[生产上线]" in text or "deliveryScope=production" in text:
        return "production"
    for m in PRODUCTION_MARKERS:
        if m in text:
            return "production"
    return "test"

def release_workflow(issue):
    f = flows_for(issue)
    if delivery_scope(issue) == "test":
        return f.get("release_test") or f["release"]
    return f["release_fe"] if is_frontend_issue(issue) else f["release"]

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


def force_issue_open(issue_id, workflow=None):
    """审批完成或状态异常时，把工作项强制置为 open 并绑定目标流程，确保能派发下一段。"""
    st, res = api(f"/api/issues/{issue_id}")
    if st != 200:
        return False
    issue = (res.get("issue") or res)
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": issue.get("description"), "status": "open",
            "priority": issue.get("priority") or "high",
            "workflowId": workflow or issue.get("workflowId"),
            "assigneeUserId": issue.get("assigneeUserId") or 1,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    st2, _ = api("/api/issues", "PUT", body)
    return st2 == 200


def has_active_run(issue_id, workflow_id):
    """检查需求下是否已有该流程的运行，避免自动衔接与人工派发重复创建。"""
    st, res = api("/api/runs?limit=50")
    if st != 200:
        return False
    items = res.get("items", []) if isinstance(res, dict) else (res or [])
    for r in items:
        if (r.get("executionContext") or {}).get("issueId") != issue_id:
            continue
        if r.get("workflowId") != workflow_id:
            continue
        if r.get("status") in ("running", "waiting", "waiting_approval", "queued"):
            return True
    return False

def dispatch(issue_id, workflow, rerun=False):
    """首次派发不带 rerun；已跑过才带 rerun。自动兼容两种 400/409。"""
    attempts = []
    if rerun:
        attempts = [True, False]
    else:
        attempts = [False, True]
    last = None
    # 先把工作项置为 open 并绑定目标流程，避免审批完成后状态/流程绑定不一致导致 400/409
    force_issue_open(issue_id, workflow)
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
            force_issue_open(issue_id, workflow)
        else:
            break
    return last or (0, "")



DEV_RETRY_LIMIT = 3

def dev_failure_count(issue_id):
    st, res = api("/api/runs?limit=100")
    if st != 200:
        return 0
    items = res.get("items", []) if isinstance(res, dict) else (res or [])
    n = 0
    import datetime
    cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=2)
    for r in items:
        if (r.get("executionContext") or {}).get("issueId") != issue_id:
            continue
        if not r.get("workflowId", "").endswith("bug-dev-v2") or r.get("status") != "failed":
            continue
        ts = str(r.get("updatedAt") or "")
        try:
            dt = datetime.datetime.fromisoformat(ts.replace("Z", "+00:00"))
        except Exception:
            continue
        if dt >= cutoff:
            n += 1
    return n


FRONTEND_OWNERS = {"gaoyanfeng": 8, "liyongfeng": 9}

def frontend_owner_for(issue):
    text = ((issue.get("title") or "") + " " + (issue.get("description") or ""))
    h5_keys = ("h5", "小程序", "公众号", "移动端", "微信", "app", "手机端", "触屏")
    lowered = text.lower()
    if any(k in lowered for k in h5_keys):
        return "liyongfeng"
    return "gaoyanfeng"

def dev_reassign_target(run_id):
    """从开发段运行的节点产物/结果里识别改派目标。"""
    st, run = api(f"/api/runs/{run_id}")
    if st != 200:
        return ""
    for node in (run.get("nodes") or {}).values():
        if not isinstance(node, dict):
            continue
        blob = (str(node.get("output") or "") + " " + str(node.get("error") or "") + " " +
                str(node.get("feedback") or "") + " " + str(node.get("result") or ""))
        m = re.search(r"REASSIGN:user:([A-Za-z0-9_.-]+)", blob)
        if m:
            return "user:" + m.group(1)
        if "REASSIGN:frontend" in blob or "改派:frontend" in blob:
            return "frontend"
    return ""

def reassign_frontend(issue_id, run_id):
    """设计阶段判定为前端问题：改责任人、打前端标记、重派开发段。"""
    st, res = api(f"/api/issues/{issue_id}")
    if st != 200:
        return 0, "issue fetch failed"
    issue = (res.get("issue") or res)
    owner = frontend_owner_for(issue)
    user_id = FRONTEND_OWNERS.get(owner, 8)
    desc = issue.get("description") or ""
    note = "\n\n## 前端改派（自动）\n\n- 判定：设计阶段确认根因在前端\n- 来源运行：" + run_id + "\n- 改派给：" + owner + "\n"
    if "[前端bug]" not in desc:
        desc = "[前端bug]\n" + desc
    if "前端改派（自动）" not in desc:
        desc += note
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": desc, "status": "open", "priority": issue.get("priority") or "high",
            "workflowId": flows_for(issue)["dev"], "assigneeUserId": user_id,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    st2, _ = api("/api/issues", "PUT", body)
    if st2 != 200:
        return st2, "issue update failed"
    return dispatch(issue_id, flows_for(issue)["dev"], rerun=True)


def user_id_by_login(login):
    st, res = api("/api/users")
    if st != 200:
        return 0
    items = res.get("items", []) if isinstance(res, dict) else (res or [])
    for u in items:
        if (u.get("loginName") or "").strip().lower() == login.strip().lower():
            return int(u.get("id") or 0)
    return 0

def reassign_to_user(issue_id, run_id, login):
    """设计阶段指定改派账号：改责任人并重派开发段。"""
    user_id = user_id_by_login(login)
    if not user_id:
        return 0, "user not found"
    st, res = api(f"/api/issues/{issue_id}")
    if st != 200:
        return 0, "issue fetch failed"
    issue = (res.get("issue") or res)
    desc = issue.get("description") or ""
    note = "\n\n## 人工改派（自动）\n\n- 改派账号：" + login + "\n- 来源运行：" + run_id + "\n"
    if "人工改派（自动）" not in desc:
        desc += note
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": desc, "status": "open", "priority": issue.get("priority") or "high",
            "workflowId": flows_for(issue)["dev"], "assigneeUserId": user_id,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    st2, _ = api("/api/issues", "PUT", body)
    if st2 != 200:
        return st2, "issue update failed"
    return dispatch(issue_id, flows_for(issue)["dev"], rerun=True)


def _close_issue_after_test(issue_id):
    st, res = api(f"/api/issues/{issue_id}")
    if st != 200:
        return
    issue = (res.get("issue") or res)
    desc = issue.get("description") or ""
    note = "\n\n## 测试环境验证完成（自动）\n\n- 仅测试环境缺陷，测试验收通过后流程结束；\n- 不提交上线 MR。\n"
    if "测试环境验证完成（自动）" not in desc:
        desc += note
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": desc, "status": "closed", "priority": issue.get("priority") or "high",
            "workflowId": issue.get("workflowId"),
            "assigneeUserId": issue.get("assigneeUserId") or 1,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    api("/api/issues", "PUT", body)

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



def ding_alert(text, logins=("caiyihua",)):
    """向指定责任人发钉钉告警（复用 lf-notify 的应用凭据）。"""
    try:
        import urllib.parse
        env = {}
        for line in open("/opt/lf-notify/env"):
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                env[k] = v
        appkey, secret = env.get("DING_APPKEY", ""), env.get("DING_APPSECRET", "")
        if not (appkey and secret):
            print("ding_alert skipped: no appkey"); return
        users = json.load(open("/opt/lf-notify/ding-users.json"))
        ids = ",".join(users[l] for l in logins if users.get(l))
        if not ids:
            print("ding_alert skipped: no ding user"); return
        tok = json.loads(urllib.request.urlopen(urllib.request.Request(
            f"https://oapi.dingtalk.com/gettoken?appkey={appkey}&appsecret={secret}")).read())["access_token"]
        body = json.dumps({"agent_id": appkey,
                           "msg": {"msgtype": "markdown",
                                   "markdown": {"title": "LoopForge 流程需要人工处理", "text": text}},
                           "to_all_user": False, "userid_list": ids}).encode()
        urllib.request.urlopen(urllib.request.Request(
            f"https://oapi.dingtalk.com/topapi/message/corpconversation/asyncsend_v2?access_token={tok}",
            data=body, headers={"Content-Type": "application/json"}))
        print("ding_alert sent:", logins)
    except Exception as e:
        print("ding_alert error:", e)

def failed_node_of(run_id):
    st, run = api(f"/api/runs/{run_id}")
    if st != 200:
        return "", "", ""
    for nid, node in (run.get("nodes") or {}).items():
        if isinstance(node, dict) and node.get("status") == "failed":
            return nid, (node.get("error") or "")[:800], (node.get("output") or "")[:400]
    return "", "", ""

def _set_issue_workflow(issue_id, issue, workflow_id):
    body = {"id": issue_id, "projectId": issue.get("projectId"), "title": issue.get("title"),
            "description": issue.get("description"), "status": "open",
            "priority": issue.get("priority") or "high", "workflowId": workflow_id,
            "assigneeUserId": issue.get("assigneeUserId") or 1,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    api("/api/issues", "PUT", body)

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
            "workflowId": flows_for(issue)["clarify"], "assigneeUserId": issue.get("assigneeUserId") or 1,
            "branchLine": issue.get("branchLine") or "standard",
            "repositoryIds": issue.get("repositoryIds") or []}
    api("/api/issues", "PUT", body)
    return dispatch(issue_id, flows_for(issue)["clarify"])

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
        flows = flows_for(issue)
        handled = True
        if status == "succeeded" and wf == flows["clarify"]:
            inject_evidence_node(issue_id, issue.get("title"))
            apply_candidate_repos(issue_id, issue)
            if has_active_run(issue_id, flows["dev"]):
                print("chain clarify->dev skipped (active dev run exists)", issue_id)
                st3 = 201
            else:
                st3, _ = dispatch(issue_id, flows["dev"])
            handled = st3 == 201
            print("chain clarify->dev", issue_id, flows["dev"], st3, "" if handled else "FAILED")
        elif status == "succeeded" and wf == flows.get("release_test") and delivery_scope(issue) == "test":
            _close_issue_after_test(issue_id)
            handled = True
            print("chain release(test) done, issue closed", issue_id)
        elif status == "succeeded" and wf == flows["dev"]:
            rel = release_workflow(issue)
            _set_issue_workflow(issue_id, issue, rel)
            print("release workflow:", rel, "frontend=", is_frontend_issue(issue))
            st3, _ = dispatch(issue_id, rel)
            handled = st3 == 201
            print("chain dev->release", issue_id, st3, "" if handled else "FAILED")
        elif status == "failed" and wf == flows["dev"]:
            target = dev_reassign_target(rid)
            if target.startswith("user:"):
                st3, _ = reassign_to_user(issue_id, rid, target.split(":", 1)[1])
                handled = st3 == 201
                print("chain dev->user reassign", issue_id, target, st3)
            elif target == "frontend":
                st3, _ = reassign_frontend(issue_id, rid)
                handled = st3 == 201
                print("chain dev->frontend reassign", issue_id, st3)
            elif is_rework_failure(rid):
                st3, _ = rework_to_clarify(issue_id, rid)
                handled = st3 == 201
                print("chain dev->clarify (rework)", issue_id, st3)
            else:
                fails = dev_failure_count(issue_id)
                if fails >= DEV_RETRY_LIMIT:
                    ding_alert("### 开发段连续失败，已停止自动重试\n\n工作项：" + issue_id +
                               "\n\n失败次数：" + str(fails) +
                               "\n\n请检查执行机凭据/环境后人工重派开发段。", logins=("caiyihua",))
                    handled = True
                    print("chain dev retry limit reached", issue_id, fails)
                else:
                    st3, _ = dispatch(issue_id, flows["dev"])
                    handled = st3 == 201
                    print("chain dev retry (non-rework failure)", issue_id, st3)
        elif status == "failed" and wf in (flows["release"], flows["release_fe"]):
            node_id, node_err, _ = failed_node_of(rid)
            platform_issue = ("artifact contract" in node_err.lower()) or ("file does not exist" in node_err.lower())
            if node_id in ("mr_test", "deliver", "sonar") or platform_issue:
                if platform_issue and node_id not in ("mr_test", "deliver", "sonar"):
                    node_id = node_id + "(产物/脚本异常)"
                # 合并/MR/扫描失败属于环境或代码冲突，交给人工处理，不重跑开发
                text = (f"### LoopForge 流程需要人工处理\n\n工作项：{issue_id}\n\n运行：{rid}\n\n"
                        f"失败节点：{node_id}\n\n错误：{node_err[:500]}\n\n"
                        f"处理建议：请人工检查测试分支合并冲突/MR 状态，解决后在平台重试发布段。")
                ding_alert(text, logins=("caiyihua",))
                print("chain alert human (release node)", issue_id, node_id)
            else:
                st3, _ = dispatch(issue_id, flows["dev"])
                print("chain release->dev (rework)", issue_id, node_id, st3)
        if handled:
            state[key] = int(time.time())
            state.pop("retry:" + key, None)
        else:
            n = state.get("retry:" + key, 0) + 1
            state["retry:" + key] = n
            if n >= 5:
                state[key] = int(time.time())
            print("chain retry scheduled", key, "attempt", n)
        save_state(state)

if __name__ == "__main__":
    try: main()
    except Exception as e:
        print("chain error:", e)
