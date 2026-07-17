# 安全政策

[English](SECURITY.en.md) | **中文**

## 项目状态

LXP 是 public alpha。`v1alpha1` 格式与 Go MVP 尚未接受全面独立安全审计。不要把当前实现视为 hardened sandbox，也不要在缺少额外隔离和审查时用它处理恶意 Artifact。

项目以 best-effort 方式修复最新版本中的安全问题；当前没有单独维护的 release branch 或保证响应时间。

## 可信 Artifact 边界

LXP 验证 archive 结构、声明大小与 SHA-256 digest。这只能证明 bytes 与 manifest 一致，不能认证发布者、证明来源、扫描恶意软件或保证导入内容安全。

Artifact 可以包含代码、脚本、配置、Conversation 等内容。应把 Artifact、引用来源以及解释内容的 Provider 都视为可信输入。请检查 provenance 与 Requirements，通过可信渠道获取 Artifact，并使用合适的 OS-level isolation。

LXP 不会隐式执行 Prompt 或 package-local hook。Executable/MCP Checks 与 Provider activation 是本地代码执行边界，必须由 consumer 明确授权。Digest 验证不等于执行授权。

Credential 与外部服务是 consumer-managed Requirements，不是可移植 Artifact state。Secret 值不得进入 manifest、lock、payload、argv、conversation、provenance 或 log。

## 报告漏洞

不要在公开 Issue 中披露疑似漏洞。请使用代码托管平台的私有安全通告或 maintainer contact；若不可用，请通过维护者公开资料私下联系。报告应包含受影响 revision 与环境、漏洞类别与影响、最小复现、是否涉及 credential/Artifact 泄露，以及可能的缓解方式。

维护者会在可行时确认、调查并协调修复与披露。

## 范围之外

协议不承诺模型输出确定性、执行重放、外部服务监管、授权执行后对恶意内容的保护，也不保护调用者主动写入 Artifact 的秘密。绕过已记录边界的协议或实现问题仍属于安全报告范围。
