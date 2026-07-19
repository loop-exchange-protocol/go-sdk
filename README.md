# LXP Go SDK

[English](README.en.md) | **中文主版本**

本仓库提供 Loop Exchange Protocol 的 Go SDK：协议类型、Artifact/CAS、Provider API、Engine 与 Requirements。具体 Provider 不属于 SDK；应用必须显式注入 Provider。

```go
e := engine.New(stateRoot, providers...)
```

Provider 作者主要实现 `pkg/provider.Provider`，需要原生变更选择时额外实现 `Tracker`，需要接管既有目录时实现 `Adopter`。`NestedDiscoverer` 可按 contract 准备并返回 Provider-native direct child roots，`BoundaryTracker` 可同步 gitlink 等父边界 metadata。Engine 由 lexical path 推导嵌套拓扑，普通操作路由到最深 root，Import 父到子、Export 子到父；Artifact 不包含 mount capability DSL。协议规范、Schema 与权威示例位于 [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol)。

通用 Engine API 按 Provider 声明支持 `reference`、`embedded` 与 `mirrored`，并在 Plan 中传递实际 distribution、locator 与 revision。Mirrored 的 reference/embedded revision 必须相同。具体安全 locator、selected state 与 fallback 由匹配的 Provider contract 实现。

## 验证

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` 是独立嵌套 module，也是官方 Production MVP composition root：它只组合 SDK 与 `go-provider-git`。公开命令面为 `init/add/status/export/import/inspect/requirements`；`lxp export --distribution` 支持 reference/embedded/mirrored `.lxpz`（默认 embedded），Import 自动读取 Artifact 声明。开发时使用相邻仓库的 `replace`；发布时必须改为已发布版本。

真实四仓库 Harness 位于 `go-provider-git`，并直接验证上述公开 CLI 的 reference 在线导入、reference 离线失败清理与 mirrored 离线 fallback。

CLI 集成测试还用 parent/child/grandchild 三个 remote 覆盖自动注册递归 Git submodule、三种 distribution、offline child 恢复、child commit 与父 gitlink staged selection。

当前版本为 `v1alpha1`，不承诺兼容性，并仅面向可信 Artifact。
