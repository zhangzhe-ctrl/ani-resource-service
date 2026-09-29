# 来源、当前事实与核验限制

访问日期：2026-09-30。以下是设计依据，不是现网可用性证据。仓库版本号、源码方法是读取事实；本包新增 Image 方法、字段和目录是提议。

## Resource：固定 SHA a4ca2a0fcb18346fff27f682a5244874ca4c60c8

基准：`https://github.com/zhangzhe-ctrl/ani-resource-service/tree/a4ca2a0fcb18346fff27f682a5244874ca4c60c8`

| 编号 | 已读文件 | 对设计的约束 |
|---|---|---|
| R1 | `AGENTS.md`, `docs/START-HERE.md` | Kratos 顶层分层、领域自治、composition root、唯一进度账本 |
| R2 | `docs/adr/0002-use-tenant-owned-data-without-rls.md` | sqlc/pgx、显式租户条件、复合 FK、不使用 RLS，不建立 IAM 外键 |
| R3 | `docs/adr/0006-evolve-resource-service-preserving-network.md` | 同仓同 module 同进程，保留 Network 契约/数据/身份，跨域窄端口 |
| R4 | `internal/data/network/postgres.go` | 运行角色检查；根 migrations 的 SHA256 和 `network_schema_version` 精确校验 |
| R5 | `internal/biz/network/network.go`, `internal/service/network/network.go` | plain domain types，依赖注入，service 仅转换，ErrorInfo 错误风格 |
| R6 | `sqlc.yaml`, `Makefile`, `scripts/verify-source`, `scripts/verify-boundaries` | 保留工具版本和真实边界/生成门禁；Image 单独 sqlc 项 |
| R7 | `internal/server/governance.go`, `cmd/ani-resource-service/governance.go` | mTLS + 精确方法白名单；资源端 legacy SAN 仍是 ani-network-service；Image 必须显式接入 |
| R8 | `cmd/ani-resource-service/main.go`, `internal/conf/v1/conf.proto`, `go.mod` | Resource Kratos v3、Go 1.26.7、Bootstrap 新字段不得改旧 tag；-migrate 现为 Network |
| R9 | `docs/remote-execution.md` | 历史重锁、Fedora 预算、源码清单、生成物回传；本次用户规则覆盖旧 Ubuntu/本地回退条款 |

## Governance：固定 SHA b7c249e4b420cbab78a2e8979eab874aba5ffe82

基准：`https://github.com/zhangzhe-ctrl/ani-governance/tree/b7c249e4b420cbab78a2e8979eab874aba5ffe82`

| 编号 | 已读文件 | 对设计的约束 |
|---|---|---|
| G1 | `app/admin/service/internal/service/network_service.go` | ResourceTenantResolver：uint32 租户转资源 UUID；Principal/Actor 来自可信上下文；响应也校验归属 |
| G2 | `app/admin/service/internal/data/network_client.go` | mTLS 下游客户端；Image 新调用使用 NewOutgoingContext，不照搬 AppendToOutgoingContext 造成身份重复的风险 |
| G3 | `api/protos/admin/service/v1/i_network.proto`, `api/buf.network.gen.yaml` | 对外 HTTP 在 Governance；catalog DTO + admin i_*.proto；定向生成 |
| G4 | `api/protos/identity/service/v1/module.proto` | 当前最大模块枚举 ACCELERATOR=13，建议追加 IMAGE=14，实施时检查冲突 |
| G5 | `go.mod` | module go-wind-admin、Kratos v2；不用 Resource v3 改造整个 Governance |

`ani-inference-service/README.md` 还表明其新推理代次使用 KServe，不能当作已经确认的普通容器创建方。该 README 读取默认分支，仅用于排除未经核验的替代假设，不作为本包实现基线。

## 主源协议参考

- H1 系统 Robot 及范围：`https://goharbor.io/docs/2.15.0/administration/robot-accounts/`
- H2 API：`https://goharbor.io/docs/2.15.0/working-with-projects/using-api-explorer/`
- H3 上游 OpenAPI：`https://github.com/goharbor/harbor/blob/v2.15.0/api/v2.0/swagger.yaml`
- H4 Robot 实际处理代码：`https://github.com/goharbor/harbor/blob/v2.15.0/src/server/v2.0/handler/robot.go`。创建时随机 Secret；RefreshSec 可提交指定 Secret；机器人创建子身份时检查权限收窄。
- K1 私库 Secret：`https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/`
- K2 镜像/Digest/拉取策略：`https://kubernetes.io/docs/concepts/containers/images/`
- K3 准入：`https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/`

实际 Harbor 对应接口、管理身份委派规则、Token 有效期、镜像安全门禁、集群身份、TLS/节点信任均在 IMG-00/03/09 验证。阅读官方文档不等于验证用户运行环境。
