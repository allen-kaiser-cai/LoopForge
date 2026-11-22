#!/usr/bin/env python3
"""按禅道 bug 号下载附件/正文内嵌截图到指定目录（默认 .agentflow/evidence/zt-<id>）。"""
import argparse, json, os, re, sys, urllib.request

ZT = os.environ.get("ZT_BASE", "http://zentao.inter.wcansoft.com")
ACC = os.environ.get("ZT_ACCOUNT", "R_DAI")
PWD = os.environ.get("ZT_PASSWORD", "")

def token():
    body = json.dumps({"account": ACC, "password": PWD}).encode()
    req = urllib.request.Request(ZT + "/api.php/v1/tokens", data=body,
                                 headers={"Content-Type": "application/json"})
    return json.loads(urllib.request.urlopen(req, timeout=20).read())["token"]

def download(tok, fid, out):
    req = urllib.request.Request(f"{ZT}/api.php/v1/files/{fid}", headers={"token": tok})
    data = urllib.request.urlopen(req, timeout=30).read()
    ext = "png" if data[:4] == b"\x89PNG" else ("jpg" if data[:2] == b"\xff\xd8" else "bin")
    path = os.path.join(out, f"attachment-{fid}.{ext}")
    open(path, "wb").write(data)
    return path

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bug", required=True)
    ap.add_argument("--out")
    args = ap.parse_args()
    out = args.out or os.path.join(".agentflow", "evidence", f"zt-{args.bug}")
    os.makedirs(out, exist_ok=True)
    tok = token()
    req = urllib.request.Request(f"{ZT}/api.php/v1/bugs/{args.bug}", headers={"token": tok})
    bug = json.loads(urllib.request.urlopen(req, timeout=30).read())
    html = (bug.get("steps") or "") + (bug.get("title") or "")
    html = re.sub(r"<[^>]+>", " ", html)
    fids = sorted(set(re.findall(r"fileID=(\d+)", json.dumps(bug, ensure_ascii=False))))
    files = []
    for fid in fids:
        try: files.append(download(tok, fid, out))
        except Exception as e: print(f"下载 {fid} 失败: {e}", file=sys.stderr)
    meta = {"bug": args.bug, "title": bug.get("title"), "status": bug.get("status"),
            "openedBy": bug.get("openedBy"), "files": [os.path.basename(f) for f in files]}
    open(os.path.join(out, "bug.json"), "w", encoding="utf-8").write(
        json.dumps(meta, ensure_ascii=False, indent=2))
    print(f"已保存 {len(files)} 个附件到 {out}")
    for f in files: print(" -", f)

if __name__ == "__main__":
    main()
