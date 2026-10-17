# 实例后端运行与恢复

行为合同见 [v1.5 规格](../specs/instance-mvp.md)，实际执行结果只看 [当前状态](../execution/status.md#gpu-container-instance-mvp-v15)。本说明提供配置和恢复步骤，不代表共享集群已部署或真实 GPU 已验收。

## 源码与验证

本任务重任务仅在 `ssh fedora` 执行，使用任务独占源码、缓存和 TMPDIR；互斥锁、资源预算、固定基线及快照见当前状态。按 [远程执行约定](../remote-execution.md) 同步带文件哈希的源码，不同步私钥、DSN 或 kubeconfig。

Resource 的 [实例 API 生成器](../../scripts/generate-instance-api) 使用固定 Buf/Go 插件及正式 Acc proto；sqlc 使用根 `sqlc.yaml`。实例定向 PG 验证入口为 [instance-integration](../../scripts/instance-integration)，只创建、迁移和清理带任务标签的独占 PostgreSQL；`INSTANCE_TEST_RUN` 可限制相关场景。提交前仍执行仓库要求的 `make verify`；实例真实 PG 门禁单独执行，不能由普通单元测试代替。

终端验证协议单独发布为 `github.com/zhangzhe-ctrl/ani-resource-service/api/instance/terminal`，只依赖 protobuf/gRPC，SG 不依赖 Resource 根模块及其 Kubernetes SDK。正式版本固定在各消费者 `go.mod`；升级先发布 provider，核验下载来源 SHA/sums，再用 `GOWORK=off` 构建消费者。早期受控联调使用任务私有 go.work；2026-10-13 发布前已改为正式下载并验证 Resource/SG 独立构建，不提交兄弟目录 replace。

## 环境与启动

写入前核对实际 API、cluster UID、节点地址与工作负载；只能使用本任务确认的集群与实验租户。冻结真实 Acc group/profile/version、Queue、受支持的 Volcano/HAMi 基线和镜像 digest。镜像须提供长驻入口、指定 shell、UID/GID 与可写 `/workspace`；不在启动时修理镜像或临时放宽 Acc 基线。

环境预先提供保存 Namespace 和普通 SA。Namespace 兼容现有 Network 标签 `network.ani.io/tenant-id=<完整资源租户 UUID>`、`network.ani.io/managed-by=ani-network-service`；缺省 Namespace 取当前 Network 配置前缀加完整 UUID。工作盘 StorageClass 必须采用 Delete reclaim policy。服务使用已有受管 Kubernetes 身份，实例 SA 不挂载 API token；本任务不是不互信租户隔离认证。

Resource 的 Kubernetes 身份还需要 PV 的 `get/list` 只读权限，以记录 PVC 的实际绑定和核对回收；实例普通 SA 不需要这项权限。删除只针对保存 UID 的专属 PVC，不直接删除 PV。PVC 消失后仍存在的原 PV、Retain 回收策略或归属不符都不能完成工作盘清理；PV 绑定按 claimRef 的完整 UID/Namespace/name 及保存的 PV UID 核验。

Resource 使用 `ANI_NETWORK_MODE=governance`，保留现有业务 mTLS listener 与服务器 DNS SAN `ani-network-service`，启用现有 Image runtime 后才可启用实例。私有 `ANI_INSTANCE_CONFIG_FILE` 是严格 JSON，字段以 [instanceSettings](../../cmd/ani-resource-service/instance.go) 为准：

```json
{
  "DatabaseDSNFile": "/managed/instance-runtime.dsn",
  "SigningKeyFile": "/managed/instance-ed25519.key",
  "QuotaAddress": "governance.internal:9445",
  "QuotaCAFile": "/managed/quota-ca.pem",
  "QuotaCertFile": "/managed/resource-client.pem",
  "QuotaKeyFile": "/managed/resource-client.key",
  "QuotaServerName": "ani-governance",
  "ReleaseAddress": "accelerator.internal:9443",
  "ReleaseCAFile": "/managed/accelerator-ca.pem",
  "ReleaseCertFile": "/managed/resource-accelerator.pem",
  "ReleaseKeyFile": "/managed/resource-accelerator.key",
  "ReleaseServerName": "ani-accelerator-service",
  "TerminalAddress": ":9446",
  "Service": {
    "ClusterID": "<Acc 的真实 cluster UUID>",
    "ServiceAccount": "workload",
    "StorageClass": "ani-block",
    "WorkspaceSize": "10Gi",
    "Shell": "/bin/bash",
    "UID": 1000,
    "GID": 1000
  }
}
```

示例中的 UUID、地址与私有路径必须替换；未配置默认拒绝。NamespacePrefix 始终从当前 Network 配置读入，不由该 JSON 改写。SigningKeyFile 为 64 字节原始 Ed25519 私钥，不放入源码、普通归档或日志。

ACC 物理释放只使用独立 `spiffe://ani.internal/service/ani-resource-service` 客户端身份，权限仅为 `ObserveRelease`，不复用 Gov 或 Quota 身份。`Release*` 字段全部必填，TLS 1.3 校验受管 CA 和精确 DNS SAN。创建 Notebook 的外部调用前，同一实例/lease 事务先持久 `ExternalComputeStarted` 与完整已见 Pod 历史；进程重启、Stop/Start 和并发删除均保留原 UID。普通 Notebook 删除采用 UID/RV 条件和 Foreground GC，不强制删除 Pod。

Notebook、StatefulSet 和 Pod 全部消失且没有待确认写后，先持久 `ComputeSealAt`，下一轮才查询 ACC 的 cluster/owner 全范围物理证据。ACC 必须确认完整来源水位已越过该 fence、无活分配，所有已知分配都有正向释放事实；未知、节点离线、缺来源或不匹配回执继续占额，不能设置 `ComputeCleaned` 或发送退额事件。只有从未开始外部计算创建且已封闭的取消/删除先到，才使用内部未执行事实跳过物理查询。该证据不增加规格资格或有效期限制。

GPU RuntimeClass 必须由 ACC 受管配置写入唯一 `GpuExecution.runtime_class_name`；Res 没有第二个配置来源。切换新合同前先完整结束旧 GPU 活动并确认释放 outbox 已匹配确认，再显式运行实例 migration，发布新 GPU Profile/Preset。不得把旧 F256 或百分比状态交给新服务恢复。

规格唯一来源为 Resource 的 `instance.compute_presets`，配置中的 `Presets` 已删除，严格配置解码会拒绝该旧字段。规格通过 Gov 的平台管理入口创建，CPU 创建不依赖 ACC；GPU 新建/替换引用必须使用可信 ACC 目录的完整事实。

首次迁移由独立 owner 身份显式运行 `ANI_INSTANCE_OWNER_DSN_FILE=<私有文件> ANI_INSTANCE_RUNTIME_ROLE=<受限角色> ./ani-resource-service -instance-migrate`。迁移维护本域 checksum/version 并向已有 runtime 角色授予本域权限；应用只用受限角色，不在启动时迁移，不用超级用户。启动方式沿用现有 Resource 的 `-conf` 与 Image 配置，新增 `ANI_INSTANCE_CONFIG_FILE`。

Governance 配置 `ANI_INSTANCE_ADDR/CA/CERT/KEY`（现有 Resource DNS-only mTLS），登记 INSTANCE 套餐模块、九个具体 API 及实验用户当前角色权限、GPU quota。先执行既有 `sync-apis --dry-run`，审阅后执行 `sync-apis`；Governance 仓的 `scripts/ops/sql/register-instance-permissions.sql` 只登记对应 action，不自动扩大租户或角色授权。退额 listener 增加 `ANI_QUOTA_RESOURCE_DNS_SAN=<Resource 客户端精确 DNS SAN>`，固定映射 `ani-resource-service`，不能与 Inference 名称复用。

Acc 在独立恢复点之后由迁移 owner 显式执行 `0003_resource_instance_owner.sql`，runtime 要求 schema 3；旧 schema 2 不接受 Resource owner。迁移入口/CI 准备已登记 0003，应用不自动迁移。`resource_owner_public_key_file` 指向本实例签名密钥对应的受管 Base64 公钥，独立于 Inference 公钥。实际 Pod 的签名与投影关联后才显示租户 Binding；缺钥或签名不匹配仍计实际容量。Gov 的原账同步产生 ENDED，不能据此判断设备物理空闲。恢复旧 Acc 二进制还需匹配的数据库恢复点，不能手改版本记录。

Gov → SG 配置 `ANI_INSTANCE_SG_ADDR/SERVER_NAME/CA_FILE/CERT_FILE/KEY_FILE`，客户端证书唯一 URI 为 `spiffe://ani.internal/service/ani-governance`。SG 启用 `RESOURCE_INSTANCE_ADDR/SERVER_NAME/CA_FILE/CERT_FILE/KEY_FILE` 和 `RESOURCE_INSTANCE_CLUSTER_ID`；它连接 Resource 的同进程终端验证 listener，证书唯一 URI 为 `spiffe://ani.internal/service/ani-session-gateway`。SG 控制口同时要求 `INSTANCE_CONTROL_CA_FILE/CERT_FILE/KEY_FILE` 和 TLS 1.3。两个 listener 均按方法和服务身份授权，不能把 Gov 业务委托证书当作 SG 终端权限。

SG 继续使用现有 Redis 持久票据、票据密钥、Origin/WS 及 Kubernetes exec 配置。票据冻结 Namespace、业务版本、Notebook/Pod UID、容器和 shell；签发与连接均复核 Resource。Pod 中 `ANI_INSTANCE_POD_UID` 来自 downward API，exec 再校验它，以拒绝查询后出现的同名替换 Pod。前端由专人接手；提供原 DTO/API，不另外创建页面工程。

Fedora 隔离 SG 可以设置绝对路径 `KUBECONFIG`，使用既有 CA 校验的 HTTPS API 与实验 Namespace 内的普通 SA Token；禁止 insecure、ExecProvider/AuthProvider。未设置则继续使用 InClusterConfig。只需要本 Namespace 的 Pods get/list、pods/exec create。显式配置 `INSTANCE_CONTROL_CA_FILE/CERT_FILE/KEY_FILE` 时，即使没有启用实例 validator，控制口也启用 mTLS；三个字段必须完整。

Notebook 1.10 的标签/注解放在 Notebook 顶层 metadata，`spec.template` 只有 spec。Resource 按保存 Notebook UID 核对同名 StatefulSet 的 controller ownerRef，再核对 Pod 的 STS UID；没有 STS 标签不能证明计算已清理。实际来源限制见 [Notebook 投影核验](../execution/records/INSTANCE-MVP-V15/notebook-v1.10-projection.md)。普通 Stop/Start 使用原生停止注解，不修改控制器生成的子对象。

## CPU 实例与前端联调

实例内外统一为 `instance_kind=container` 与来自数据库规格的 `compute_type=cpu|gpu`。无空类型推断，不接受 `instance_type` 或外层 `preset_version`。

先经平台入口创建规格，再使用返回的 UUID `preset_id` 创建租户实例：

```json
{"data":{"code":"cpu-1c-2g","name":"CPU 1核 / 2Gi","compute_type":"cpu","cpu_millis":"1000","memory_mib":"2048","enabled":true}}
```

```json
{"data":{"idempotency_key":"<新UUID>","name":"cpu-dev","image_id":"<已登记镜像ID>","image_scope":"platform","preset_id":"<规格UUID>"}}
```

CPU 不带 GPU request/plan/charges。CPU/内存精确转换为 Quantity 并冻结；规格修改、禁用、删除都不改变已经持久受理的命令或已有实例。调度原始缺失与实际 `NONE` 分别保留。已受理重试使用原命令；Start/Stop/Delete 继续使用新的幂等键和最新 `expected_version`。

普通列表使用 `page`/`pageSize`、`items`/`total`。静态详情返回冻结 CPU/内存、规格名称/编码、实际调度、安全 GPU 摘要及真实创建时间，不查询当前规格、ACC 或 Kubernetes。CPU `quota_returned=true` 表示无待退 GPU 配额。

Exec 返回一次性 `ws_url`，通过 SG 的 `ani.terminal.v1` WebSocket 子协议传输终端帧；不要保存票据到普通日志或在停止/重启后复用旧票据。停止后保留 `/workspace`，最终删除会清除专属工作目录。CPU 不展示“仍占 GPU 配额”。

以下 2026-10-15 隔离环境记录属于旧版运行证据，不是新合同启动配置；升级必须先完成本批迁移和配置切换。旧运行根：`/home/chabking/workspace/ani-resource-service-runs/instance-cpu-mvp-20261015`。`private/cpu-runtime.sh start|stop|status [resource|gov|sg]` 管理任务专用 Docker 服务，SSH 退出后继续运行；不启动 Acc、不改共享部署。原持久 PG/Redis 与私有身份材料保留，任务容器按 label 核对归属。配置、二进制、Image receipt 只在该任务目录，登录密码/token 不进入仓库。

前端开发机可建立专用隧道：

```bash
ssh -N -o ExitOnForwardFailure=yes \
  -L 18080:127.0.0.1:52673 -L 18081:127.0.0.1:47157 fedora
```

Gov 地址为 `http://127.0.0.1:18080`，SG 票据的公开地址为 `ws://127.0.0.1:18081/api/v1/realtime`。前端开发服务器代理 `/api` 到 Gov；SG 已允许 `http://localhost:5173`、`http://127.0.0.1:5173` 与实验 driver Origin `http://instance-lab`。其他 Origin 需在该任务私有配置中明确登记并重启 SG，不放开通配。

手动刷新 SG token 并只重启该任务 SG：

```bash
ssh fedora 'python3 /home/chabking/workspace/ani-resource-service-runs/instance-cpu-mvp-20261015/private/refresh-session-token.py'
```

脚本核对 cluster/Namespace/SA UID，只申请原 namespaced 权限的 token，以 API 返回的到期时间为准；不安装定时任务。TLS 证书到期前使用保存 CA/CSR 续签。普通用户按当前 Gov 正式合同完成租户密码登录，沿用 INSTANCE/IMAGE 当前角色和套餐授权。实验租户名 `instance-lab-v15`、账号 `instance-lab-user`；`POST /api/v1/auth/password/login` 接受 `tenant_name`、`username`、`password`。密码位于 Fedora 原任务根 `private/instance-lab/tenant.password`，只通过受管私有渠道读取；无需管理员账号。实际镜像 ID、运行结果、有效期和精确快照统一见 [CPU 当前状态](../execution/status.md#cpu-instance-backend-20261015)。

## 本任务隔离实验的恢复

固定远端根为 `/home/chabking/workspace/ani-resource-service-runs/instance-mvp-v15-20261013`。私有配置与运行入口在 `private/instance-lab/` 和 `private/lab-*.{py,sh}`；ID、源码哈希与实际结果见 [真实实验记录](../execution/records/INSTANCE-MVP-V15/live-lab.md)。PG/Redis 和 Harbor 项目保留，四个服务已停止。不要在恢复时删除数据库卷、角色密钥、原账或恢复标记。

1. 在持续 SSH 连接中运行 `bash <根>/private/lab-session.sh`。Fedora 最后一个 SSH 退出会停止 user manager；该入口仅保持一个 20 分钟任务会话，退出时停止本任务四个 unit，不修改账号 linger 策略。结束可创建 `private/instance-lab/session.stop`；再次运行前移除这个任务停止标记。
2. 在另一条 SSH 连接中运行 `python3 <根>/private/lab-prepare.py` 刷新实验 Namespace 的 SG Token，再运行 `python3 <根>/private/lab-configure.py`。Token 有效期 7200 秒；任务 TLS 证书为 30 天，过期前使用保存 CA/CSR 续签，不能禁用校验。Resource/Acc 复用保存的受管集群凭据，SG 只用 namespaced SA。
3. 运行 `bash <根>/private/lab-services.sh start all`；查询或停止使用 `status all` / `stop all`。unit 名为 `instance-mvp-v15-lab-{acc,gov,resource,sg}`，全部监听随机冻结 loopback 端口，并分别限制 CPU/内存；日志仍只保留在私有目录。不能重用共享服务端口或停止其他 unit。
4. `private/lab-api.py captcha` 返回正常登录挑战，使用图中答案调用 `login <答案>` 或 `tenant-login <答案>`；密码与 token 由驱动读取/保存到私有文件。`query` 验证当前租户权限→Gov→Resource 的真实空列表；`register` 重放原实验集群/池登记键，不能生成新键来掩盖未知结果。驱动是 API 客户端，不写产品操作或完成状态 SQL。

五域分别使用 `lab_{gov,acc,net,image,inst}_{owner,runtime}`，每个角色有独立凭据；runtime 非超级用户、非 owner、没有 owner 角色成员资格。新实验首次准备由 `lab-db-prepare.sh` 调用受控迁移及官方 Gov CLI；已有数据库凭据修复/配置恢复使用 `lab-db-credentials.py`，不得把 owner DSN 配给应用。Harbor `init-platform` 已由生产 Image 生命周期完成，只建立本任务 InstallationID 对应的项目；当前没有 GPU 工作镜像或 publisher credential，后续需要实际镜像合同和产品凭证验收。

## 恢复与回退

- 重启后由同一受限 PG、原冻结 spec 和唯一 GPU execution、待定 mutation 和 lease epoch 恢复；普通 Stop/Start 不重占额、不重新解析镜像或 Namespace，不更换 PVC。
- `CREATE_RESULT_UNKNOWN` / `WORKSPACE_CREATE_RESULT_UNKNOWN` 保留原待定动作与原账。404、等待和重启不能清除它；只有原归属对象或可归因的确定外部结果才可推进。未实现绕过该规则的管理强制完成接口。
- 旧 worker 结果只能写入所属原操作的 receipt，不能覆盖新业务意图；过期 lease 拒绝回写。版本冲突后重新读取当前状态，不能重试旧 Start。
- `QUOTA_RETURN_PENDING` 使用持久 outbox 中的原事件、原 CREATE、resource 与 reason重试。Gov 已入账但回执丢失也不能生成新事件。PVC 删除失败时允许 GPU 已归还，整体删除仍保持未完成。
- `WORKSPACE_OBSERVATION_FAILED` 保留磁盘重试，计算已清理时仍可归还原 GPU quota。`WAITING_FOR_WORKSPACE_RECLAIM` 表示 PVC 已消失、原 PV 尚未回收；只有回收核验通过才完成整体删除。不通过手改状态、移除存储 finalizer 或直接删 PV 解阻。
- 回退前保存共享服务原配置、镜像、证书引用和数据库迁移点；恢复原启动配置可禁用新接入，但不能删除实例账本、待定动作或退额 outbox。已有接受请求未收敛时，保留 owner 执行与可靠退额能力。不得删除共享 Namespace、租户 Image pull secret 或其他任务对象。

本任务快照、测试二进制、私有运行配置与文件哈希按当前状态记录的远端目录保留。临时 fixture PG/Redis 由标签核对后自动清理；隔离实验的持久 PG/Redis、卷和角色密钥保留用于恢复。CPU 基础设施探针已按原 UID 回收，资源清单记录留存；真实 GPU 产品对象必须通过产品删除并核对 compute、专属 PVC 与账本，再清理对应任务 SA/Namespace。共享平台组件缺失时记录准确断点和最小接入条件，不安装设备插件或重建集群来扩展本任务。
