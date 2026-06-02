# 灾难恢复说明

更新时间：2026-06-02

本文用于在 VPS 故障、容器重建、误删数据或迁移服务器时恢复 `if-i-am-gone`。

## 必须备份的内容

- `config.yaml`：运行配置，包含真实 chat_id、收件人、下载域名等。
- `.env`：Telegram Bot Token、SMTP 授权码、`MASTER_PASSPHRASE` 等敏感变量。
- `data/source/`：待保护的原始文件。
- `data/state/state.db`：流程状态、投递幂等记录、下载 token、审计记录和当前归档密码字段。
- `data/state/archives/`：已经生成的 AES-256 加密 ZIP。
- `data/state/app.log`：运行日志，便于事故排查。

建议至少保留一份离线备份，并确保备份介质本身加密保存。

## 恢复步骤

1. 在新服务器安装 Docker 或 Go 运行环境。
2. 恢复 `config.yaml`、`.env`、`data/source/`、`data/state/` 到原路径或同步修改配置路径。
3. 确认 `.env` 中的 `MASTER_PASSPHRASE` 与原环境一致。
4. 确认 `download.self_hosted.public_base_url` 指向新服务器可访问的 HTTPS 地址。
5. 启动服务：`docker compose up -d --build` 或运行本地二进制。
6. 查看日志确认 state 打开成功、Telegram polling 启动、self_hosted 下载服务启动。
7. 发送一次 Telegram 确认，确认 Bot 回调正常。

## 常见故障处理

### VPS 短时宕机

服务恢复后，Scheduler 会按持久化时间戳继续推进当前阶段。若有未确认的旧月度确认，不会被新月确认覆盖，会继续按旧确认补推进流程。

### `state.db` 损坏或丢失

如果只丢失 `state.db`，系统会创建新的初始状态，但会失去投递幂等记录、下载 token 和当前归档密码字段。此时应优先从备份恢复 `state.db`。

### 归档文件丢失

如果密码阶段已经发送密码但归档文件丢失，受益人收到的下载链接可能无法下载。应从备份恢复对应 `archives/` 文件；如果无法恢复，需要人工重新评估是否重新触发流程。

### `MASTER_PASSPHRASE` 丢失

开启 `state_protection.encrypt_password_field` 后，如果 `MASTER_PASSPHRASE` 丢失，已保存在 state 中的归档密码无法解密。只能依赖仍可读取的历史邮件、人工备份或重新打包生成新密码。

### Telegram Bot Token 或 SMTP 授权码失效

更新 `.env` 后重启服务。重启不会清空 state，投递幂等记录仍会保留。

## 恢复后检查清单

- `go test ./...` 或镜像构建测试通过。
- 服务日志没有配置校验错误。
- Telegram 确认消息可发送，按钮回调可收到确认。
- SMTP 可发送测试邮件。
- `download.self_hosted.public_base_url` 可从公网访问。
- 历史 `state.db` 中当前阶段与预期一致。
