# Image MVP 有界交付与续跑入口

本记录交付已完成的后端代码和独立验证，**MVP未完成**。当前状态唯一入口为
[Resource执行账本](../../../status.md#image-mvp)。live profile、普通容器owner/Namespace
绑定、可写租户Console仓库三个输入缺失；没有共享Harbor/集群部署或产品验收。

## 分支与源码

| 仓库 | 基线 / 候选 | 交付状态 |
|---|---|---|
| Resource | 基线 `a4ca2a0fcb18346fff27f682a5244874ca4c60c8`；已验代码 `62ecc67b839dd1040468323f8c316189a0c30653` | `codex/image-mvp-20260930` 已推；后续仅证据/说明文档提交 |
| Governance | 实际基线 `d1a804f1d35d7294cb7eab48ee3f256d9d2482b5`；已验 `d64d6ee478801795afadb4573ba25f8c2de6b7fc` | 同名review分支已推；原checkout的 `_agent/` 保留 |
| Console候选 | 基线 `47233a7279c6ec563ea2842192379bf14546f6e8`；本地准备 `674f2f0b09be0faf2b36ff334f291ad2575e21d7` | 未推、未生成、未验证；[权限阻塞](IMG-09-access-blocker.md) |
| 普通容器owner | 未找到符合范围的非ANI仓库/创建方法/tenant Namespace权威解析 | blocked；不拿KServe或技术Pod代替 |

没有merge/rebase/amend/force-push、正式发布或PR合并。审阅时以候选代码SHA和各日志内
HEAD对齐；文档后续提交不改变所验运行代码。前端准备提交不代表页面已实现。

## 实际实现与审阅顺序

1. 契约/领域：Resource `api/image/v1/{image,runtime}.proto`；`biz/image` 的
   `Lifecycle.EnsureImageSpace/IssuePublisherCredential/ResetPublisherCredential/DisablePublisherCredential`，
   `Catalog.RegisterImage/ListImages/UpdateImage/UnregisterImage`，`Runtime.ResolveImageForWorkload/GetTenantPullMaterial`。
   fixedDigest、显式tenant/scope、CAS、签名cursor、同步幂等command和受限Secret交付。
2. 存储/Provider：`migrations/image/001_image.sql`、`internal/data/image/queries`及生成sqlc；
   独立schema/version/runtime角色。`Harbor` TLS、Project ID绑定、限定Robot模板、制品平台读取，
   `AESGCMKeyring`以安装/租户/空间/用途/代次/command AAD绑定。恢复先存候选，外部调用不在SQL事务内。
3. 平台/装配：`Platform`与独立平台SQL端口；`cmd/.../image_admin.go`固定8动作和新0600输出；
   `image.go/image_migrate.go`显式接线与独立迁移。`runGovernance`仅追加11项租户RPC；
   `internal/server/image_identity.go`重建可信Image caller。disabled/full/vpc-read没有Image公共写入口。
4. Governance：`data/image_client.go`只引入固定Resource生成契约；`service/image_service.go`
   复用 `ResourceTenantResolver`；`image_views.go`有归属核验/安全错误转换。`auth/image_policy.go`
   与HTTP filter处理精确路由、body/headers/AK签名、IMAGE模块及现有权限。
   模块追加14，权限seed只建catalog，不给角色/账号/AK自动授权。
5. 验收辅助：Image真PG runner、3个mutation、13个真实进程退出恢复场景；Harbor技术驱动
   [实现与限制](IMG-07-smoke.md)。技术helper无运行listener，不能被解释为已接通业务owner。

Resource源码与Network分层保留在同一repo/module/process；biz无Kratos/protobuf/HTTP/pgx/k8s依赖。
没有把用户API租户身份、Harbor admin或namespace透传为业务参数。

## API 与数据库合同

HTTP路由/11权限、字段限制、错误与重试的唯一正式合同见
[API规格](../../../../specs/image-api.md)和实际Proto；当前Governance公开DTO的 `image_id`
保持snake_case，其他JSON按生成OpenAPI lowerCamel，int64版本/代次为JSON字符串，scope、
purposes、accelerator为小写字符串。公开DTO无tenant/Project ID/Namespace/admin/pull Secret。
内部两个Runtime契约已实现adapter但未注册独立listener，待真实owner部署决定同进程端口或独立mTLS。
Governance `go.mod` 固定Resource `v0.0.0-20260929185545-71aa986078df`；后续平台CLI/驱动
没有改变这份生成契约。两仓各自保留Kratos v2/v3，无local replace或internal跨仓依赖。

[数据契约](../../../../specs/image-data.md)及实际迁移定义四张业务表
`image.spaces/credentials/registrations/commands` 和独立 `image.schema_version`。
租户行必须非空tenant；平台行仅显式platform scope且tenant为空，通过复合FK与scope检查
关联正确空间。没有RLS；runtime不是owner/超级用户/BYPASSRLS，不得DDL或读取Network域。
Root Network迁移bytes/checksum、旧RPC/SAN/迁移语义保留；Image迁移显式owner执行，不随服务启动。

## 证据边界与剩余门禁

- [IMG-01](IMG-01.md)、[02](IMG-02.md)、[03](IMG-03.md)、[04](IMG-04.md)、[05](IMG-05.md)：
  契约/纯规则、真PG、协议与密文、持久化/进程恢复、固定Digest目录。早期SHA证据只支撑当时切片；
  当前Resource全套Image用例和verify在62ecc67重跑通过。
- [IMG-06 Resource](IMG-06-resource.md)：真实TLS/gRPC与旧Network回归；
  [Governance](IMG-06-governance.md)：真实PG/Redis/HTTP/JWT/AK/Casbin/mTLS，Resource端使用协议peer。
  不是实版Harbor或已部署登录/浏览器验收。
- Governance默认优化build与回归pass。默认优化race编译在2300M预算OOM；仅生成Ent包关闭内联后
  保留所有race插桩/断言的定向和真实HTTP race通过。原失败保留，不能称默认优化race已通过。
- [IMG-07平台](IMG-07-platform.md)和[驱动](IMG-07-smoke.md)支持code/isolated门禁。
  actual Harbor版本、管理权限/Token TTL、TLS节点信任、A/B真实registry、缓存负例仍blocked。
- 原始Network完整integration/race/tenant-mutations正在独立执行，结果归档后补充链接；
  它们不会将live/product自动升级为pass。GitHub CI没有作为本次已验证证据。

## 续跑与恢复

1. live：由有权操作该环境的人提供已批准profile，固定cluster API/CA/UID、Harbor版本/CA与
   精确Project范围、两个tenant/账号、base digest、预算及有限清理授权。私密值只用文件引用。
   按[部署/驱动手册](../../../../../deployments/image/README.md)预检；无profile不得探用可见集群。
2. owner：提供实际非ANI普通容器仓库/SHA/Create方法和tenant Namespace权威解析。
   再实现IMG-08的受信端口、固定意图持久化、唯一Namespace Secret generation/ownership及Always。
   运行pull凭证受控更换尚未接通，不能用手工改DB/Harbor密码代替owner同步。
3. 前端：为当前身份开放候选仓write权限或明确另一个可写目标。先核对目标与现有本地准备提交，
   push后才在Fedora导入OpenAPI/生成类型，实现列表/过滤/受控凭证弹窗/登记维护并跑原npm verify。
   真实容器选择依赖owner绑定，不新造替代创建页。
4. 所有恢复继续同一个Source-first循环和Fedora既有重锁/预算；检查待定command和实际资源ID后
   才重试，不通过换key认领不明对象。原失败日志不能被覆盖成后续pass。

## 资源与清理

当前Image/Governance隔离PG/Redis fixture均已按实际ID/ownership清理，详情在各批日志。
本次没有共享Harbor/集群写入，无需删除共享Project/Namespace/Pod。
Fedora run根、源码副本、缓存、生成返回包和脱敏证据保留，供复核/恢复；不是正在部署的业务服务。
正在运行的原Network回归fixture待其trap和独立ID核验后再记清理完成。
本地Resource review分支、Governance/Console独立worktree均保留；不清理原仓无关文件。
