# 云池宿主机脚本（LoopForge 缺陷流程）

| 脚本 | 用途 |
| --- | --- |
| `ocr_review.sh` | 对开发分支相对基线的 diff 跑 Open Code Review，产出 `ocr-report.md`；严重问题退出码 2 触发打回 |
| `sonar_check.sh` | 拉 SonarQube 项目问题数，与上一次分析对比，增加则钉钉告警，产出 `sonar-report.md` |
| `zt_evidence.py` | 按禅道 bug 号下载附件/截图到 `.agentflow/inputs/evidence/<bug>/` |
| `chain.py` | 三段式自动衔接：澄清→开发→发布；开发打回→澄清；网络类失败→重试开发 |

部署位置：云池宿主机 `/opt/lf-tools/`，衔接服务 `/opt/lf-orchestrator/chain.py`（systemd timer 每分钟）。
