# 外部探活与失联告警建议

本文说明如何给 `if-i-am-gone` 增加服务外部探活。外部探活的目标不是替代 Telegram/SMTP，而是在 VPS 宕机、程序退出、网络异常、磁盘满或 Telegram/SMTP 无法发出时，尽早让你发现系统本身已经失效。

## 建议方案

优先使用独立于部署 VPS 的第三方或另一台机器监控：

- `healthchecks.io`：适合简单心跳。服务定时访问一个专属 ping URL，超时未收到就告警。
- `Uptime Kuma`：适合自建监控。可以监控 HTTP 端点、TCP 端口、systemd 服务或 Docker 容器状态，也能转发到 Telegram、邮件或其他通知渠道。
- 云厂商监控：适合已经使用云服务器的场景，可监控实例状态、端口可达性、磁盘和 CPU。

推荐顺序：

1. 先配置 Uptime Kuma 或 healthchecks.io 监控 VPS 是否在线。
2. 再监控 self-hosted 下载端点的 HTTPS 可达性。
3. 最后再启用程序内主动 ping webhook。内置 ping 已实现，但仍建议把它作为补充信号，而不是唯一监控来源。

## 监控目标

至少监控以下项目：

- VPS 是否在线：例如 TCP 22、HTTP 80/443 或云厂商实例状态。
- 下载服务是否可访问：监控 `https://你的域名/download/__health_placeholder__` 可返回 `404` 或 `403`，只要说明服务能响应即可。
- HTTPS 证书是否过期：Uptime Kuma 可直接监控证书有效期。
- 磁盘空间是否不足：`data/state` 和归档目录无法写入会影响状态与打包。
- systemd 服务是否运行：如果使用原生部署，应监控 `ifgone.service` 是否处于 active 状态。
- Docker 容器是否运行：如果使用 docker compose 部署，应监控容器 restart 次数和当前状态。

## 程序内主动 ping

程序已支持可选的主动 ping webhook。启用后，进程会按配置定时访问 `ping_url`；访问失败只写日志和审计，不会阻止 Telegram 确认、打包或邮件投递。

示例配置：

```yaml
reliability:
  healthcheck:
    enabled: true
    ping_url: ${HEALTHCHECK_PING_URL}
    interval: 10m
    timeout: 10s
```

`.env` 示例：

```bash
HEALTHCHECK_PING_URL=https://hc-ping.com/你的-uuid
```

注意：程序内 ping 只能证明“程序进程运行到 ping 循环”。如果 VPS 整体宕机、网络断开或程序无法启动，ping 会停止，但它不能主动发出最后一条告警；告警仍由 healthchecks.io、Uptime Kuma 或其他外部监控系统负责。

## healthchecks.io 手工接入

如果暂不启用程序内 ping，也可以继续用宿主机 cron 做外部心跳：

```bash
*/10 * * * * curl -fsS --max-time 10 https://hc-ping.com/你的-uuid >/dev/null
```

如果想确认程序进程仍在运行，可以改成：

```bash
*/10 * * * * pgrep -f "ifgone" >/dev/null && curl -fsS --max-time 10 https://hc-ping.com/你的-uuid >/dev/null
```

如果使用原生 systemd：

```bash
*/10 * * * * systemctl is-active --quiet ifgone && curl -fsS --max-time 10 https://hc-ping.com/你的-uuid >/dev/null
```

如果使用 Docker：

```bash
*/10 * * * * docker compose ps ifgone | grep -q "Up" && curl -fsS --max-time 10 https://hc-ping.com/你的-uuid >/dev/null
```

注意：cron 心跳只能证明“探测脚本认为服务存在”，不能证明 Telegram/SMTP 一定可用。

## Uptime Kuma 配置建议

建议创建以下监控项：

| 名称 | 类型 | 目标 | 期望 |
|---|---|---|---|
| VPS SSH | TCP | `服务器IP:22` | 可连接 |
| 下载服务 HTTPS | HTTP(s) | `https://你的域名/download/not-a-real-token` | 返回 404 或 403 也算服务活着 |
| 证书有效期 | HTTP(s) | `https://你的域名/` | 证书未过期 |
| systemd 状态 | Push/脚本 | `systemctl is-active --quiet ifgone` | active |
| Docker 状态 | Docker Container | `ifgone` 容器 | running |

告警渠道建议不要只用 Telegram。如果 Telegram Bot 或网络到 Telegram 出问题，监控告警也可能收不到。建议至少配置一个备用渠道，例如邮件、短信、企业微信、ntfy 或另一套 IM。

## 告警处理 checklist

收到外部探活告警后，按以下顺序检查：

1. 登录 VPS，确认机器是否在线、磁盘是否满。
2. 查看服务状态：原生部署用 `systemctl status ifgone`；Docker 部署用 `docker compose ps`。
3. 查看日志：原生部署用 `journalctl -u ifgone -f` 或 `data/state/app.log`；Docker 部署用容器日志。
4. 确认 `.env`、`config.yaml`、`data/state/state.db` 是否仍存在且权限正确。
5. 如果下载服务不可达，检查反代、域名解析、防火墙和 TLS 证书。
6. 如果 Telegram/SMTP 失败，先用最小测试消息验证凭据和网络。

## 实现边界

- 内置 ping 独立于核心调度状态机。
- ping 成功写入 `healthcheck_ping_ok` 审计。
- ping 失败写入 `healthcheck_ping_failed` 审计，并输出日志。
- ping 失败不改变死亡开关状态，不阻止确认、打包、密码邮件或下载链接投递。
