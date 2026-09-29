你负责实施“ANI Image最小闭环”，不是继续讨论方案。先读取解压后的ani-image-mvp-v1.0设计包：README、SOURCES、docs/specs、docs/plans/image-mvp.md、各IMG批次、Fedora手册、contracts和templates/import-map.md；再读实际仓库AGENTS、START-HERE、ADR2/6、remote-execution及唯一执行状态。本次用户限制优先于旧文档的Ubuntu/本地回退条款。

范围固定：在ani-resource-service内增加Image领域，单repo/module/process；Harbor私有租户Project+私有平台共享Project；自动初始化；租户受限发布/运行两类身份；用户Push后登记固定Digest；用途多选、加速器声明；一个真实普通容器业务和一页最小UI。无RLS，无完整镜像目录同步、扫描/审计产品、构建、GC、通用凭证平台或Compute新服务。不读取/修改zhangzhe-ctrl/ANI。

现在从IMG-00开始，按计划连续执行IMG-00～IMG-11及全部小任务。一个批次已通过相关门禁后立即进入下一依赖满足的批次，不问我“是否继续”，不止步于写计划/骨架/Proto。仅当无可推进独立任务、权限不足、必要信息确实不可解析或存在不可安全恢复的外部副作用时停止，并写明恢复动作。

先冻结Resource/Governance实际SHA与工作区，核对设计阅读基线但不强制回退。按import-map迁入新增文档，不能覆盖README/AGENTS/历史状态。查明普通容器owner、tenant Namespace解析和前端的真实非ANI仓库/文件/方法，记录bindings。未知不编造：可以继续后端独立批次，相关owner/UI/product任务保留blocked；验收driver的Pod不能冒充产品接入。

本地只能阅读/编辑源码文档、审阅diff、普通Git提交推送和SSH/SCP转运。生成、gofmt、依赖下载、go list/build/test、Buf/sqlc/protoc、真PG、容器构建、前端构建、live API驱动全部ssh fedora执行；失败不得回退本地或Ubuntu。Fedora不手改业务源码、不提交/推送产品代码。使用真实HOME下新run目录、现有有效重任务锁和手册预算；不改全局环境、不抢锁、不杀未知进程。

每轮：本地编码→commit/push到review分支→Fedora检出该完整SHA→限资源生成/格式化→按文件白名单及base hash回传差异→本地审核提交生成物→新SHA远端重新生成无diff并测试。不得手改pb.go/sqlc输出、用未提交远端修补通过验收、用旧SHA证据证明新代码。记录命令、退出码、host/cwd、时间、日志/hash和实际资源ID，pipefail不得吞错。

实现遵循现有package biz/data/service；biz无transport/HTTP/pgx/k8s依赖，service只转换。Image独立schema/sqlc/migrations/image/schema_version；保持根Network迁移bytes/checksum、旧RPC、SAN、配置及-migrate语义。Bootstrap仅追加字段。Tenant SQL显式tenant/scope，复合FK及真DB跨租户反例，不复制IAM或Network数据实现。

Governance复用ResourceTenantResolver，保持其Kratos v2与Resource v3各自版本；新Image客户端重建可信metadata，只import固定SHA的生成契约，不local replace。Image租户RPC仅现有受信入口的精确白名单；平台CLI和内部pull材料不得暴露浏览器或未鉴权full入口。平台管理身份仅Image后端使用，用户/工作负载永不拿Harbor admin。

凭证须有有限重试/幂等与Secret加密/脱敏，遵守实际Harbor版本API和子身份权限约束。旧command不能覆盖新代次；未知同名Project不能直接认领；Secret只在限定签发交付/重放窗口返回，普通GET不返回。镜像登记固定Digest，取消登记不删Harbor内容。Namespace/Secret归创建方，只有租户只读凭证，强制正确拉取策略。

真实写入先验证已批准live profile、cluster/CA指纹、Project/Namespace范围、测试tenant与预算。无授权只做代码/隔离验证，不能冒用可见集群。不得更改既有实验密码、关闭TLS/鉴权/扫描/签名或共享集群准入来过测试。仅清理本run且ID/UID/ownership匹配资源，不全局GC、不删共享对象。

每批执行其定向测试；最终保留原verify/integration/race/tenant-mutations并补Image覆盖。失败先复现和修复，不删测试、不降低门禁、不mock替代真Harbor/PG/业务验收。新增脚本/Make目标先实现验证再使用，不能把计划里的名字当成现成工具。

当前进度只追加到Resource docs/execution/status.md的Image区块，历史记录放docs/execution/records/IMAGE-MVP/<run-id>/。每小任务记录code/isolated/live/product状态、源码SHA、证据和blocker。PASS必须实证；blocked/not_run不能变为全部完成。额度/会话将结束前写checkpoint：下一任务、SHA、已发副作用、退出码、下一安全动作；恢复先读状态，不声称会话结束后后台继续。

最终提供review分支/提交、实际改动文件与方法、API/DB合同、各级验收证据、未完成/阻塞和清理结果。禁止自动merge、rebase、amend、force-push或正式发布。现在执行IMG-00，随后按上述规则自动推进。
