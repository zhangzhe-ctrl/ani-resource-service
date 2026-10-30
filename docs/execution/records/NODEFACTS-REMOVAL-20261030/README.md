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

## Boss 验收（默认出口删除通过，后续删除待确认）

原配置已在 Fedora 私有 run 中保存，凭据不进入源码或本记录。当前原链：`ani-public → ens35/VLAN0 → 172.16.102.0/24 → kcn-system/kcn-cluster`；上游 `.1`、OVN `.2`、分配窗口 `.192..207`、默认出口 `.192`。无租户 EIP/SNAT/base-connectivity 依赖。保留系统 VPC。

页面原有默认出口读取因 Gov 缺 API 目录而 403。已补 singular `GET /api/v1/admin/networks/default-vpc-egress`（API 399）及到既有权限 545 的映射；root 的现有平台权限未扩大。通过 Boss 的真实 API 更新触发既有 Casbin 重载，随后 Gov 与 Resource 查询均 200，页面显示原默认出口。

Boss 原表单拒绝 OVN `.2` 位于分配窗口外，无法原参数重建。用户授权基于最新 `feat/governance-api@d0e89ea4f8b7188ee4b4e7d38ef9a24a91ca9ec7` 修复；提交 `0f30a3eb954803744f13d0df16c63bd40237b6fb`，在 Fedora 启动 `http://172.16.101.31:31083/`，API 代理到 `http://172.16.101.10:30082`。前端只调整 OVN 窗口约束、按窗口内网关计数和三处说明；原参数输出 16 个可分配 IP。Fedora Corepack pnpm 8.6.11 的 18 项定向测试、lint、fmt:check、build 通过。GitNexus NOT_RUN：用户明确许可本次绕过，核对后端源码与固定差异。未更新共享 Boss 部署。

已从 Fedora Boss 真实登录并删除原默认出口。页面随后显示“未配置”，原 KCN EIP/Snat 均不存在，见 `default-egress-deleted-kcn.json`。原公网池仍在，二层网络及交换机未删除。当前停在 Boss“删除公有网络”的“删除后不可恢复”确认框（`boss-delete-confirm.jpg`）；因浏览器操作规则要求对不可恢复删除在执行时确认，已请求用户确认公网池、二层网络与交换机的后续删除和原参数重建。在确认返回前不继续这些删除。删除/重建完整业务验收仍为 **not_verified**，不得称已完成。
