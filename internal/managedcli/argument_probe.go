package managedcli

import "context"

// validateClaudeHiddenFileArgument 校验 Claude Code CLI 是否接受
// --system-prompt-file。
//
// 历史实现通过“未知选项 + 缺失文件”两个探测命令判断（见上游 0.3.8）；
// 本机 Claude Code 2.1.x 把这两类报错都写到 stderr，而探测只读取 stdout，
// 导致误判为“不支持该参数”，任务在 1 秒内以 executor exited without
// terminal result 失败。该参数自 2.0 起稳定存在，因此这里直接放行：
// 若 CLI 真的不支持，执行阶段会给出明确报错，不会被静默吞掉。
func (m *Manager) validateClaudeHiddenFileArgument(ctx context.Context, installed Installation) error {
	_ = ctx
	_ = installed
	return nil
}
