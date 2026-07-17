# 为 LXP 贡献

[English](CONTRIBUTING.en.md) | **中文**

感谢帮助改进 Loop Exchange Protocol。LXP 仍处于 public alpha；接口与 Schema 可能变化，生产采用前应谨慎评估。

## 开始之前

- 阅读 `README.md`、`docs/spec-v1alpha1.md` 与 `AGENTS.md`。
- 创建 Issue 或 Proposal 前先搜索已有讨论。
- 较大的协议、Schema、安全或兼容性修改应先创建 Issue，讨论设计与迁移影响。
- 疑似漏洞应使用 `SECURITY.md` 中的私下报告流程。

规范与 Schema 定义可移植行为；`.lxp/sessions` 下的 Engine-local 文件只是实现状态，不能成为协议要求来源。

## 开发流程

1. Fork 仓库并创建聚焦的分支。
2. 提交最小且完整的修改；行为变化应同时包含测试和文档。
3. 保持 `AGENTS.md` 中的协议不变量。
4. 运行：

   ```bash
   go test ./...
   go vet ./...
   scripts/verify-local.sh
   ```

5. 使用清晰的 Conventional Commit，例如 `fix: reject unsafe archive paths`。
6. PR 应说明问题、方案、兼容或安全影响，以及完成的验证。

Harness 必须在 consumer import 前删除 exporter root 与原始 Git source；通过 exporter-local 路径恢复不具备可移植性。

提交贡献即表示同意按仓库 `LICENSE` 中的 Apache License 2.0 授权。目前不要求 CLA。社区参与受 `CODE_OF_CONDUCT.md` 约束。
