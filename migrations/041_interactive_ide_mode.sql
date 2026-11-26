-- IDE 交互模式：账号交互方式 + IDE 交互任务表（幂等，可重复执行）
SET @has_mode := (
  SELECT COUNT(*) FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'user_configs' AND column_name = 'interactive_mode'
);
SET @ddl_mode := IF(@has_mode = 0,
  'ALTER TABLE user_configs ADD COLUMN interactive_mode VARCHAR(16) NOT NULL DEFAULT "ide"',
  'DO 0');
PREPARE stmt_mode FROM @ddl_mode; EXECUTE stmt_mode; DEALLOCATE PREPARE stmt_mode;

CREATE TABLE IF NOT EXISTS ide_interactive_tasks (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  run_id VARCHAR(64) NOT NULL,
  node_id VARCHAR(64) NOT NULL,
  project_id VARCHAR(64) NOT NULL DEFAULT '',
  issue_id VARCHAR(64) NOT NULL DEFAULT '',
  user_id BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(24) NOT NULL DEFAULT 'waiting',
  decision VARCHAR(16) NOT NULL DEFAULT '',
  note TEXT NULL,
  artifacts_json JSON NULL,
  branch_name VARCHAR(255) NOT NULL DEFAULT '',
  confirm_token VARCHAR(64) NOT NULL DEFAULT '',
  remind_at DATETIME(3) NULL,
  fail_at DATETIME(3) NULL,
  submitted_at DATETIME(3) NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  UNIQUE KEY uk_ide_run_node (run_id, node_id),
  KEY idx_ide_status (status),
  KEY idx_ide_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

SET @has_token := (
  SELECT COUNT(*) FROM information_schema.columns
  WHERE table_schema = DATABASE() AND table_name = 'ide_interactive_tasks' AND column_name = 'confirm_token'
);
SET @ddl_token := IF(@has_token = 0,
  'ALTER TABLE ide_interactive_tasks ADD COLUMN confirm_token VARCHAR(64) NOT NULL DEFAULT ""',
  'DO 0');
PREPARE stmt_token FROM @ddl_token; EXECUTE stmt_token; DEALLOCATE PREPARE stmt_token;
