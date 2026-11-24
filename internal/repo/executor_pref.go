package repo

// PreferredExecutorID 返回用户配置的惯用智能体执行器 ID（空表示未配置）。
func (r *Store) PreferredExecutorID(userID int64) string {
	if userID <= 0 {
		return ""
	}
	var value string
	q := r.DB.Raw(`SELECT COALESCE(preferred_executor_id, '') FROM user_configs WHERE user_id = ?`, userID)
	if err := q.Scan(&value).Error; err != nil {
		return ""
	}
	return value
}
