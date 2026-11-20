package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DefaultWorkBranch names the git work branch for an issue run. Gongfeng issues
// use fix-issue-{iid}; TAPD stories use story-{tapdId}; platform requirements use
// requirement-{number}. The suffix is a local timestamp that keeps branch names
// readable while separating dispatches started in different minutes.
func DefaultWorkBranch(issue Issue, now time.Time) string {
	suffix := now.In(time.Local).Format("20060102-1504")
	source := strings.ToLower(strings.TrimSpace(issue.Source))
	kind := strings.ToLower(strings.TrimSpace(issue.Kind))

	if source == "tapd" || strings.HasPrefix(strings.TrimSpace(issue.ID), "tapd-") {
		if id := tapdStoryID(issue); id != "" {
			return "feature/story-" + workBranchSegment(id) + "-" + suffix
		}
	}
	if source == "gongfeng" || kind == "issue" {
		if n := gongfengIssueNumber(issue); n > 0 {
			return fmt.Sprintf("feature/fix-issue-%d-%s", n, suffix)
		}
	}
	if kind == "requirement" || source == "" || source == "local" {
		if issue.Number > 0 {
			return fmt.Sprintf("feature/requirement-%d-%s", issue.Number, suffix)
		}
	}
	if issue.Number > 0 {
		return fmt.Sprintf("feature/fix-issue-%d-%s", issue.Number, suffix)
	}
	if issue.ID != "" {
		return "feature/" + workBranchSegment(issue.ID) + "-" + suffix
	}
	return "feature/fix-" + suffix
}

func gongfengIssueNumber(issue Issue) int {
	if issue.ExternalRef != nil && issue.ExternalRef.ExternalIID > 0 {
		return issue.ExternalRef.ExternalIID
	}
	return issue.Number
}

func tapdStoryID(issue Issue) string {
	if issue.ExternalRef != nil {
		if id := strings.TrimSpace(issue.ExternalRef.ExternalID); id != "" {
			return id
		}
	}
	return strings.TrimPrefix(strings.TrimSpace(issue.ID), "tapd-")
}

func workBranchSegment(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	result := strings.Trim(b.String(), "-.")
	if result == "" {
		return "run"
	}
	return result
}

// SpecWorkBranch 按《SSP 分支命名规范》生成工作分支：
// {类型前缀}-{YYYYMMDD}-{姓名拼音}-{中文描述}
// 类型仅四种：feature / hotfix / refactoring / optimization；bug 一律 hotfix。
func SpecWorkBranch(issue Issue, author string, now time.Time) string {
	prefix := "feature"
	kind := strings.ToLower(strings.TrimSpace(issue.Kind))
	title := strings.TrimSpace(issue.Title)
	if kind == "bug" || strings.Contains(title, "线上bug") || strings.Contains(title, "【bug】") {
		prefix = "hotfix"
	}
	day := now.In(time.FixedZone("CST", 8*3600)).Format("20060102")
	name := strings.ToLower(strings.TrimSpace(author))
	name = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(name, "")
	if name == "" {
		name = "unknown"
	}
	desc := specBranchDescription(title)
	if desc == "" {
		desc = "未命名改动"
	}
	return prefix + "-" + day + "-" + name + "-" + desc
}

// specBranchDescription 从需求/缺陷标题提炼分支描述：去掉禅道前缀和标点，最多 30 个字符。
func specBranchDescription(title string) string {
	t := strings.TrimSpace(title)
	// 去掉 [禅道#123] / 「线上bug」 / 【bug】 等前缀标记
	t = regexp.MustCompile(`^[\[【(（][^\]】)）]{0,30}[\]】)）]\s*`).ReplaceAllString(t, "")
	t = regexp.MustCompile(`[「」【】\[\]()（）<>《》]`).ReplaceAllString(t, "")
	// 只保留中文、字母、数字；其他字符视为分隔
	t = regexp.MustCompile(`[^\p{Han}A-Za-z0-9]+`).ReplaceAllString(t, "")
	runes := []rune(t)
	if len(runes) > 30 {
		runes = runes[:30]
	}
	return string(runes)
}
