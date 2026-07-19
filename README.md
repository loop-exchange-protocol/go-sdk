# LXP 官方参考实现（Go）

[English](README.en.md) | **中文主版本**

[![CI](https://github.com/loop-exchange-protocol/lxp/actions/workflows/ci.yml/badge.svg)](https://github.com/loop-exchange-protocol/lxp/actions/workflows/ci.yml)

本仓库是 Loop Exchange Protocol 的官方参考实现，当前使用 Go，包含可复用 library（协议类型、Artifact/CAS、Provider API 与 Requirements）、Engine 和 `lxp` CLI。具体 Provider 位于独立仓库，由应用显式注入。

```go
e := engine.New(stateRoot, providers...)
```

Provider 作者主要实现 `pkg/provider.Provider`。每个 Provider 同时声明全局唯一、语言无关的 contract 坐标和精确 implementation package 坐标；EngineConfig binding 必须与实际注册实现完全一致。官方 Production MVP 只绑定 `loop.exchange:git:v1`。`Validate` 必须在不写入 Component 内容的前提下完成校验，`Apply` 必须幂等且可重试。需要原生变更选择时可额外实现 `Tracker`，需要接管既有目录时实现 `Adopter`；`NestedDiscoverer` 返回 Provider-native direct child roots，`BoundaryTracker` 同步 gitlink 等父边界 metadata。Engine 由 lexical path 推导嵌套拓扑，普通操作路由到最深 root，Import 父到子、Export 子到父；Artifact 不包含 mount capability DSL。协议规范、Schema 与权威示例位于 [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol)。

通用 Engine API 按 Provider 声明支持 `reference`、`embedded` 与 `mirrored`；Component 将实际 distribution、locator、revision 与 payload 交给对应 contract 校验和应用。Mirrored 的 reference/embedded revision 必须相同，安全 locator、selected state 与 fallback 语义由对应 contract 定义。Import 在调用 `Apply` 前写入 `importing` state 并固定本地扩展解析结果，在每个 Component 成功后持久化进度；失败不会回滚或清理已完成内容，使用同一 Artifact 与同一实现重试会继续收敛，已为 `ready` 的 Session 重试为 no-op。

## 安装 CLI

CLI 是 `cmd/lxp` 子目录中的独立 Go module，发布 tag 使用 `cmd/lxp/vX.Y.Z` 前缀：

```bash
go install github.com/loop-exchange-protocol/lxp/cmd/lxp@latest
lxp help
```

Public alpha 首个版本为 `v0.1.0-alpha.1`；`@latest` 在没有 stable release 时会选择最高 pre-release。发布 module 不包含 `replace`。本地活跃布局为三个仓库：规范源 `loop-exchange-protocol`、本参考实现 `lxp` 与 `provider-git`；上层 `go.work` 组合两个 Go 实现仓库中的 module。

CLI 默认给外部操作 15 分钟 deadline；可用正数 Go duration 覆盖，例如 `LXP_TIMEOUT=30m lxp import ...`。Git Provider 还会禁用交互式 credential prompt；认证必须由现有非交互 credential helper 或 SSH agent 满足。Session 存储和发现统一使用解析 symlink prefix 后的物理路径，因此 macOS 的 `/var` 与 `/private/var` 不会被误判为不同 Workdir。

## 验证

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` 是独立嵌套 module，也是官方 Production MVP composition root：它只组合本仓 library、Engine 与 `provider-git`。公开命令面为 `init/add/status/export/import/inspect/requirements`；`lxp export --distribution` 支持 reference/embedded/mirrored `.lxpz`（默认 embedded），Import 自动读取 Artifact 声明。

真实三仓布局中的 Harness 位于 `provider-git`，并直接验证上述公开 CLI 的 reference 在线导入、reference 离线失败与可重试状态，以及 mirrored 离线 fallback。

CLI 集成测试还用 parent/child/grandchild 三个 remote 覆盖自动注册递归 Git submodule、三种 distribution、offline child 恢复、Import 后父仓 native submodule config/递归 status 一致、child commit 与父 gitlink staged selection。

协议版本为 `v1alpha1`，Go module 使用 `v0.x` alpha tag；两者都不承诺兼容性，并仅面向可信 Artifact。
