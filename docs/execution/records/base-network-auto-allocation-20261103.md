# 基础网络自动分配与 LB 产品主链（2026-11-03）

当前状态只见[执行状态](../status.md#base-network-auto-allocation-20261103)。业务合同见[基础连接规格](../../specs/vpc-connectivity-lb.md)与[平台运行前提](../../../deployments/egress/intranet.md)。

## 实施与固定输入

Resource 延续 PR #46 的 `cba0bcb`，本批业务源码提交 `1ddb5c6`；Console 延续 PR #10 的 `1e032e4`，新增只读摘要提交 `c0765b6`；BOSS 从实际最新 main `9f917a4` 创建独立分支，最终提交 `02dc75b`；Governance 从 main `316ee2e` 创建配套分支，提交 `566c3b2`。原有 dirty 主工作区及用户 43927 预览保留。

Resource 新增 `GetIntranetPoolDefaults` 和 `SetIntranetPoolAutoAllocationEnabled`；平台身份来自既有 mTLS 委托，池版本与永久幂等复用既有接受记录。自动开启在一个事务设置默认池、池开放及新 VPC rollout；关闭只允许当前默认源并保留池开放和现有资源。默认源切换在开启时校验并开放目标池，不移动已受理子资源。Intranet 池新增只读 `new_vpcs_enabled`，只有当前启用的默认源为 true。

迁移 0027 删除人工 Intranet verification 分配约束，并在既有平台操作类型 CHECK 中加入新命令；不改已应用迁移。准入与运行 adapter 均改为依赖真实 Provider 当前事实，不再以人工证据作为 Intranet EIP/SNAT 前置。创建表单默认身份及路由来自只读 Provider 查询，实际创建仍核对冻结的 UID、控制器默认 router、路由覆盖与 Ready。

## Fedora 检查

独占目录 `/home/chabking/tasks/base-network-20261103/resource`。初始输入 8542 文件，归档 SHA-256 `fb173cf7d799aae5db656efe0ef70bcc855c4f2ab08999c201b84ea1bb328a24`；后续精确差异保留于本 Git 提交及远端源码。Go 1.26.10，Buf 1.60.0，sqlc 1.31.1；GOMAXPROCS=2、GOFLAGS=-p=2。生成、格式化、数据库、自动化测试、镜像构建、API 驱动与集群操作均在 Fedora 执行；本机仅编码/Git/转运及正式页面轻量浏览器交互。

- `make config`：PASS；生成后网络 API 与正式输入同步。
- `go test -count=1 ./internal/biz/network ./internal/service/network ./internal/server`：PASS，包含所有平台方法的受信身份检查。
- `scripts/integration ./internal/data/network -run '^Test(Intranet|BaseConnectivity|BaseConcurrentWorkers)' -v -timeout=15m`：PASS，真实独占 PostgreSQL，运行 116.650 秒；覆盖原子默认/分配/开关、无人工 verification、永久重放/冲突/旧版本、切换旧源误关闭拒绝、真实默认身份/路由拒绝，以及新建 VPC→基础 EIP/SNAT→并发/未知结果恢复/删除。
- 最终 `make verify`：PASS，包括锁定生成一致性、分层故障检查、依赖 tidy diff、全 Go 单元检查、vet、build、模块校验与 diff 检查。

原始失败未覆盖：首次新增开关 PG 失败定位为平台操作类型 CHECK 未登记，已在 0027 修复；真实 VPC fixture 去除人工证据后发现 adapter 残余 verification gate，已移除并由同一真实生命周期复验通过。最初扩大至旧 backfill 测试时，它们在旧 VPC fixture 创建阶段因 `tenant namespace reader unavailable` 失败；该历史补齐路径不在本次实施范围，不修改断言或记为通过。具体日志为远端 `evidence/{postgres-tests-first-failed,postgres-tests-with-legacy-fixture-failures,postgres-tests-adapter-gate-failed,postgres-tests,make-verify}.log`。

配套仓库均在各自独占 Fedora 源码目录执行生成/必要检查和原生构建：Governance `make api`、新增平台方法/委托/错误与 VPC 摘要的定向行为测试、data/auth vet PASS，SDK 固定为 `v0.0.0-20261102171245-1ddb5c61eb17`；整个 Service vet 仍有未修改的 `vm_list_filter_test.go` mutex copy 及 tenant-space 测试 unkeyed literal 失败，未宣称全仓检查通过。BOSS/Console 的 lint、format、类型及 production build PASS；BOSS 真实 Hook/TanStack/API 的12场景受控 HTTP 边界验证、Console 摘要组件/既有 LB Hook 验证及最终依赖图检查 PASS。未为了末尾仅更新文档而重复已经通过的生成/构建/测试。

## 发布与业务验收

目标为 `kubernetes-admin@ani-lab`，API `https://172.16.101.10:6443`，kube-system UID `5277649d-e28d-4a0a-ae76-8354365c63bc`。依次完成 Resource→Governance→BOSS/Console 发布；四套 Deployment 均核对 UID、实际 imageID、observedGeneration 和 Ready，正式前端 HTTP/bundle 与 Pod 文件哈希一致。镜像仓库前缀均为 `172.16.101.10:30003/icn-01a15337-release/`：

| 服务 | 业务源码 | 实际镜像 digest |
| --- | --- | --- |
| Resource | `1ddb5c6` | `ani-resource-service@sha256:ab3d779d6abcf0259666f460a6ea26f93a1f9a500a81ae80db79377864e2e693` |
| Governance | `566c3b2` | `ani-governance@sha256:12e40190f94008fb6cad5119489e27bad4613a6376ba87d01d93cf8ebef49009` |
| BOSS | `02dc75b` | `eight-boss@sha256:ae1ae8a666760258ba3c2569914738e04908fdc0a606b250a62032f3270273fa` |
| Console | `c0765b6` | `eight-console@sha256:83acd3b15db32ca249abbd25402c19cd31b12e7e7c6515a6e38a5386aa1a9f72` |

Resource 原生构建二进制与镜像 `/app/bin/server` 字节一致，SHA-256 `567b5d29bbaebed2f0a16d865cb357a441c4c992820c88458ec567c074a29f76`。正式 Resource 配置仅扩充已验证不冲突的 VPC CIDR 预设，保留原 `10.73.0.0/16`，增加 `10.75.0.0/16`～`10.78.0.0/16`；未改 Provider 门禁。完整部署回执位于 Fedora `resource/deployment/evidence/publication-result.json`、`governance-artifacts/delivery-receipt.json` 和 `/home/chabking/tasks/base-network-frontend-publish-20261103/`。

### 数据库与发布修复

原生 Network 0026→0027 迁移 PASS；原 26 项 checksum 保留，新增迁移仅调整上述两项 CHECK。执行前 `pg_dump -Fc` 及 `pg_restore --list` 均 PASS，备份 SHA-256 `bd76ca2283efaa46eed66066afa057ec54db8b1a44dbcd8b67195380c20c1b28`。旧严格 schema 镜像不能直接作为 0027 数据库的回退方案，保留原部署与备份。

Governance 业务库同样完成完整 custom backup 及 TOC 校验，SHA-256 `53289bea4d202b9617437919ebb1acfd7538018c88cc1139aa3fb2dcd1b197cc`；原生 catalog 同步与本批最小权限增量已执行。首次发布遗漏主干已存在的乱序 Atlas 迁移 `20261030010000 tenant_space_intent`，导致 `sys_tenants.tenant_space_state` 缺失、正常租户登录 401 和租户查询 500；授权路径的旧 403 也是该查询错误，并非 Instance 权限不足。按仓库正式部署流程通过原生 Atlas `apply 1 --exec-order non-linear` 补齐唯一缺失迁移，最终 18 项已执行、pending 0。未改密码、账号或 Compute 权限；fresh 正常租户登录、平台租户列表、initial-context 和 VPC 列表均恢复 200。证据为 `governance-artifacts/{auth-schema-stage.log,auth-postmigration.json,delivery-receipt.json}`，保留原失败，不将初次发布记为成功。

### 已完成的真实验收

- 自动分配缺省关闭：正常租户新建 `vpc_bac2c2741de546f3be444ac2455f5ec4`（`10.78.0.0/16`），同键重放返回相同 VPC/operation，SQL 恰好一组受理，基础连接/EIP/SNAT 均 0。真实 KC VPC UID `4061e790-36fa-4eb0-a8a3-e544fc619bb6` 达到 Ready；正式 DELETE 后 operation succeeded、物理 VPC 消失、活动 CIDR/operation/pending binding 均 0。原 7 个 KC VPC UID 保留。证据 `vpc-off-acceptance/result.json`。
- 管理权限：正常租户向新增 defaults GET 和合法 auto-allocation PUT 均得到 Gov 403，两个 request ID 及真实拒绝日志保留；按 Gov 实际时间校正 Fedora 约61秒时差后，Resource 对应窗口两个 RPC 调用均0，前后 SQL 的目标池/operation/幂等记录均0，默认源与 rollout 未变。平台 root 的 list200/defaults503 已通过认证授权、后者仅在 Provider 门禁失败。证据 `platform-put-permission/result.json`；未创建测试账号、API Key 或扩大授权。
- 正式 Console 空后端创建：`lb_9d66811e2c954d23b4e34a5c9f354fd5`、private/small、VIP `10.73.0.101:8081`。页面显示等待后端；target/applied 均 0，标签选择器核查本 LB 的 Gateway/Proxy/Backend/Route/Policy 均 0。配置更新重放返回同一 operation，旧版本请求返回 412 `VERSION_CONFLICT`；浏览器旧表单保存后监听端口仍为 8081，未覆盖并发描述更新。
- 纯数据库 LB 删除与 VIP 产品占用释放：两个专门空后端样本依次使用同一 `10.73.0.102`，每次正式 DELETE 均 succeeded/deleted，第二次正常受理证明第一组占用已释放。该结果仅证明未下发分支，不冒充运行中数据面清理。证据 `live-evidence/lb-empty-delete-summary.json`。
- 正常 Image→Instance→Attachment 真实业务后端：首次现有官方 Nginx 在既有 UID1000/drop ALL 下因缓存目录权限失败；未降低安全上下文。通过正式租户镜像发布/注册兼容静态 HTTP 镜像 `img_07d36296f16b4fbc96af3cded04a6a4c`，digest `0a0041e80d18aabb8747ae0808440a7aeee04ab8d7d4106fee1cbe3125169919`；正常 Instance `41330fcd-f726-4c99-b5aa-3ac488c2da8e` running、operation completed，Attachment `att_b4905919fee8414882324f769521e92d` attached，真实 Pod UID/GID1000、Ready、restart0，同 VPC `10.73.0.15:8080` 返回 HTTP200/`base-lb-http-uid1000-20261103`。
- 正式 Console 添加后端并激活 private/small：member `848e5235-bb16-497e-bb52-bd9b41226261` 绑定上述 Attachment，专属 Proxy、Gateway、Backend、Route、Policy 正常下发，Gateway UID `5faed532-b96e-4b74-b99b-2c11f43cf068`、Programmed True、2/2 Envoy 可用；desired/target/applied=3、available/configured。同一正常业务实例访问 `10.73.0.101:8081` 实测 HTTP200 与正确 marker。此 VPC 使用此前保留的手工基础 SNAT，不能记为新 VPC 自动分配主链通过。
- 正式 Console 权重 1→3：desired/target/applied=4、available/configured，实际 Route configuration4、weight3、Accepted/ResolvedRefs 与当前代次一致；Gateway UID、member ID 和 Attachment 均保持，VIP 仍 HTTP200。随后在正式页面清空最后成员，VIP 不再转发、业务后端仍200；等待原生 360 秒 Envoy 退出宽限期后，desired/target/applied=5、等待后端，全部自有 CR 和生成 Deployment/RS/Pod/Service/EndpointSlice 匹配0。未强删 Pod 或改变 grace。
- 为顺序验证中型容量，经正式平台 API 新建本次专用 CPU preset `a66ffe3d-e5ec-49a2-93ee-560f019ed04f`（100m/128Mi），正常租户创建同镜像 HTTP Instance `fb7502b6-3acd-4e33-aeb4-416ba4096ebf`、Attachment `att_8422c9ba45e7477999fa0cfceeb7504b`，`10.73.0.18:8080` 实测200/正确 marker。沿正常产品删除本次失败 Nginx 和先前 1CPU HTTP 测试实例；不改旧 preset、平台请求量或 LB 固定规格。最终保留小规格后端用于主 LB 再激活，原1CPU后端仅作为上述初次流量验证历史。
- 私网 medium 专用样本 `lb_e7ab1ebb1af145b3855a85e2a02ce947`：同键创建重放同 LB/operation；真实4/4 Envoy Ready，每副本 requests2CPU/2Gi、limits4CPU/4Gi，额外 shutdown manager10m/32Mi；未改规格或调度配置。desired/target/applied=1、available，正式后端访问 `10.73.0.103:8083` HTTP200/正确 marker。通过正常 DELETE operation `e4602961-35c4-400a-b1b8-17712a48abeb` 删除后，operation succeeded、API deleted/reason空，旧 Gateway 及全部自有 CR、生成 Deployment/RS/4Pods/Service/EndpointSlice 匹配0，VIP103不再转发、业务后端仍200。SQL 核对 `vip_occupied_revision=89115246`、后续 `vip_absence_revision=89126006`，同 VIP 活动产品占用、active component/generated 均0，容量释放；完整证据见 `lb-backend/evidence/empty-medium/`。
- 正式 Console 再添加新后端后，主 small desired/target/applied=6、available/configured，真实2/2副本保持每 Envoy requests1CPU/1Gi、limits2CPU/2Gi；VIP101:8081再次返回 HTTP200/正确 marker。新 Gateway `lb-065406b03c474924ba3b1fff6e240476` UID `b6a4ea53-32e4-4dbd-a659-7cd5c2bd51e7` 不同于旧 UID，新 member `7a7349e1-08b6-47e2-929b-becc664896e4` 不复用旧 member，绑定当前 Attachment、weight3。正式页面刷新后可见 available/已配置与新成员。证据 `lb-backend/evidence/reactivated-small/result.json`；保留供用户复核。

### 环境阻塞与保护范围

BOSS 正式 `http://172.16.101.10:30082/basic-networks` 浏览器已登录并填写新建表单，但 defaults 查询 503 `BASE_CONNECTIVITY_NOT_READY`，提交按钮禁用，未发池创建 POST。默认 VPC UID/Ready/代次、默认路由和 Service CIDR 覆盖都通过；现有 KCN `providerImages` 健康门禁因一个 Pending controller、一个自 10 月 30 日删除中的 ani-03 controller 返回缺失。ani-03 自该日 NotReady；既有 Fedora/本机 SSH key 均被拒，任务尚无可用节点登录方式。未更改 KCN 控制器、默认 VPC 或放宽门禁。开启自动分配、默认源切换、无人工 verification 的新 VPC 真实自动 EIP/SNAT，以及其删除仍待该环境前提恢复；真实 PG/adapter 行为测试与现场验收分别报告。

产品公网池 `pool_1c5c2bebf286440fa7ed5006c2256021` 仍 degraded/`PROVIDER_UNAVAILABLE`、观测过期，租户产品 EIP 列表为空。仅一次正常 CreateEIP 在 `poolReady` 受理检查返回 503 `PUBLIC_EGRESS_NOT_READY`，request ID `f228d641-dcbc-49ac-b0cc-a6e90eb22cf5`，未持久化 EIP 或调用 Provider 创建。产品公网/双模式 LB 真实流量未运行；原手工公网 `.194` 流量试验不替代本次产品闭环。证据 `public-lb-prerequisite/result.json`。

large 固定6副本，每 Envoy requests4CPU/4Gi、limits8CPU/8Gi，另含 shutdown manager10m/32Mi，合计24.06CPU/24.1875Gi。最终样本恢复时，ani-01 已请求14.660/15.600CPU、余0.940，ani-02 已请求7.740/15.600CPU、余7.860，Ready 节点总余8.800CPU/21,881,091,710B，分箱最多1/6副本；ani-03 NotReady。该时点因容量不足未运行，未创建六副本占用或缩小规格，历史证据保留于 `lb-backend/evidence/reactivated-small/large-capacity-final.json`。用户随后明确“跳过大型”，本轮 large 运行验收现为 **SKIPPED（用户明确跳过）**，不再列作待完成场景；小/中规格和固定映射保持原样。

跳过仅适用于大型规格。基础网络自动分配代码虽已实现并发布，真实 BOSS→新 VPC→基础 EIP/SNAT 主链仍卡在上述 defaults503，业务交付尚未完成；公网/双模式产品验收的前提阻塞也未解除。

用户授权的闲置样本清理释放旧 Inference 的 8 CPU/16Gi，以及 Notebook/KServe manual 样本的 1 CPU/1Gi；旧 Inference ani-03 上的 Pod 仍未物理删除，故其整体删除保持 pending，不强删 finalizer 或伪造完成。保留此前私网/公网 KCN 对照试验、基础 EIP `.2/.3`、公网 EIP `172.16.102.194`、平台默认 SNAT `.192`、用户 VM 和新正常 HTTP 后端/私网 LB 主样本；手工 CR 不采用为本次产品管理资源。43927 预览未修改或停止。

本轮主保留资源：LB `lb_9d66811e2c954d23b4e34a5c9f354fd5`（`base-flow-private-small-20261103`、`10.73.0.101:8081`）；Instance `fb7502b6-3acd-4e33-aeb4-416ba4096ebf`（`base-lb-http-small-20261103`、`10.73.0.18:8080`）；上文100m preset、兼容 HTTP Image/tag、既有 VPC/Subnet。完整清单及已删除专用样本见 Fedora `lb-backend/evidence/final-retained-resources.json`。正式页面为 `http://172.16.101.10:30082/basic-networks` 和 `http://172.16.101.10:30081/load-balancers/lb_9d66811e2c954d23b4e34a5c9f354fd5`。四仓 PR 均不自动合并。
