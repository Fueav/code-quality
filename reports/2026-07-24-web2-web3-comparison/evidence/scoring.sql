DROP TABLE IF EXISTS run_scores;
DROP TABLE IF EXISTS durations;

CREATE TABLE run_scores (
  case_order INTEGER NOT NULL,
  case_label TEXT NOT NULL,
  case_name TEXT NOT NULL,
  domain TEXT NOT NULL,
  lane TEXT NOT NULL,
  lane_order INTEGER NOT NULL,
  core_hit INTEGER NOT NULL,
  quality_score REAL NOT NULL,
  finding_count INTEGER NOT NULL,
  false_positive_count INTEGER NOT NULL,
  actionable INTEGER NOT NULL,
  difference TEXT NOT NULL
);

INSERT INTO run_scores VALUES
  (1, 'W2 列表分页', 'Runner 列表分页稳定性', 'Web2', 'Codex 内置', 1, 1, 8, 1, 0, 1, '结论等价；code-quality 固定输出检查范围与规则 ID'),
  (1, 'W2 列表分页', 'Runner 列表分页稳定性', 'Web2', 'code-quality', 2, 1, 8, 1, 0, 1, '结论等价；code-quality 固定输出检查范围与规则 ID'),
  (2, 'W2 过滤计数', 'Commit 日期过滤计数', 'Web2', 'Codex 内置', 1, 1, 7, 1, 0, 1, 'code-quality 明确写出恢复 --since/--until 的最小修复'),
  (2, 'W2 过滤计数', 'Commit 日期过滤计数', 'Web2', 'code-quality', 2, 1, 8, 1, 0, 1, 'code-quality 明确写出恢复 --since/--until 的最小修复'),
  (3, 'W2 release 统计', 'Release 同步统计串位', 'Web2', 'Codex 内置', 1, 1, 7, 1, 0, 1, 'code-quality 明确给出 inserts/deletes/updates 对应关系'),
  (3, 'W2 release 统计', 'Release 同步统计串位', 'Web2', 'code-quality', 2, 1, 8, 1, 0, 1, 'code-quality 明确给出 inserts/deletes/updates 对应关系'),
  (4, 'W3 日志扫块范围', 'eth_getLogs 扫块区间', 'Web3', 'Codex 内置', 1, 1, 8, 1, 0, 1, '结论与修复等价'),
  (4, 'W3 日志扫块范围', 'eth_getLogs 扫块区间', 'Web3', 'code-quality', 2, 1, 8, 1, 0, 1, '结论与修复等价'),
  (5, 'W3 chain reorg', 'Chain indexer reorg 恢复', 'Web3', 'Codex 内置', 1, 1, 8, 3, 0, 1, '内置额外命中 section 边界 off-by-one；code-quality 漏掉'),
  (5, 'W3 chain reorg', 'Chain indexer reorg 恢复', 'Web3', 'code-quality', 2, 1, 8, 1, 0, 1, '内置额外命中 section 边界 off-by-one；code-quality 漏掉'),
  (6, 'W3 PreBlock 索引', 'PreBlock 事件索引', 'Web3', 'Codex 内置', 1, 1, 8, 1, 0, 1, '结论与修复等价'),
  (6, 'W3 PreBlock 索引', 'PreBlock 事件索引', 'Web3', 'code-quality', 2, 1, 8, 1, 0, 1, '结论与修复等价');

CREATE TABLE durations (
  lane TEXT NOT NULL,
  seconds REAL NOT NULL
);

INSERT INTO durations VALUES
  ('Codex 内置', 36), ('Codex 内置', 38), ('Codex 内置', 19),
  ('Codex 内置', 33), ('Codex 内置', 88), ('Codex 内置', 36),
  ('code-quality', 83), ('code-quality', 81), ('code-quality', 96),
  ('code-quality', 79), ('code-quality', 152), ('code-quality', 90);
