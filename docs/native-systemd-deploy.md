# 原生 systemd 部署说明

更新时间：2026-06-02

本文说明如何在 VPS 上以原生二进制 + systemd 方式部署 `if-i-am-gone`。Go 程序可以编译为独立可执行文件，长期运行时比 Docker 少一层容器依赖，更适合本项目这种单服务、低资源、强依赖宿主机持久目录和外部探活的场景。

Docker 仍可作为可选部署方式，用于本地 smoke、临时测试或你已经有成熟容器运维体系的服务器。

## 推荐目录

```text
/opt/ifgone/
  ifgone
  ifgonectl
  config.yaml
  .env
  data/
    source/
    state/
```

目录说明：

- `/opt/ifgone/ifgone`：主服务二进制。
- `/opt/ifgone/ifgonectl`：本地管理 CLI。
- `/opt/ifgone/config.yaml`：运行配置。
- `/opt/ifgone/.env`：Telegram、SMTP、S3、`MASTER_PASSPHRASE` 等敏感环境变量。
- `/opt/ifgone/data/source`：待保护文件目录。
- `/opt/ifgone/data/state`：`state.db`、归档、日志和下载 token 状态目录。

## 构建二进制

在项目根目录执行：

```bash
go test ./...
go build -o ifgone ./cmd/ifgone
go build -o ifgonectl ./cmd/ifgonectl
```

如果在本地构建后上传到 Linux VPS，请确保目标系统架构一致。例如 x86_64 Linux：

```bash
GOOS=linux GOARCH=amd64 go build -o ifgone ./cmd/ifgone
GOOS=linux GOARCH=amd64 go build -o ifgonectl ./cmd/ifgonectl
```

ARM64 VPS 可使用：

```bash
GOOS=linux GOARCH=arm64 go build -o ifgone ./cmd/ifgone
GOOS=linux GOARCH=arm64 go build -o ifgonectl ./cmd/ifgonectl
```

## 创建运行用户和目录

```bash
sudo useradd --system --home /opt/ifgone --shell /usr/sbin/nologin ifgone
sudo mkdir -p /opt/ifgone/data/source /opt/ifgone/data/state
sudo cp ifgone ifgonectl /opt/ifgone/
sudo cp config.example.yaml /opt/ifgone/config.yaml
sudo cp .env.example /opt/ifgone/.env
sudo chown -R ifgone:ifgone /opt/ifgone
sudo chmod 750 /opt/ifgone
sudo chmod 750 /opt/ifgone/data /opt/ifgone/data/source /opt/ifgone/data/state
sudo chmod 600 /opt/ifgone/config.yaml /opt/ifgone/.env
sudo chmod 750 /opt/ifgone/ifgone /opt/ifgone/ifgonectl
```

编辑 `/opt/ifgone/config.yaml` 和 `/opt/ifgone/.env` 后，确认路径与部署目录一致：

```yaml
storage:
  source_dir: /opt/ifgone/data/source
  state_dir: /opt/ifgone/data/state
```

如果使用 self_hosted 下载链接，还需要把 `download.self_hosted.public_base_url` 配置为公网 HTTPS 地址，并按 `docs/https-reverse-proxy.md` 配置 Nginx 或 Caddy 反代到本机下载端口。

## systemd 服务文件

创建 `/etc/systemd/system/ifgone.service`：

```ini
[Unit]
Description=if-i-am-gone dead man's switch
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/ifgone
EnvironmentFile=/opt/ifgone/.env
ExecStart=/opt/ifgone/ifgone --config /opt/ifgone/config.yaml --tick 1m
Restart=always
RestartSec=10s
User=ifgone
Group=ifgone
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=/opt/ifgone/data/state
ReadOnlyPaths=/opt/ifgone/data/source

[Install]
WantedBy=multi-user.target
```

说明：

- `Restart=always`：进程异常退出后自动拉起。
- `EnvironmentFile`：从 `.env` 注入敏感变量，不需要写入 service 文件。
- `ReadOnlyPaths=/opt/ifgone/data/source`：待保护文件默认只读，降低误写风险。
- `ReadWritePaths=/opt/ifgone/data/state`：state、日志、归档和下载 token 必须可写。

## 启动与验证

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ifgone
sudo systemctl status ifgone
journalctl -u ifgone -f
```

本地管理检查：

```bash
sudo -u ifgone /opt/ifgone/ifgonectl status --config /opt/ifgone/config.yaml
sudo -u ifgone /opt/ifgone/ifgonectl dry-run --config /opt/ifgone/config.yaml
sudo -u ifgone /opt/ifgone/ifgonectl test-email --config /opt/ifgone/config.yaml --to you@example.com
```

首次部署至少确认：

- 服务状态为 `active (running)`。
- 日志没有配置校验错误。
- Telegram 能收到安全确认消息，并且点击后返回“本月已确认，祝君安康！”。
- SMTP 测试邮件可收到。
- 如果使用 self_hosted，公网 HTTPS 反代能访问 `/download/` 路径。
- 如果使用 S3-compatible，真实对象存储上传与预签名链接已按联调 checklist 验证。

## 更新版本

更新前建议先备份：

```bash
sudo systemctl stop ifgone
sudo cp -a /opt/ifgone/data/state /opt/ifgone/data/state.backup.$(date +%Y%m%d-%H%M%S)
sudo cp /opt/ifgone/config.yaml /opt/ifgone/config.yaml.backup.$(date +%Y%m%d-%H%M%S)
sudo cp /opt/ifgone/.env /opt/ifgone/.env.backup.$(date +%Y%m%d-%H%M%S)
```

替换二进制并重启：

```bash
sudo cp ifgone ifgonectl /opt/ifgone/
sudo chown ifgone:ifgone /opt/ifgone/ifgone /opt/ifgone/ifgonectl
sudo chmod 750 /opt/ifgone/ifgone /opt/ifgone/ifgonectl
sudo systemctl start ifgone
sudo systemctl status ifgone
```

## 常用排查命令

```bash
systemctl status ifgone
journalctl -u ifgone --since "1 hour ago"
sudo -u ifgone /opt/ifgone/ifgonectl status --config /opt/ifgone/config.yaml
sudo -u ifgone /opt/ifgone/ifgonectl cleanup-tokens --config /opt/ifgone/config.yaml
```

如果服务无法启动，优先检查：

- `/opt/ifgone/.env` 是否存在且包含必要变量。
- `/opt/ifgone/config.yaml` 是否仍使用示例值。
- `storage.source_dir`、`storage.state_dir` 是否存在且权限正确。
- `download.self_hosted.listen_port` 是否被其他进程占用。
- `MASTER_PASSPHRASE` 是否与旧 state 加密配置一致。
