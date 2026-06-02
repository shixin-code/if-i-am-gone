# 首次部署 Checklist

更新时间：2026-06-02

按本清单完成首次部署，避免漏掉 Telegram、SMTP、下载链接、systemd 托管和恢复演练。

## 1. 准备 Telegram

- 使用 `@BotFather` 创建 bot，保存 Bot Token 到 `.env` 的 `TELEGRAM_BOT_TOKEN`。
- 给 bot 发送一条消息。
- 使用 `getUpdates` 查询自己的 chat_id。
- 将 chat_id 写入 `config.yaml` 的 `telegram.chat_id`。
- 本地或服务器启动后，确认能收到“本月安全确认”按钮消息。

## 2. 准备 SMTP

- 为邮箱开启 SMTP 服务。
- 使用授权码，不要使用邮箱登录密码。
- 将授权码写入 `.env` 的 `SMTP_PASSWORD`。
- 配置 `smtp.host`、`smtp.port`、`smtp.use_ssl`、`smtp.from_email`。
- 发送测试邮件，确认中文标题、正文显示正常。

## 3. 准备数据目录

- 创建 `data/source/`：存放需要被保护和最终投递的文件。
- 创建 `data/state/`：存放 `state.db`、日志、归档文件。
- 确认运行用户对两个目录有读写权限。
- 不要把 `data/` 提交到 git。

## 4. 配置目标流程

- 设置 `target_flow.checkin_day_of_month`，例如每月 1 号。
- 设置 `target_flow.daily_reminder_days`，默认 7 天。
- 设置 `target_flow.password_delay_after_warn`，默认 72h。
- 设置 `target_flow.file_delay_after_password`，默认 168h。
- 设置 `target_flow.timezone`，建议 `Asia/Shanghai`。

## 5. 配置下载链接

- 设置 `download.mode: self_hosted`。
- 设置 `download.self_hosted.public_base_url` 为公网 HTTPS 地址。
- 设置 `download.self_hosted.listen_port` 为本地监听端口，例如 8080。
- 配置 HTTPS 反代，把公网 `/download/` 转发到本地监听端口。
- 从公网访问一次下载域名，确认不会被防火墙拦截。

## 6. 可选：开启 state 密码加密

- 设置 `state_protection.encrypt_password_field: true`。
- 在 `.env` 设置 `MASTER_PASSPHRASE`。
- 将 `MASTER_PASSPHRASE` 离线备份；丢失后无法解密已保存的归档密码。

## 7. 原生 systemd 启动与验证

推荐使用原生二进制 + systemd 部署。完整命令见 `docs/native-systemd-deploy.md`，首次部署时至少完成以下动作：

```bash
go test ./...
go build -o ifgone ./cmd/ifgone
go build -o ifgonectl ./cmd/ifgonectl
sudo systemctl daemon-reload
sudo systemctl enable --now ifgone
sudo systemctl status ifgone
journalctl -u ifgone -f
```

注意：

- `/opt/ifgone/config.yaml` 中的 `storage.source_dir`、`storage.state_dir` 应指向真实部署目录。
- `/opt/ifgone/.env` 必须只保存在服务器本地，不要提交或复制到公开记录。
- 如果使用 self_hosted 下载链接，systemd 服务启动后还要确认 HTTPS 反代可访问。

如果只是做 Docker 本地 smoke，不想触碰真实 `.env` 和 `config.yaml`，可以运行：

```bash
./scripts/docker-smoke.sh
```

该脚本会在临时目录生成假配置、假数据和独立容器，验证镜像构建、容器启动、`/data/state` 可写、下载服务端口可响应。若 Docker Hub 拉取基础镜像失败，请先配置镜像源或预拉取 `golang`、`alpine` 基础镜像后重试。

注意：`docker-compose config` 会展开 `.env` 中的真实 token、SMTP 授权码和对象存储密钥。可以本地查看，但不要把完整输出写入文档、提交记录或公开日志。

验证项：

- 服务日志无配置校验错误。
- `systemctl status ifgone` 显示服务为 `active (running)`。
- Telegram 确认消息可发送，按钮回调可确认。
- SMTP 测试邮件可收到。
- self_hosted 下载服务日志显示已启动。
- HTTPS 反代可访问 `/download/` 路径。

## 8. 恢复演练

- 备份 `config.yaml`、`.env`、`data/source/`、`data/state/`。
- 在临时目录或测试服务器恢复上述文件。
- 通过 systemd 启动服务并确认 state 能打开。
- 确认 Telegram 与 SMTP 仍可用。

恢复演练通过后，首次部署才算完成。
