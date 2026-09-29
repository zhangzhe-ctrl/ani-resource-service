# 验收条目与证据映射（固定源码观察）

本文件将[验收矩阵](../../../../specs/image-acceptance.md)映射到已经取得的证据，
不是另一份当前状态表。Resource代码 `62ecc67b839dd1040468323f8c316189a0c30653`，
Governance代码 `d64d6ee478801795afadb4573ba25f8c2de6b7fc`。
Resource当前Image全套日志见[IMG-07](IMG-07-smoke.md)，Governance日志和编译限制见
[IMG-06](IMG-06-governance.md)。表中“隔离支持”不将该项要求的live/product自动视为完成。

| 验收条目 | 已取得的源码/执行证据 | 仍需真实环境或owner的部分 |
|---|---|---|
| A01 disabled | `TestImageDisabledAndPrivateFiles`、`TestImageConfigurationAdmission`；旧保护路径diff | 无Image live启用记录 |
| A02 caller/证书 | `TestGovernanceMTLSBoundary`、`TestImageGovernanceAllowlistAndScope`，实际TLS/gRPC负例 | 现网证书绑定未提供 |
| A03 JWT/AK/租户 | `TestImageJointHTTP` 真PG/Redis/权限/HTTP/mTLS；测试身份由真实authenticator/cache签发 | 实际登录流程、测试用户及部署映射未联调 |
| A04 tenant mutation | `TestTenantQueriesAndAtomicCatalog`、复合FK用例，删除tenant谓词后断言失败 | 真PG证据已取得；不是Registry授权证明 |
| A05 platform关系 | `TestTenantCompositeFKAndPlatformFK`，平台写tenant mutation被发现 | 真PG证据已取得 |
| A06 role/readiness | `TestRuntimeRoleAndMigration`，权限/RLS错误拒绝 | 部署数据库角色未创建/验证 |
| A07 migration | 同一测试验证首建/重复/校验错误；root Network checksum独立核对 | 未执行共享数据库迁移 |
| A08 slug竞争 | `TestConcurrentSpaceSlug`、`TestLifecycleTwoProcessesSameCommands` | 实Harbor并发结果未测 |
| A09 半失败恢复 | `TestLifecycleActualProcessExitRecovery` 13场景，实际子进程退出73后恢复0；阶段故障用例 | Provider为协议fixture，真实故障注入未执行 |
| A10 未知Project | `TestLifecycleProjectOwnershipRecovery`、`TestPlatformLostProjectAndRobotRecovery` | 实际归属证据及人工恢复未发生 |
| A11 管理权限 | 有限Robot权限契约与adapter入参校验 | 实际Harbor版本/父身份委派能力未确认 |
| A12 publisher/pull范围 | `TestRestrictedRobotPermissions`、平台/租户生命周期断言；smoke实现正向及跨tenant读负例 | 真实只读Push拒绝、platform写拒绝等完整Registry矩阵未执行 |
| A13 完整用户名 | `TestHarborRobotOwnershipAndWireContract` 使用自定义prefix及provider返回用户名 | 实际Robot登录未执行 |
| A14 密文/脱敏 | `TestCipherWrongAADTamperKeyMissing`、`TestSecretRedactionAndErrorReason`、`TestImageRequestAndCredentialReplyNotLogged`；真PG无明文断言 | 已取得隔离证据；不宣称备份物理擦除 |
| A15 交付重放 | `TestLifecycleEnableIssueResetDisableReplay`，actor/key/version/窗口；平台对应断言 | 已取得隔离证据 |
| A16 迟到请求 | `TestStaleGenerationCannotActivateOrReplay`；reset实际进程恢复场景 | 已取得隔离证据 |
| A17 停用/Token TTL | 受控Harbor协议断言旧Robot停用；API metadata状态/CAS/重放 | 实际Token TTL/新登录及已签Token行为未测 |
| A18 ref安全 | `TestValidationMatrix`、Harbor TLS/重定向边界 | 已取得隔离证据 |
| A19 可运行制品 | `TestHarborArtifactDigestAndPlatforms` manifest/index/附件/深度/错误平台 | 实版Harbor返回和目标节点架构未联调 |
| A20 固定Digest | `TestCatalogUseCasesAndRuntimeIsolation`、平台登记重放，受控tag变化保持原快照 | 真实重推tag及已接受业务意图未测 |
| A21 filter/cursor | `TestCatalogCASAndPagination`、`TestCursorTenantScopeFilterAndSignature` | 真PG/隔离证据已取得 |
| A22 CAS | 真PG目录/平台CAS、固定字段/跨租户拒绝 | 真PG证据已取得 |
| A23 unregister | 用例断言取消不调用Provider删除、后续Resolve拒绝 | 实Harbor内容保留及已运行业务未测 |
| A24 Harbor故障 | `TestHarborTransportBoundary` 状态码/超时/大小/重定向；持久化阶段故障 | 真实故障期间已有业务行为未测 |
| A25 内部runtime | 公共/租户白名单拒绝内部两RPC，`Runtime`真PG归属/拉取材料测试 | 未绑定owner，因此没有内部listener/证书接入证明 |
| A26 Namespace/Secret | 没有创建方实现或K8s写入 | owner/Namespace权威解析与live profile缺失 |
| A27 产品创建 | 没有产品创建记录；用户明确没有测试条件，普通容器对接先阻塞 | 待后续具备测试条件；技术helper/Pod不算该项 |
| A28 runtime imageID | 根Digest解析用例只覆盖用例层 | 实际Pod spec/imageID子manifest语义未测 |
| A29 节点缓存/其他入口 | 没有节点/准入写入 | 共享节点缓存及其他Pod入口边界未测 |
| A30 UI | 用户于2026-09-30明确前端尚未确定，保留IMG-09 blocked；本次不做对接 | 待用户确定前端仓库；既有本地准备未发布，不能计pass或n_a |
| A31 全量门禁 | Image定向/真PG/race/3mutation/verify通过；原Network integration/race/6mutation通过，见[精确代码记录](IMG-10-regression.md) | Governance默认优化race仍有OOM限制；不能声称该配置全绿 |
| A32 cleanup | Image/Governance及原Network隔离fixture删除日志、ID/ownership及最终不存在检查 | 没有live资源创建；不能用空清单声称真实清理流程已验收 |

Image进程测试中的helper仅在子进程带明确场景输入时执行，其父进程入口不是一个live用例。
常规 `make verify` 的build-tag默认集合不能替代 `scripts/image-integration -v -race`；后者
已单独真实执行并归档。CLI/HTTP协议fixture及浏览器准备均不能替代后续真实Harbor/产品验收。
