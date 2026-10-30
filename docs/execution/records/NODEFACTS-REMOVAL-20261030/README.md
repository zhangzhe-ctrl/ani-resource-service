# NodeFacts 拆除与 Boss 验收（2026-10-30）

用户业务链：管理员准备网卡和地址，经 Boss 手填网卡名，Resource 持久受理、保留逻辑占用与幂等，提交 KCN；KCN 接管后由其节点反馈和 CR 状态推进可用。删除和原参数重建必须实际从 Boss 操作。

## 固定输入与执行位置

- Resource 基线：`f60752bbf08127288d863e5028b5962749606706`；分支 `codex/remove-nodefacts-20261030`。
- 测试/构建源码清单：939 个文件，`final-source.manifest.json` 的 SHA-256 为 `92144ea8dbc375d922334ca68d25a1467c2accbbd4f4a0c754516456254a36db`。回传后逐文件核对本地一致；本记录及最终执行状态为验证后的证据更新。
- 所有 Go 生成、格式化、构建、测试、PostgreSQL、镜像及集群操作均在 `ssh fedora` 执行，无本地重任务回退。
- Fedora run：`/home/chabking/workspace/ani-resource-service-runs/remove-nodefacts-20261030`；Go 1.26.10、Buf 1.60.0、sqlc 1.31.1；使用既有 `net05a-heavy.lock` 串行，Go 并行度 2。
- 集群限定 `172.16.101.10/11/12`；kube-system UID `5277649d-e28d-4a0a-ae76-8354365c63bc`，每次写入检查身份，显式指定 API server `.10:6443`。

## 实现与边界

运行路径不再读取 NodeFacts 或检查物理 MAC、carrier、管理路由、OVS 探测。交换机仍冻结 Node UID/成员并条件更新本产品组标签，设备接管按 KCN 对应组的精确网卡名反馈确认；创建、持续观察与删除均移除事实采集前置。旧独立网卡发现、登记及退出写入口显式拒绝，鉴权仍执行，历史字段与交换机内部设备引用保持兼容。

VPC 使用已规划的 YAML 白名单和数据库内地址池冲突检查；受理及首次 Provider 创建不再扫描 NodeFacts/平台 CIDR。修复 KCN `notReadyNodes` 可选字符串的解析。保留权限、持久化、永久幂等、资源占用、租约及未知结果屏障。KCN 源码、桥拆除与通用故障恢复未改动。

## Fedora 验证

| 检查 | 结果 | 证据 |
|---|---|---|
| `make verify`（生成一致性、边界、fault builds、tidy、全包默认测试、vet、build、mod verify、diff check） | PASS | `verify-05.log` |
| 独立 PostgreSQL、实际业务/adapter、外部 KCN fixture 定向网络回归 | PASS，107.566 秒 | `integration-05.log` |
| 静态二进制、非 root runtime 镜像、Harbor 推送 | PASS | `build-image-05.log`、`image.reference` |

PG 实际命令：

```sh
./scripts/integration -v -timeout 20m -run 'Test(VirtualSwitch|Egress|VPCCIDR|RemovedDevice|KCPlatformShutdown|PlatformCancellation|NodeNetwork|Switch|ServiceProcessesRecoverT1)' ./internal/data/network
```

该 PG 验证包含权限拒绝与无副作用、交换机/VLAN/公网池生命周期、VPC 白名单/池并发冲突、实际 Provider 创建、进程崩溃恢复、删除/迟到对象和取消屏障；外部 Kubernetes 为边界 fixture，不能冒充真实集群/Boss 验收。

初次编译的未用 import 已修复。首轮扩大到整个 Network PG 包时，发现基线 public 池无需手工 verification 的新合同下有过期断言，现改为验证旧证据重登记拒绝、健康 public 池仍可分配且不自动重签；另在无关 LB 进程退出用例等待，主动取栈后中断，完整 PG 包不能计 PASS。后续只重跑本次相关范围。门禁还修正了基线已跟踪 `.probe/main.go` 的 import 排序及未用依赖；首次 socket 测试因长 TMPDIR 超出 Unix 路径限制失败，改用 Fedora HOME 下短私有目录后通过。

## 集群拆除

- Resource Deployment UID `e17f1f9e-e406-452f-838d-85062a6e860a`，generation 49；只替换 server 镜像并添加来源注解，带 UID/resourceVersion/旧镜像前置条件。镜像 `sha256:eb1f7995e1d95815ef542c9ea87c5aca4b4b2824c776b744439aa5a1d8af68fe`，见 `deployment-result.json`。
- 三套 collector 的 Deployment、SA、CM、Role/Binding、ClusterRole/Binding 共 21 个对象按精确 UID 删除，确认全部不存在、collector Pod 数为 0。无 selector 批量删除、Node 变更或 finalizer 移除，见 `collector-removal.jsonl`。
- 删除仅给 Resource 开放 `kcn-config get/patch` 的旧 `ani-network-device-adoption` Role/RoleBinding，确认无其他绑定引用，见 `legacy-adoption-rbac-removal.json`。保留共享读观察与 Node 组写权限；KCN 配置未改。
- 采集器及旧权限移除后，03:13:05 和 03:13:39 UTC 两次只读 DB 采样仍持续续证。后一次 VLAN `available`/4.13 秒、公网池及 gateway `available`/2.84 秒、默认出口 `.192` 为 `available/ready/succeeded`/10.07 秒，`applied_enabled=true`，无 pending/unknown。KCN 原对象 UID 保持，EIP/Snat Bound，三节点组反馈 `ens35`。不据此声称已验证实际出网流量。

## Boss 删除与原参数重建验收：PASS

原配置已在 Fedora 私有 run 中保存，凭据不进入源码或本记录。当前原链：`ani-public → ens35/VLAN0 → 172.16.102.0/24 → kcn-system/kcn-cluster`；上游 `.1`、OVN `.2`、分配窗口 `.192..207`、默认出口 `.192`。无租户 EIP/SNAT/base-connectivity 依赖。保留系统 VPC。

页面原有默认出口读取因 Gov 缺 API 目录而 403。已补 singular `GET /api/v1/admin/networks/default-vpc-egress`（API 399）及到既有权限 545 的映射；root 的现有平台权限未扩大。通过 Boss 的真实 API 更新触发既有 Casbin 重载，随后 Gov 与 Resource 查询均 200，页面显示原默认出口。

Boss 原表单拒绝 OVN `.2` 位于分配窗口外，无法原参数重建。用户授权基于最新 `feat/governance-api@d0e89ea4f8b7188ee4b4e7d38ef9a24a91ca9ec7` 修复；提交 `0f30a3eb954803744f13d0df16c63bd40237b6fb`，在 Fedora 启动 `http://172.16.101.31:31083/`，API 代理到 `http://172.16.101.10:30082`。前端只调整 OVN 窗口约束、按窗口内网关计数和三处说明；原参数输出 16 个可分配 IP。Fedora Corepack pnpm 8.6.11 的 18 项定向测试、lint、fmt:check、build 通过。GitNexus NOT_RUN：用户明确许可本次绕过，核对后端源码与固定差异。未更新共享 Boss 部署。

已从 Fedora Boss 真实登录并删除原默认出口，页面显示“未配置”，原 KCN EIP/Snat 不存在，见 `default-egress-deleted-kcn.json`。随后在“不可恢复”确认框按浏览器规则请求执行时确认；用户明确回复“确认继续删除并原参数重建”，继续完成公网池、二层网络和交换机删除。旧 VLAN 与交换机操作均 succeeded，三节点旧组 label、feedback、owner、fence 全部清除，旧内部设备 retired，见 `boss-original-chain-deleted.json` 及 `boss-original-switch-deleted.jpg`。

重建动作均从 Fedora Boss 页面完成：创建同名 `ani-public`，手填原网卡 `ens35` 与 VLAN `0`，创建同名二层网络；再填写原公网 CIDR、`.192..207`、上游 `.1`、OVN `.2`。确认页显示 16 个可分配 IP，见 `boss-original-pool-parameters.jpg`。最后从“平台组件出网”选择新池，恢复系统 VPC 默认 EIP/SNAT。

最终于 **2026-10-30 11:29:26（Asia/Shanghai）** 从实际 Res 数据库与 KCN 只读核对：

- 新交换机 `vsw_465db8e141bb421f977b4b5dabaed23b` 为 available/configured；新 VLAN `vlan_e36c8849f3034c31a1c55571ff845558` 为 available/succeeded，三节点新 binding 与 `ens35` 接管反馈一致。新冻结节点 MAC 为空，未借用旧物理快照。
- 新池 `pool_1c5c2bebf286440fa7ed5006c2256021` 及平台托管网关 `egw_01601cbe1387407182e9ba11ff7a2b79` 均 available/succeeded，KCN underlay Ready。
- 新默认出口 `dve_0b031929127b402aa53b1fbb4dffb54d` 为 available/ready/succeeded，地址 **172.16.102.192**，AppliedEnabled=true，无 pending/unknown。原公网参数与有效分配窗口保持；业务 ID 按新建正常重新分配。
- KCN EIP/Snat 均 Bound，Snat disable=false。系统 VPC 原 UID 保持；租户分配仍关闭，没有设置默认租户公网池。
- 原 NodeFacts 21 个对象逐项 GET 均不存在，collector Pod 数为 0。此前专用 adoption Role/Binding 也已移除。

最终证据为 `boss-recreated-chain-final.json`、`boss-recreated-layer2.jpg` 和 `boss-recreated-default-egress.jpg`。这是实际 Boss → Gov → Res → PostgreSQL/worker → KCN 的删除、重建、接管与状态闭环，不是受控边界 fixture。**本轮未另做 Pod 出网流量复测（NOT_RUN）**。

原独立网关为 `managed_gateway=false`，按既有所有权规则保留，未把它冒充 NodeFacts 残留或强行删除。新页面公网池使用当前合同的平台受管网关，确认页明确展示“由平台自动创建和管理”。KCN 桥拆除逻辑未修改；本次删除与同网卡/VLAN 重建已实际通过。

## 废弃接口与旧代码清理（2026-10-30 追加）

用户指出返回“已移除”的兼容接口和旧过滤器仍在，继续要求清理。Resource 提交 `0a9f1bea326c728e6a056d0c09630ed1e3815c6a` 移除节点网卡发现、独立设备登记/查询、退出/退出查询五个 RPC、消息、服务注册及治理方法白名单；删除 `FilterNodeInterfaces`、独立登记事务和退出 worker/Provider/SQL 查询。旧 Proto 字段编号和名称 reserved，历史 migration 不重写。交换机内部设备仍保留逻辑身份/占用，节点只含 NodeName、NodeUID、Name；物理快照旧字段由 JSON decoder 忽略。

保持旧池/网关/旧 VLAN 的 v1 指纹编码（含历史 null Device 槽）和当前 v2 指纹，新增固定 hash、旧快照兼容以及实际 gRPC 旧方法不分发的回归。新业务类型不再暴露物理事实；原数据库指纹列和内部 operation 标签继续用于逻辑身份与既有约束，不是 NodeFacts 运行依赖。

Fedora 独占 run `ani-resource-service-runs/remove-nodefacts-api-20261030`；925 个测试源码文件，生成后清单 SHA-256 `f52614b676432b357fe68e3fd9818ac9ad8c8b939d226e068760ca8d522850a0`，已逐文件核对本地与远端一致。`make verify` 日志完整通过，并进入后续定向 PostgreSQL 回归，后者 PASS（113.841 秒，任务 PG 容器已回收）。SSH 复用连接回传中断导致外层终端返回 255，随后通过独立连接恢复完整日志，未把连接错误记作业务测试失败，也未重跑已经通过的检查。

Governance 提交 `88c5a11362835498f68da390d02d2bcebd072518` 同步删除 HTTP 路由、专属 DTO/转发/权限映射及目录种子，使用正式 Resource SDK `v0.0.0-20261030040700-0a9f1bea326c`。通过标准 Go VCS 获取并核对完整 Origin.Hash、module/go.mod checksum；先前官方 zip/sumdb 连接 EOF 与错误代理端点均保留失败记录。最终启用 Go HTTP/1.1 并使用官方 sum.golang.google.cn 校验服务后下载通过；标准 `go list -m -json module@完整SHA` 单独提供 Origin，`go mod download -json` 在已缓存依赖上未提供此可选字段。

完整 `make api`、auth/service/data 定向单测、vet、服务构建均 PASS。真实 Gov HTTP/JWT/Casbin → mTLS → 实际 Resource/独立 PostgreSQL/worker 联测 PASS（12.789 秒）：保留权限拒绝，验证二层创建/接管/引用阻止/删除、公网与内网地址池、默认出口和五个旧 HTTP 路径无副作用。Kubernetes/KCN 是这组联测的外部边界 fixture。先前两次联测分别发现 fixture 缺少已存在的 EIP 权限种子，以及仍要求已删除 `mac` 字段的旧断言；已补种子、去重共用权限 grant，并改为断言 MAC 不存在，其余业务断言保留。最初全文件 gofmt 带来的 22 个无关格式改动已按原始字节恢复，未混入提交。最终 2698 文件的源码清单 SHA-256 `80ec47216a8be879b5f8f2c73b0cc492054c21565ad8122b64499c39828a5f9a`，本地与 Fedora 逐文件一致。结果与失败记录见 [验证收据](api-cleanup-verification.json)及[核心测试日志](api-cleanup-test-excerpts.log)。

10～12 的 Gov 目录按固定行快照、引用检查与事务锁，精确删除 5 API、5 权限映射、10 专属角色 grant 和 5 专属权限；不改历史、菜单、计划、通用策略或其他角色权限。公开[目录清理收据](api-cleanup-catalog-cleanup.jsonl)保留实际计数，完整行恢复快照只存 Fedora 私有目录。Boss API 管理按 `node-interfaces` 搜索已为 0 条，见 `boss-node-interfaces-removed.jpg`。

本次跨仓变更为 [Resource #28](https://github.com/zhangzhe-ctrl/ani-resource-service/pull/28) 和 [Governance #73](https://github.com/zhangzhe-ctrl/ani-governance/pull/73)，原 Boss 表单修复为 [Boss #6](https://github.com/bykenkk/ani-boss-console/pull/6)。成对镜像、集群更新和最终页面复核的结果见下文；不把上述受控联测当作集群验收。

### 废弃接口清理后的成对发布与 Boss 复验：PASS

Fedora 使用 Go 1.26.10、CGO=0、并行度 2 和既有共享重任务锁构建两服务，分别限制 Resource 1536MiB、Governance 3GiB；复用固定 distroless 基础镜像，私有 Harbor 推送与回读 digest 均通过。[成对构建收据](api-cleanup-image-pair.json)记录实际正式提交、临时远端 Git 快照、编译预算与镜像身份。镜像构建源码清单额外包含生成器、fault-build 和 Python cache 等忽略产物，因此清单格式/文件数与测试源码清单不同；[逐文件对照收据](api-cleanup-source-match.json)确认全部 925/2698 个测试源码内容一致，额外产物逐项列出，运行镜像上下文仅包含服务二进制、Dockerfile 和 notices。

| 服务 | 正式源码提交 | 部署镜像 digest | generation |
|---|---|---|---|
| Resource | `0a9f1bea326c728e6a056d0c09630ed1e3815c6a` | `sha256:0d508e2000d1d3238c3569a076ebc7a4f38ff2537368bfa5dccb0f6ab9d0ece9` | 50 |
| Governance | `88c5a11362835498f68da390d02d2bcebd072518` | `sha256:b472ac3daba6eff5fceacc41e2bf882dabf1ab83435bc4407d47093dcc72b8ac` | 154 |

限定三节点 UID/IP、cluster UID、两 Deployment UID 和旧镜像，使用新 resourceVersion 的 JSON Patch 条件更新；先 Gov 后 Res，仅修改 server image 与来源注解。两次 patch 结果均 confirmed，两服务均 1/1 Ready，实际 Pod imageID 与预期 digest 完全一致；Gov 新进程启动已成功 ResetPolicies/Casbin 重载。未改配置、replicas、KCN 或 Boss 共享部署，见[部署收据](api-cleanup-deployment.json)。

最终[实际 DB/KCN 只读核验](api-cleanup-live-acceptance.json)的 11 项检查全部通过：旧 API 目录 0 条、collector 及配套对象 0 个，原交换机、VLAN、池、默认出口 ID/关系保持，三节点组仍 ACK `ens35`；VLAN/public pool 观测未过期且 available/succeeded，默认出口 `.192` 为 available/ready/succeeded、AppliedEnabled=true、pending_unknown=false。KCN 对象 UID 保持，pool Ready，EIP/Snat Bound，系统 VPC 原 UID 保持。年龄按 DB 时钟测量并累计本次 I/O 耗时，读取不会续期。

新版发布后从 Fedora Boss 实际重新加载：API 管理搜索 `node-interfaces` 为 0 条；二层 `cluster-public-ens35-res-20261022` 和公网池 `cluster-public-102-res-20261022` 均可用。打开“平台组件出网”，显示原 `kcn-system/kcn-cluster`、出口 **172.16.102.192**、状态“可用”、观测“有效”。页面证据为 `boss-node-interfaces-removed.jpg`、`api-cleanup-boss-layer2.jpg` 和 `api-cleanup-boss-default-egress.jpg`。本追加轮没有再次删除已恢复业务链；原删除/同参数重建的真实 Boss 验收见上节，**实际 Pod 出网流量仍为 NOT_RUN**。
