# Agent 合约

[English](AGENTS.en.md) | **中文主版本**

本仓库只包含语言级 Go SDK、Engine 与 `cmd/lxp` composition root。不得在核心 module 中加入 Git、文件系统、OSS 等具体 Provider 实现。

## 必需验证

```bash
go test -race ./...
go vet ./...
(cd cmd/lxp && go test -race ./... && go vet ./...)
git diff --check
```

Engine 必须通过显式注入获得 Provider。未知 `provider + contract` 必须失败，不得静默回退。Artifact 不得携带 Provider 可执行代码或本地物化路径。`v1alpha1` 不承诺兼容性，并且只面向可信 Artifact。

官方 CLI 的 Production Profile 只注入 `git@v1`，接受 reference/embedded/mirrored `.lxpz` Artifact，并冻结为 `init/add/status/export/import/inspect/requirements`。Export 通过 `--distribution` 选择形式并默认 embedded；Import 自动读取 Artifact 声明。Provider Plan 是 Import 内部 preflight，不是公开命令。
