# 安全说明

更新时间：2026-06-02

本文记录 `if-i-am-gone` 当前安全边界、已实现保护措施和仍需人工注意的风险。

## 已实现保护

- Telegram 确认只接受配置的 `telegram.chat_id`，非授权 chat_id 的 callback 会被忽略并写审计。
- 每次确认按钮使用一次性 token，旧 token 在确认后失效。
- 目标流程不提前打包；只有到密码阶段才生成本轮 AES-256 加密 ZIP 和随机密码。
- 文件阶段统一发送下载链接，不通过邮件附件发送加密包。
- self_hosted 模式下载链接只暴露随机 token，不暴露服务器本地文件路径。
- self_hosted 下载 token 使用 32 字节随机数生成，并校验过期时间和最大下载次数。
- self_hosted 下载端点会对 token 缺失、过期、次数超限、文件缺失等拒绝路径写审计。
- self_hosted 下载响应设置 `Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`。
- S3-compatible 模式会上传归档并生成预签名 URL；链接有效期由 `download.s3.presign_expiry` 控制，下载次数需由对象存储侧策略或后续清理策略控制。
- 可开启 `state_protection.encrypt_password_field`，使用 `MASTER_PASSPHRASE` 加密保存在 `state.db` 中的归档密码。

## 敏感信息处理

- `.env`、`config.yaml`、`data/`、`state.db`、日志、归档文件都不应提交到 git。
- Telegram Bot Token、SMTP 授权码、`MASTER_PASSPHRASE`、S3 Secret Key 必须放在 `.env` 或部署平台的 secret 管理中。
- `config.example.yaml` 只能保留示例值，不能填真实邮箱授权码、chat_id、token 或真实下载域名中的敏感路径。
- 开启 state 密码加密后，`MASTER_PASSPHRASE` 必须长期保存；丢失后无法解密已保存的归档密码。

## 部署建议

- self_hosted 下载端点建议放在 HTTPS 反代之后，例如 Nginx、Caddy 或 Traefik。
- `download.self_hosted.public_base_url` 必须配置为受益人可访问的 HTTPS 公网地址。
- 限制服务器入站端口，只开放 SSH、HTTPS 以及必要的反代端口。
- 定期更新系统和容器基础镜像。
- 使用独立外部探活服务监控 VPS 是否在线，避免服务器整体宕机时系统无法发送心跳。

## 剩余风险

- 邮件一旦发出无法撤回；下载链接可通过过期时间和下载次数限制降低后续访问风险。
- 如果 VPS 被完全攻破，攻击者可能读取待保护文件、state、日志和环境变量。
- 如果 `MASTER_PASSPHRASE` 与服务器同时丢失，已加密 state 中的归档密码不可恢复。
- S3-compatible 预签名 URL 一旦发送给受益人，在有效期内可能被转发访问；请使用足够短的有效期、私有 bucket，并避免在公开文档或日志中记录完整预签名链接。
- 使用 S3-compatible 模式时，建议为 `ifgone/` 对象前缀配置生命周期清理规则，避免测试归档或旧归档长期留存在对象存储中。
