# Sandbox Kubernetes 权限

Sandbox 运行在 Resource 进程中，共用该进程的 Kubernetes 客户端；此清单不创建新的
Deployment 或 ServiceAccount。`rbac.yaml` 使用现有的 `ani-system/ani-resource-service`
身份。若实际部署使用不同 ServiceAccount 或显式 kubeconfig，须以该客户端的实际身份
配置和核验授权，不能假定示例绑定已经生效。

集群级规则供读取和初始化共享租户命名空间、读取 StorageClass、Kata RuntimeClass 和 PV 使用；其余 Pod、PVC、镜像拉取 Secret
权限只通过每个租户命名空间的 RoleBinding 授予。Secret 的 get/update 限定为该租户的
Sandbox 拉取 Secret；Kubernetes RBAC 无法按名称限制 create。Storage 的现有角色覆盖部分相同操作，
但其租户角色不包含 Sandbox 的 Pod 和 Secret 操作，集群角色也没有 PV list。
两套角色可以绑定到同一身份，Kubernetes 会合并权限。

## 普通事件、日志与出口查询权限升级顺序

普通事件只需要对已绑定的租户命名空间授予 `events` 的 `list`，不授予集群级事件读取。
日志还需要当前租户命名空间的 `pods/log:get`。本版程序严格接受旧三规则、旧规则加
`events:list`、再加 `pods/log:get`、或再加 `networking.k8s.io/networkpolicies:list` 这四种精确角色，不接受其他组合及多余权限。出口查询只读标准 Kubernetes NetworkPolicy 声明，不证明 CNI、集群级策略或 Host 白名单实际生效。
升级时**先滚动更新所有 Resource 副本并确认旧副本全部退出**，然后可依次增加事件、日志和 NetworkPolicy 只读
权限（或直接应用含三者的 `rbac.yaml`）；混有旧副本时变更角色会导致旧副本拒绝校验租户
空间。权限未到位期间对应功能不可用。回滚时先移除 `networkpolicies:list` 和 `pods/log:get`，需要回滚到更旧程序时
再移除 `events:list`，确认目标程序接受该精确角色后再回滚程序。不得以其他规则或宽权限
代替。现有同名角色绑定
无须重建；但应逐租户确认绑定仍存在并使用 Resource 实际身份核对：

```bash
kubectl auth can-i list events -n "$NS" --as="$SA"
kubectl auth can-i get pods --subresource=log -n "$NS" --as="$SA"
kubectl auth can-i list networkpolicies.networking.k8s.io -n "$NS" --as="$SA"
```

这只是部署步骤；不代表已经部署或测试。普通事件读取已记录 Pod UID 对应的 Kubernetes
当前保留事件，身份不能验证时返回错误；保存期限由集群事件 TTL 决定，不能代表完整历史事件库。


## 检查点权限与语义

检查点候选还需要租户内 PVC patch、VolumeSnapshot get/create/delete 和集群级 VolumeSnapshotClass get/list、VolumeSnapshotContent get。升级时必须先更新并排空全部旧 Resource 副本，再更新租户角色；旧副本不认识扩展后的精确角色。写入及删除结果不明时保留操作记录和屏障，不能把对象暂时不存在当作已回收。

检查点保存工作盘，运行中创建只保证崩溃一致性，不包括内存，不承诺应用一致性。恢复要求 Sandbox 暂停；克隆目标完成磁盘准备后保持暂停。新卷是否脱离来源快照必须有可信存储证据，不能仅凭 PVC Bound 判断。

这些是候选发布约束，不表示已部署或完成真实集群验收。

## 开通

以下手工命令适用于尚未安装租户自动开通候选策略的现有部署。自动开通上线后，
Resource 会为命名空间和角色对象写入 UID 标记；候选准入策略要求这些标记与实际
空间一致，不能直接用下述无标记的手工命令创建同名对象。具体实现和验收进度见
[执行状态](../../docs/execution/status.md#2026-10-30租户空间自动开通进行中)。

1. 由管理员应用 `deployments/sandbox/rbac.yaml`，并确认 Resource 实际使用的身份。
2. 租户初始化命名空间后，由负责租户开通的管理员在该命名空间创建绑定；下例中的
   `TENANT_ID` 是该租户的 Resource UUID，`NS` 是其已初始化的命名空间：

   ```bash
   TENANT_ID=<租户 UUID>
   NS=<租户命名空间>
   kubectl create rolebinding ani-sandbox-tenant \
     --clusterrole=ani-sandbox-tenant \
     --serviceaccount=ani-system:ani-resource-service \
     -n "$NS"
   kubectl create role ani-sandbox-pull-secret \
     --verb=get,update --resource=secrets \
     --resource-name="ani-sandbox-pull-$TENANT_ID" -n "$NS"
   kubectl create rolebinding ani-sandbox-pull-secret \
     --role=ani-sandbox-pull-secret \
     --serviceaccount=ani-system:ani-resource-service -n "$NS"
   ```

   对已有租户也要逐个核对。仅创建命名空间不会自动创建此 RoleBinding；当前清单不授予
   Resource 管理 RoleBinding 的权限。若租户开通流程已经自动管理绑定，应在该流程配置
   等价绑定并核验其实际生效。
3. 用实际身份逐项核验，以下以示例 ServiceAccount 为例：

   ```bash
   SA=system:serviceaccount:ani-system:ani-resource-service
   kubectl auth can-i get namespaces --as="$SA"
   kubectl auth can-i create namespaces --as="$SA"
   kubectl auth can-i get storageclasses.storage.k8s.io --as="$SA"
   kubectl auth can-i list storageclasses.storage.k8s.io --as="$SA"
   kubectl auth can-i get runtimeclasses.node.k8s.io/sandbox-kata --as="$SA"
   kubectl auth can-i get runtimeclasses.node.k8s.io/kata --as="$SA"
   kubectl auth can-i get persistentvolumes --as="$SA"
   kubectl auth can-i list persistentvolumes --as="$SA"
   for resource in pods persistentvolumeclaims; do
     for verb in get create delete; do
       kubectl auth can-i "$verb" "$resource" -n "$NS" --as="$SA"
     done
   done
   kubectl auth can-i create pods --subresource=exec -n "$NS" --as="$SA"
   kubectl auth can-i create secrets -n "$NS" --as="$SA"
   kubectl auth can-i get "secret/ani-sandbox-pull-$TENANT_ID" -n "$NS" --as="$SA"
   kubectl auth can-i update "secret/ani-sandbox-pull-$TENANT_ID" -n "$NS" --as="$SA"
   ```

以上操作只描述待执行的部署步骤。权限检查不能替代真实创建、暂停、恢复和删除验收；
目标集群现有 ServiceAccount、其他角色、准入策略和实际有效权限需要现场核实。Secret
授权和 Pod 创建能力受 RoleBinding 所在租户命名空间约束，不能通过集群级绑定授予
`ani-sandbox-tenant`。Pod 创建能力可能间接访问命名空间中的其他 Secret；Kubernetes
RBAC 不能按 Pod 内容限制这项能力，需要结合实际准入策略评估。
候选准入清单还限制 Resource 直接创建的 Sandbox Pod、服务账号 Token Secret 和
exec 目标；这些策略必须先验证其有效性及与 Instance、VM、Image 的兼容，再与租户
权限一起发布。候选策略的存在不代表目标集群已经安装或验收。

### 开通状态诊断

自动开通上线后，可使用已有 Resource 数据库连接，只读查询指定 Resource 租户 UUID
的开通状态和写入记录（示例使用 `psql -v tenant_id=<租户 UUID>`）：

```sql
SELECT tenant_id, operation_id, phase, namespace_uid, pending_id, pending_kind, updated_at
FROM sandbox.tenant_spaces WHERE tenant_id = :'tenant_id'::uuid;

SELECT mutation_id, kind, outcome, receipt
FROM sandbox.tenant_space_mutations WHERE tenant_id = :'tenant_id'::uuid;
```

`pending_id` 非空表示一次 Kubernetes 写入尚未得到可信回执；查询不到对象不能证明
该写入不会迟到。应保留记录并按回执排查，不直接修改数据库清除屏障。

### Sandbox Token 签名密钥

Token 与数据库连接、命名空间和 Resource 入口共用既有配置；另外需要一份**稳定的签名密钥文件**，通过 `ANI_SANDBOX_TOKEN_KEYS_FILE` 指向受保护的普通文件。它只存令牌签名密钥，不是 Sandbox 服务配置文件。密钥缺失或非法时 Token 接口返回不可用，其他 Sandbox 能力继续使用；生产环境不能用随机进程密钥兜底。

文件为 JSON，`current` 指向当前签发密钥，`keys` 是密钥 ID 到标准 Base64 编码密钥的映射，每个解码后至少 32 字节、最多 128 字节，最多保留 16 个 ID：

```json
{"current":"key-2026-a","keys":{"key-2026-a":"<base64-encoded-secret>"}}
```

示例占位符不能直接使用；实际内容应使用密码学安全随机数生成、通过受管 Secret 注入，禁止写入仓库或日志。服务读取文件时要求普通文件、权限不宽于 `0600`，且不能是符号链接；Kubernetes 默认 Secret 目录条目通常是符号链接，须采用符合该要求的受管文件挂载方式（例如经过验证的单文件 subPath），并确保服务运行用户可读。部署前先以实际运行身份验证权限。签名密钥读取于启动时，修改后需滚动重启。

轮换应分三步：先让所有副本都持有新旧密钥且仍以旧密钥签发；再将 `current` 切到新密钥并滚动重启；最后至少等待最长 Token 有效期一小时和滚动窗口，确认不再需要旧 Token 后才移除旧密钥。旧密钥提前移除会使其签发的 Token 无法验证和幂等重放。密钥泄露时应按事件处置立即移除受影响密钥并使相关 Token 失效。Token 吊销及 Sandbox 版本变化对后续请求生效，已在执行中的请求不保证被中断。

## Pod UID 历史升级顺序

使用第 12 版迁移及 Pod 历史查询前，必须先停止并排空全部旧 Resource Sandbox worker，等待在途 Provider 写入及其回执落库；然后执行迁移、部署新版本并恢复 worker。不得让旧、新 worker 混跑：旧 worker 在新版本并行期间仍可能创建后清空 UID，绕过历史记录。旧版进程不会因数据库存在新版本迁移而自动拒绝启动，因此不能依赖 schema 检查来阻止混跑。回滚到旧版之前也必须停止并排空所有 Sandbox worker，回滚期间保持旧 worker 不写入；如无法保证旧 worker 不写入，应停止旧 Resource 进程，恢复新版本后才恢复 Sandbox worker。

迁移只回填当时 `provider_refs` 中仍保存的 UID，迁移前已清空的 UID 无法恢复；迁移后若创建回执丢失且 Pod 在可观察前消失，也可能从未取得 UID。因此无论何时，都不能把历史记录当作所有 Pod 的完整清单。`first_recorded_at` 是首次记录时间，不是实际创建时间。上述顺序是发布前提，不代表已部署或完成集群验证。

实验性预览默认禁用。仅在明确允许直接 NodePort 暴露的环境，将 Resource 进程 `ANI_SANDBOX_EXPERIMENTAL_NODEPORT_ENABLED` 显式设为 `true`；Gov 同时须单独登记并授权预览 API。NodePort 不提供额外入口认证，在能访问任意集群节点该端口的网络中均可能被访问，返回的节点 IP 并不限定唯一入口。地址来自已核验的运行 Pod、控制器 EndpointSlice、所在节点 InternalIP 和 Service nodePort，不保证客户端可达或应用健康。关闭、暂停、删除和过期触发清理，须等待 Service 与对应 EndpointSlice 消失；不确定的创建保持待确认。 Kubernetes 对象回收不保证已建立连接立即终止，NodePort 之后可能被重新分配。

预览的租户 ClusterRole 需要 `services` 的 `get/create/delete` 与 `discovery.k8s.io/endpointslices` 的 `list`；集群读取 `nodes` 的 `get`。更新顺序先部署所有接受扩展权限规则的 Resource 副本，再更新租户 ClusterRole 和授权目录，避免旧副本拒绝新版规则。该功能的现场部署和验收状态以执行状态为准。

Pod selector 依赖租户 Namespace 的 Kubernetes 权限边界：普通租户不得获得 Pod 创建或标签更新权限。Service 按标签选择 Pod，UID 核验是观察校验而非 selector 的隔离条件；若其他主体能伪造标签，则可能被加入流量目标。66 的现有 Pod admission 仅约束 Resource ServiceAccount 的 CREATE，不能证明其他主体或 UPDATE 受限。

## Sandbox 终端受信入口

Sandbox 不依赖创建 Instance。Gov 的终端绑定解析使用现有 Resource 业务 gRPC 及正常租户身份；SG 的绑定验证使用现有 Instance/VM 终端 mTLS 监听器。如果部署没有启用 Instance 或 VM，设置通用进程监听地址 `ANI_TERMINAL_ADDRESS` 为 SG 提供同等受限监听；其复用现有 Resource TLS 证书文件，不需要独立服务或 Sandbox 配置文件。未配置该监听时无法验证终端连接。终端容器须有 `/bin/sh` 且带 Resource 管理的 Pod UID downward API 字段。终端已建立连接的终止由网关负责，Resource 的校验只约束新连接。

## 克隆临时维护闭闸

`ANI_SANDBOX_CLONE_WRITES_PAUSED=true` 在 Resource 启动时于 Sandbox 共享装配入口开启临时克隆闭闸，所有调用方的 `AcceptSandboxClone` 在进入业务处理和数据库写入之前返回 gRPC `Unavailable`，原因是 `SANDBOX_CLONE_TEMPORARILY_UNAVAILABLE`。其他 Sandbox RPC 不受这一开关影响。此入口同时覆盖常规初始化和依赖恢复后的 `sandboxRetry`，也适用于共享监听和 Governance 专用装配。该变量只接受空值、`false`、`true`；空值和 `false` 是不闭闸，其他值使服务启动失败，不会把非法配置解释为开闸。修改配置需要重启进程。维护切换时必须显式配置 `true` 并核对每个新 Pod 的实际配置，不能依赖缺省值。

闭闸也拒绝已经受理命令的重复投递，不返回接受回执；调用方必须保留原来的命令身份及未知结果，不能把 `Unavailable` 视为未曾受理。已经保存在 Resource 中的 transfer 工作继续由 worker 根据原日志恢复；此闭闸不会停止或撤销这些工作，也不会保证已发出的 Provider 请求未发生。普通创建沿用原有受限出口检查和已受理重放规则，不因该克隆开关改变。

需要 017/018 迁移时，先隔离所有克隆入口和后台投递，并有序停止、排空所有使用同一 Sandbox 数据库的旧 Resource writer，包括独立部署及其后台 worker；确认旧 Pod/进程及数据库写连接均退出，并按原 Provider journal 保留在途或未知结果。之后执行数据库备份、校验和迁移，再以明确设置 `true` 的新 Resource 启动，核验所有新副本镜像、配置、注册路径和闭闸结果。不得让旧新 writer 混跑，也不得仅以摘除 Service Endpoint 代替旧 worker 排空。排查时按实际数据库身份、进程配置及权限识别 writer，避免停掉无 Sandbox 写权限的其他业务或 e2e 测试进程。共用 Resource 入口在停止旧进程的维护窗口内会短暂不可用。

保持闭闸直到全部可能投递克隆的调用方都已验证理解新 Resource 的确定拒绝回执，或者以可靠措施限制不兼容调用方；必须包含 Gov 的前台及后台投递，不能假定只有 Gov 持有服务证书。还须复查未确认的受限出口创建和克隆意图，不能将暂时不可查询当作已完成。解除前核对目标版本与所有客户端、排空旧副本和未决请求，再以受控发布把该开关显式设为 `false` 并验证回执及原命令重试。含新拒绝回执的数据不能直接回退到无法识别其语义的旧 Resource；保留 journal 并优先恢复兼容的新版本，不能删回执绕过屏障。以上是发布前提，不代表现场已完成迁移或验证。
