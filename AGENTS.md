# Agent 合约

[English](AGENTS.en.md) | **中文主版本**

本仓库是 LXP 的官方 Go 参考实现，包含可复用 library、Engine 与 `cmd/lxp` composition root。不得在核心 module 中加入 Git、文件系统、OSS 等具体 Provider 实现。

## 必需验证

```bash
go test -race ./...
go vet ./...
(cd cmd/lxp && go test -race ./... && go vet ./...)
git diff --check
```

Engine 必须通过显式注入获得 Provider。Provider 与 Checker 使用全局唯一、语言无关的 `namespace:name:version` contract，并声明精确 implementation package 坐标；未知 contract、重复注册或不匹配的本地 binding 必须失败，不得静默回退。Artifact 不得携带 Provider 可执行代码或本地工作路径。`v1alpha1` 不承诺兼容性，并且只面向可信 Artifact。

Provider 的 Import 合约只有非写入的 `Validate` 与幂等、可重试的 `Apply`。Engine 必须先校验全部 Component，再写入 `importing` state、固定扩展解析结果并按父到子调用 `Apply`；每个成功 Component 立即持久化。失败时不得回滚或清理已完成内容，同 Artifact 重试必须使用固定实现继续收敛，`ready` Session 重试必须为 no-op。

CLI 必须向所有 Provider 操作传递有 deadline 的 Context；外部进程不得交互式等待 credential。Session/Workdir 的本地比较必须使用解析既有 symlink prefix 后的物理绝对路径，不能把 `filepath.Abs` 当作 realpath。

Component roots 唯一且可嵌套。Engine 按最深 root 路由，向父 Provider 提供 direct child context，Import 父到子并拒绝 symlink/non-empty collision，Export 子到父；不得在 wire model 中增加 mount capability 矩阵。

官方 CLI 的 Production Profile 只注入 `loop.exchange:git:v1`，接受 reference/embedded/mirrored `.lxpz` Artifact，并冻结为 `init/add/status/export/import/inspect/requirements`。Export 通过 `--distribution` 选择形式并默认 embedded；Import 自动读取 Artifact 声明。
