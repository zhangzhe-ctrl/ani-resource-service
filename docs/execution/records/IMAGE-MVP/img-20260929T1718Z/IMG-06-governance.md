# IMG-06 Governance：合同、真实 HTTP 和有界 race 验证

固定源码 `d64d6ee478801795afadb4573ba25f8c2de6b7fc` 已推送至 Governance
`codex/image-mvp-20260930`。本地隔离工作树
`/home/chabking/workspace/.worktrees/governance-image-mvp-20260930`，原 dirty 工作树未修改。
Resource 契约固定为 `71aa986078dfb64388d783a7b2de01b7a94b0027`，module
`v0.0.0-20260929185545-71aa986078df`，没有 local replace 或框架迁移。

## 实现落点

| 小任务 | 实际文件/方法 | 结果与范围 |
|---|---|---|
| 06.3 | `app/admin/service/internal/data/image_client.go`：ImageConfigFromEnv/NewImageClient/trusted call/11方法 | TLS1.3、固定服务身份、可信 metadata 重建、超时上限；只 import 固定 Resource 生成契约 |
| 06.4 | `internal/service/image_service.go`、`image_views.go`：NewImageService/11 BFF方法/响应校验 | 复用 ResourceTenantResolver；当前 Principal actor；scope/tenant/ID/digest/凭证交付校验失败即拒绝；公开响应无内部tenant |
| 06.5 | `pkg/middleware/auth/image_policy.go`、auth/logging、permission/tenant_access、REST/DI、Module/Ent、定向生成脚本 | 精确11路由权限；IMAGE=14，旧编号保留；AK按原始body摘要及canonical query签名；no-store/日志脱敏；权限seed不自动授权角色 |
| 06.6 | `internal/service/image_joint_http_test.go`、`image_joint_fixture_test.go`；`scripts/image-joint-integration` | 实际 HTTP、PG、Redis、JWT/AK、Casbin、套餐检查、租户映射、mTLS；Resource下游是明确的协议fixture |

路径中的 `internal/service` 等相对于 Governance `app/admin/service`。
定向生成还运行现有 OpenAPI AK 201 后处理；`image_id` 显式 JSON name 使 HTTP
router、权限 catalog、OpenAPI 路径完全一致。合同测试核对全部11项及 int64 JSON 字符串。

SQL catalog seed 位于 Governance `sql/data/20260930_image_permissions.sql`，先要求
OpenAPI catalog 有且只有对应有效 IMAGE API，再幂等创建精确权限关系；保留既有禁用状态、
拒绝其他权限绑定，独立执行不创建 RolePermission。联合测试先断言零角色授权，再只给
自有测试角色显式授予读/发布权限。没有给真实套餐、角色、用户或 API Key 修改权限。

## 精确 SHA 的验证分层

全部执行在 Fedora 本 run，持原 `net05a-heavy.lock`；systemd
CPUQuota=200%、MemoryMax=2300M、MemorySwapMax=0。PG 单独768 MiB/1 CPU，
Redis128 MiB/0.5 CPU，无 swap、随机 loopback 端口、私有随机密码。
所有流水线使用 `set -Eeuo pipefail`；OOM 的 SSH exit 141 明确保留为未完成。

| 检查 | 结果 | 原始证据 |
|---|---|---|
| Buf/Ent 重生成 clean；go mod tidy -diff | pass | [serial完整日志](governance/evidence/gov-d64d6ee478801795afadb4573ba25f8c2de6b7fc-verify-serial/output.txt) |
| imagecontract/sql-bootstrap/auth/logging/constants/rpc | pass | 同上 |
| 原有完整 data/service/server 回归，含真实 Redis Asynq lifecycle | pass | 同上；9.310s/23.760s/0.700s |
| 默认优化的 server/admin build | pass | [c1日志](governance/evidence/gov-d64d6ee478801795afadb4573ba25f8c2de6b7fc-verify-c1/output.txt)；runner先build再HTTP |
| 真 PG/Redis 上 HTTP/JWT/AK/Casbin/mTLS，非race | pass | 同上；TestImageJointHTTP 0.69s |
| 默认优化 race 编译 | fail/environment OOM；测试未完成 | [supervisor](governance/supervisor.txt)；三轮exit141，非断言/race报告 |
| 生成 Ent 包关闭内联优化后的完整 race 插桩，定向data/service/auth | pass | [race日志](governance/evidence/gov-d64d6ee478801795afadb4573ba25f8c2de6b7fc-verify-race-noinline/output.txt) |
| 相同编译参数下真实 HTTP/PG/Redis/mTLS race | pass | 同上；TestImageJointHTTP 2.77s，包3.881s；exit0 |
| 真实 Harbor、共享集群、浏览器/普通容器产品闭环 | blocked/not_verified | 没有批准live profile及真实非ANI容器owner/Namespace绑定 |
| GitHub CI | not_verified | Fedora结果不代表GitHub CI |

最后一次 race 的确切额外参数为
`GOFLAGS='-p=1 -trimpath -gcflags=go-wind-admin/app/admin/service/internal/data/ent=-l'`，
`GOMAXPROCS=1 GOMEMLIMIT=768MiB GOGC=50`。只关闭生成 Ent 包的内联优化，
没有移除任何包的 race 插桩、跳过断言或增加内存预算。普通优化 build 已另行通过；
默认优化 race 在当前预算下仍未完成，不能把本次结果改写成该配置通过。
最后一轮2026-09-29T20:24:07Z—20:32:27Z，scope `run-p830669-i9206543.scope`，
exit0。三次 OOM 与之前生成/fixture 失败的逐项恢复见
[尝试记录](IMG-06-governance-attempts.md)。

HTTP 用例实际覆盖：11条允许路由，租户/平台目录读取，两个租户各自映射，JWT/AK只读写入
拒绝，发布身份写入，重复/伪造身份头及body拒绝，签名body被改拒绝，混合认证拒绝，
错误下游tenant和错误SAN拒绝，角色撤销对JWT/AK即时生效，AK禁用及套餐模块移除拒绝。
凭证响应及错误带no-store，GET/错误无Secret，审计body/referer及认证信息脱敏。
JWT签发使用真实 authenticator/cache 的测试夹具，不声称登录入口或浏览器会话已联调。

## 资源、日志与回传

最后HTTP race fixture PG `149a8af3bd9a0ac393abc250849ae1910566777c5820b88ee5c047559b6f569c`、
Redis `db9defd1325e702a2ea4cdba4fcea09becb83513118edb22e328a80103c5de30` 已按实际ID和
ownership删除。非race fixture PG `2ab022c1164e5ca789265a2ad3b83e757959572ecd5f2df18a8cc7abcf89ed4a`、
Redis `72454f0574a6b0b96f4bb14feb6ef887fb3f25a43a69ef7ad5eee2c99505524c` 同样清理。
前序回归容器删除记录均在相应日志；最终[fixture inventory](governance/fixture-inventory.txt)为空。
共享Harbor/集群没有写入，也没有更换既有密码或关闭鉴权/TLS。

[SHA256SUMS](governance/SHA256SUMS)覆盖37项归档中的36个原始日志、返回清单、runner及
supervisor/inventory；返回tar SHA256为
`63681845e15b073225abad521207f407bb7a98c4f7911ff07bd12231b26c0942`，转运时逐文件校验。
最终race日志SHA256为 `4e84b8be1ed656817886472d95edbe0aaf4d8601136ab9bd846882e42bd30362`。
生成返回的manifest保留完整源SHA/base SHA256/目标SHA256；没有回传密码或私有DSN。

本阶段 code 与上述有界 isolated 验证支持继续独立 CLI/目录页工作，不等于MVP产品完成。
