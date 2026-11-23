# 云池宿主机脚本（LoopForge 缺陷修复流程）

| 脚本 | 用途 |
| --- | --- |
| `ocr_review.sh` | 开发分支 vs 基线 diff 的 Open Code Review，产出 ocr-report.md；严重问题退出码 2 触发打回 |
| `sonar_check.sh` | 拉 SonarQube 问题数与上次分析对比，增加则钉钉告警，产出 sonar-report.md |
| `compile_check.sh` | 合并前编译检查（含测试代码编译），失败阻断合并，产出 compile-report.md |
| `startup_check.sh` | 合并前用 daily profile 启动服务，确认能正常启动，产出 startup-report.md |
| `release_summary.sh` | 生成《上线内容清单》：版本变化、升级内容、SQL/配置扫描、影响范围、回滚建议 |
| `create_mr.sh` | 上线 MR：同步基线、去重、无差异跳过、失败钉钉告警不终止流程 |
| `zt_evidence.py` | 按禅道 bug 号下载附件/截图到 `.agentflow/inputs/evidence/<bug>/` |
| `chain.py` | 三段式自动衔接：澄清→开发→发布；开发打回→澄清；合并类失败→人工告警；按项目/前后端分流 |

部署位置：脚本 `/opt/lf-tools/`，衔接服务 `/opt/lf-orchestrator/chain.py`（systemd timer，每分钟）。
