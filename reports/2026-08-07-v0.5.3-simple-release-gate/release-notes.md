## v0.5.3

- 用 `PASS / BLOCK / ERROR` 一眼说明是否有问题、是否可以继续发布流程。
- 有效 finding 直接返回 `BLOCK` 并使 Jenkins/GitHub CI 失败；不可信扫描返回 `ERROR`，默认阻止发布。
- Codex 与 Claude Code 使用原生 JSON Schema 结构化输出。
- CI 主产物只保留简明 Markdown/JSON，完整原始证据统一归档为 `evidence.tar.gz`。
- Jenkins 生产配置继续使用已登录的本机 Provider，PR 级扫描固定 `reasoning_effort=high`，不需要 API Key。
