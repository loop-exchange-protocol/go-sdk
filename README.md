# LXP Go SDK

[English](README.en.md) | **中文主版本**

本仓库提供 Loop Exchange Protocol 的 Go SDK：协议类型、Artifact/CAS、Provider API、Engine 与 Requirements。具体 Provider 不属于 SDK；应用必须显式注入 Provider。

```go
e := engine.New(stateRoot, providers...)
```

Provider 作者主要实现 `pkg/provider.Provider`，需要原生变更选择时额外实现 `Tracker`，需要接管既有目录时实现 `Adopter`。协议规范、Schema 与权威示例位于 [`loop-exchange-protocol`](https://github.com/loop-exchange-protocol/loop-exchange-protocol)。

## 验证

```bash
go test -race ./...
go vet ./...
cd cmd/lxp && go test -race ./... && go vet ./...
```

`cmd/lxp` 是独立嵌套 module，也是官方 Production MVP composition root：它只组合 SDK 与 `go-provider-git`。公开命令面为 `init/add/status/export/import/inspect/requirements`，并且只接受 embedded `.lxpz` Artifact。开发时使用相邻仓库的 `replace`；发布时必须改为已发布版本。

当前版本为 `v1alpha1`，不承诺兼容性，并仅面向可信 Artifact。
