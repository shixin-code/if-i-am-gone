# 项目指引

## 仓库概览
- 本仓库是 Go 实现的「死亡开关 / Dead Man's Switch」项目：用户长期未确认存活后，系统按阶段向受益人发送预警、解压密码和加密文件下载链接。
- 主程序入口为 `cmd/ifgone`，本地管理 CLI 为 `cmd/ifgonectl`。
- 核心模块分层：
  - `internal/scheduler`：调度状态机，只依赖接口，不直接耦合具体通知实现
  - `internal/state`：SQLite 持久化状态、投递记录、下载 token、审计日志
  - `internal/app`：把 `telegram`、`mailer`、`download`、`templates`、`packer` 组装为调度器依赖
  - `internal/config`：YAML 配置加载与校验
  - `internal/packer`：AES-256 ZIP 打包
  - `internal/download`：`self_hosted` 或 `s3` 下载链路

## 常用命令
- 构建主程序：`go build ./cmd/ifgone`
- 构建管理 CLI：`go build ./cmd/ifgonectl`
- 全量测试：`go test ./...`
- 单包测试：`go test ./internal/scheduler`
- 运行主程序：`./ifgone --config config.yaml --tick 1m`
- 查看本地状态：`./ifgonectl status --config config.yaml`

## 交流语言 
- 所有回复必须使用简体中文。 
- 即使用户使用英文提问，也必须用中文回答。 
- 技术术语可以保留英文，但解释必须是中文。 
- 如果模型认为英文更合适，也必须先用中文解释，再给英文内容。
- 模型不得因为上下文、代码语言、文件内容为英文而自动切换到英文。 
- 当用户要求英文时，才允许使用英文。
- 模型生成中间文档，如计划Plan, 待办任务 Task 待文件，也一律使用简体中文

## 交互规范
- 回答要求：尽量 **简洁但不牺牲关键细节**，避免废话。 
- 当用户提出问题时，如果存在潜在风险或误解，需主动指出并给出更优方案。
- 模型生成的中间文件，如计划，任务等不要删除，方便用户后续查询
- 在执行Plan之前，先按逻辑相关性生成 Task 待执行任务列表，之后再一个任务一个任务逐步执行
- 当提供多个方案给用选择时，给方案提供编号，用户直接回复编号即实现方案选择
- 所有需要用户确认的都提供编号给用户选择，用户方便回复编号
- 我的方案和建议不一定都对，不要迁就我，深入思考我的方案和建议，如果不合适就反驳我，并给出更好的方案

## 项目架构红线
- 调度器必须保持“基于持久化绝对时间戳的无状态纯函数”特性。`scheduler.Tick(now)` 的决策只能依赖当前时间和 `state.db` 中的持久化时间戳，不能引入依赖进程持续运行的内存状态。
- 投递必须保持幂等。每个受益人每个阶段只能成功投递一次；修复或重构时不能破坏重启后不重复发送的语义。
- 用户通过 Telegram 确认后，必须立即重置回 `ALIVE`，并清理本轮触发态相关状态；这是唯一明确的业务回退入口。
- 目标流程不能提前生成最终投递包。必须到密码阶段才进行打包并生成本轮密码。
- 不要让 `internal/scheduler` 直接依赖 `telegram`、`mailer`、`s3` 等具体实现，优先通过接口保持可测试性与低耦合。

## 仓库修改约束
- 涉及 `internal/scheduler` 的状态推进、时间计算、确认重置、投递条件修改时，必须同步检查 `internal/state`、相关测试和文档是否需要一起更新。
- 涉及配置结构、默认值、字段语义变更时，至少同步检查 `config.example.yaml`、`README.md`、相关 `docs/` 文档和配置校验逻辑，避免文档与程序行为漂移。
- 涉及下载链路修改时，要明确区分 `self_hosted` 与 `s3` 两种模式，不要只改其中一条路径就默认整体完成。
- 涉及状态迁移、触发流程归一化、密码存储逻辑时，要优先考虑兼容已有 `state.db`，避免把用户线上状态直接打坏。
- 如果修改影响核心流程但用户未明确要求跳过验证，优先建议先跑相关测试，再决定是否补全更大范围验证。

## 配置与安全约束
- 敏感信息必须放在 `.env`，配置文件通过 `${ENV}` 引用；不要把真实 token、SMTP 密码、`MASTER_PASSPHRASE` 等写入版本库。
- 默认不提交真实 `config.yaml`、`.env`、`data/`、`state.db`、归档文件和构建产物。
- 涉及 `state_protection.encrypt_password_field`、下载 token、打包密码、归档路径的改动时，要明确提示安全影响，避免日志或文档中泄露敏感信息。
- 涉及状态库迁移、清理、回填时，优先提醒先备份 `data/state`、`config.yaml` 和 `.env`。

## 部署与运行偏好
- 默认推荐原生 `systemd` 部署路径，Docker 仅作为可选部署或测试手段，不要默认把 Docker 视为主路径。
- 用户问部署或验收时，优先引用现有文档，而不是重复发散式口述步骤。
- 调试运行态问题时，优先看当前配置、当前状态库、当前日志和实际进程行为，不要只靠代码猜测。

## 代码修改执行规范
- 用户反馈代码结果或者问题时**不要直接修改代码**，先分析原因并给出方案，待用户确认后再执行代码修改
- 如果有多种实现方案，请列出并分析不同方案的优劣
- 实现较大的功能要应用设计模式，考虑功能解耦、代码复用、组件复用
- 计划、实现功能时要参考**软件设计原则**
   - 单一职责（SRP）
   - 开闭原则（OCP）
   - 氏替换（LSP）
   - 接口隔离（ISP）
   - 依赖倒置（DIP）
   - DRY（不要重复）
   - KISS（保持简单）
   - 高内聚、低耦合
   - 可测试性（Testability）
   - 可扩展性（Extensibility）
- 代码中展示给用户文本规范
   - 用户可见文本禁止硬编码，全部资源化。
   - 同时翻译成两种语言：英文放 values/strings.xml，中文放 values-zh-rCN/strings.xml。
   - key 用 snake_case：模块_场景_语义。
   - 动态文案用 %1$s/%1$d，占位符禁止拼接。

## 测试与文档入口
- 核心调度行为优先查看或补充 `internal/scheduler` 相关测试。
- 快速演练文档：`docs/quick-flow-drill.md`
- 真实链路验收清单：`docs/real-flow-integration-checklist.md`
- 原生部署文档：`docs/native-systemd-deploy.md`
- 首次部署检查：`docs/deploy-checklist.md`
- 状态迁移说明：`docs/state-migration.md`
- 变更较大时，优先先更新任务文件或计划文件，再进入实现或文档修改。

## git 使用
- 当用户说 **gitlog** 时，基于当前的修改生成中文 git log，但不要自动commit
- git commit 格式：
   ```
   <type>(<scope>): <subject>
   <空行>
   <body>
   ```
- 类型 (type)：
  - feat: 新功能 (feature)
  - fix: 修补 bug
  - docs: 文档变更
  - style: 不影响代码含义的格式变动（如空格、分号等）
  - refactor: 重构（即不是新增功能，也不是修改 bug 的代码变动）
  - test: 增加测试或修改测试用例
  - chore: 构建过程或辅助工具的变动

## SKILL 使用
 - supperpowers skill 分析过程中生成的文档存放于 `.code/supperpowers` 目录下，不提交版本库
 - 每次代码前不要自动使用TDD，可先问我要不要先写测试再实现还是直接实现代码。用编号给我选择
