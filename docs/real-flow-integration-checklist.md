# 真实目标流程联调 Checklist

更新时间：2026-06-02

本文用于真实 Telegram、SMTP、self_hosted 或 S3-compatible 下载链路的最终联调。联调目标是证明 `docs/telegram-to-delivery-flow.md` 中的目标流程在真实外部服务上可闭环。

## 安全边界

- 不要在本文、任务表、提交信息或公开日志中记录真实 Telegram token、chat_id、SMTP 授权码、S3 access key、S3 secret key、完整收件人邮箱、完整下载链接或完整预签名 URL。
- 联调时优先使用测试受益人邮箱和测试文件；不要把真实隐私资料放入 `data/source/`。
- 如果必须使用真实受益人邮箱，邮件正文中应明确这是测试，不要包含敏感内容。
- `docker-compose config` 会展开 `.env`，只能本地查看，不要复制完整输出。

## 前置准备

1. 备份当前 `config.yaml`、`.env`、`data/state/`。
2. 准备测试文件：

```bash
mkdir -p data/source data/state
printf 'real flow integration test\n' > data/source/integration-test.txt
```

3. 确认基础能力：

```bash
go test ./...
go build ./cmd/ifgone ./cmd/ifgonectl
./ifgonectl status --config config.yaml
./ifgonectl dry-run --config config.yaml
```

4. 确认 `.env` 至少包含 Telegram Bot Token、SMTP 授权码、`MASTER_PASSPHRASE`。如果 `download.mode=s3`，还需要 S3 access key 和 secret key。

## 快速节奏配置

建议在测试环境使用短延迟：

```yaml
target_flow:
  checkin_day_of_month: <今天的日期>
  daily_reminder_days: 1
  password_delay_after_warn: 1m
  file_delay_after_password: 1m
  timezone: Asia/Shanghai
```

注意：连续提醒按“天”计算，无法仅靠 `--tick 10s` 完全压缩。需要完整走到受益人预提醒时，可在 D0 安全确认已发送后使用受控测试辅助命令推进时间：

```bash
./ifgonectl drill advance-checkin --config config.yaml --days 2
```

该命令只回拨确认发送时间戳，不发送消息、不打包、不伪造邮件投递结果。只建议在测试库或演练环境使用。

## 联调路径 A：self_hosted 下载

配置要点：

```yaml
download:
  mode: self_hosted
  link_expiry: 336h
  max_downloads: 5
  self_hosted:
    public_base_url: https://your-domain.example.com
    listen_port: 8080
```

验收步骤：

1. 启动服务，确认日志显示下载服务启动。
2. 从公网访问 `https://your-domain.example.com/download/not-a-real-token`，返回 404 或 403 也可接受，关键是反代可达。
3. 触发文件阶段后，受益人收到下载链接邮件。
4. 使用邮件中的链接下载 ZIP。
5. 使用此前密码邮件中的密码通过 7-Zip、WinRAR 或 Keka 解压。
6. 下载次数达到 `max_downloads` 后再次访问应被拒绝。

验收证据只记录脱敏摘要：

```text
self_hosted 公网路径可达：通过 / 未通过
受益人收到下载链接邮件：通过 / 未通过
ZIP 下载：通过 / 未通过
密码解压：通过 / 未通过
次数限制：通过 / 未通过
日志/审计摘要：download_token_created / download_success / download_limit_exceeded 是否出现
```

## 联调路径 B：S3-compatible 下载

配置要点：

```yaml
download:
  mode: s3
  link_expiry: 336h
  max_downloads: 5
  s3:
    endpoint: https://s3.amazonaws.com
    bucket: your-private-bucket
    region: us-east-1
    access_key: ${S3_ACCESS_KEY}
    secret_key: ${S3_SECRET_KEY}
    presign_expiry: 168h
```

验收步骤：

1. 确认 bucket 为私有，未开启公开读。
2. 确认 access key 只具备必要的 `PutObject` / `GetObject` 预签名权限。
3. 触发文件阶段后，确认对象存储中出现 `ifgone/YYYY-MM-DD/...` 对象。
4. 受益人收到预签名下载链接邮件。
5. 使用邮件中的预签名链接下载 ZIP。
6. 使用此前密码邮件中的密码解压。
7. 等待或模拟链接过期后，确认旧链接不可访问。
8. 联调结束后删除测试对象，或确认 bucket 生命周期规则会自动清理 `ifgone/` 前缀下的测试归档。

验收证据只记录脱敏摘要：

```text
S3 上传：通过 / 未通过
对象 key 前缀：ifgone/YYYY-MM-DD/...
受益人收到预签名链接邮件：通过 / 未通过
ZIP 下载：通过 / 未通过
密码解压：通过 / 未通过
过期后拒绝：通过 / 未通过
测试对象清理：通过 / 未通过
日志/审计摘要：s3_presigned_link_created 是否出现
```

## 目标主链验收

| 阶段 | 用户本人 Telegram | 受益人 Email | 状态/审计 |
|---|---|---|---|
| D0 安全确认 | 收到确认按钮 | 无 | `pending_token` 已设置 |
| 连续提醒 | 收到第 N 天提醒 | 无 | `miss_count` 更新 |
| 预提醒 | 收到即将通知受益人阶段提醒 | 收到预提醒邮件 | `entered_warned` |
| 密码 | 收到即将打包并发送密码阶段提醒 | 收到密码邮件 | `packed`、`entered_password_sent` |
| 下载链接 | 收到即将发送下载链接阶段提醒 | 收到下载链接邮件 | `entered_file_sent` |
| 完成 | 无新增 | 无新增 | `COMPLETED` |

## 取消路径验收

在以下任一阶段点击最新 Telegram 确认按钮：

- 连续提醒期。
- 预提醒邮件发送后、密码阶段前。
- 密码邮件发送后、下载链接邮件前。

预期：

- Telegram 回调显示 `本月已确认，祝君安康！`。
- state 回到 `ALIVE`。
- 后续 tick 不继续发送下一阶段邮件。
- 若已经进入触发流程，deliveries 被清理，下次触发重新投递。

## 投递失败重试验收

建议用测试 SMTP 账号演练：

1. 在某一邮件阶段前临时设置错误 SMTP 授权码。
2. 等待 tick，确认投递失败并保持当前阶段。
3. 恢复正确授权码并重启。
4. 等待 tick，确认失败项重试成功，已成功项不重复发送。

## 联调记录模板

```text
联调日期：
环境：本地 / 测试 VPS / 正式 VPS
下载模式：self_hosted / s3
Telegram D0 确认：通过 / 未通过
Telegram 阶段提醒：通过 / 未通过
SMTP 预提醒：通过 / 未通过
SMTP 密码邮件：通过 / 未通过
SMTP 下载链接邮件：通过 / 未通过
ZIP 下载：通过 / 未通过
ZIP 解压：通过 / 未通过
取消路径：通过 / 未通过
投递失败重试：通过 / 未通过
外部探活 ping：通过 / 未通过 / 未启用
敏感信息已脱敏：是 / 否
备注：
```
