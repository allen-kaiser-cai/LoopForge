ALTER TABLE user_configs ADD COLUMN interactive_mode VARCHAR(16) NOT NULL DEFAULT 'ide';

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
  remind_at DATETIME(3) NULL,
  fail_at DATETIME(3) NULL,
  submitted_at DATETIME(3) NULL,
  created_at DATETIME(3) NULL,
  updated_at DATETIME(3) NULL,
  UNIQUE KEY uk_ide_run_node (run_id, node_id),
  KEY idx_ide_status (status),
  KEY idx_ide_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
