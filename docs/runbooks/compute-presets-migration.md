# 算力目录迁移与恢复

本手册用于匹配 Gov/Res 的受控维护。行为合同见[算力目录](../specs/compute-presets.md)，当前结果只见[执行状态](../execution/status.md#compute-preset-resolution)。目标必须先核对集群、库、owner/runtime、进程、镜像和任务样本，不能从示例或默认 DSN 推定。重任务执行位置遵守[远程规则](../remote-execution.md)。

## 发布与维护前置

本手册的目录元数据迁移本身保留实例。当前容器配置去版本的009切换须先完成[实例运行手册](instance-mvp-fedora.md)中单独的清空开发实例前置；二者不能混同。

先完成正式 Res API 发布、Gov 精确锁版、两仓真实协议/身份/PG持久主链验证、独占库正逆向演练及匹配镜像。API 模块发布供编译消费；删除旧 RPC 的 Res 不能提前部署到旧 Gov。

迁移前通过真实业务入口准备归属明确的 CPU/GPU 样本，覆盖运行、停止、待投递/ACK和清理中。保存 Notebook/Pod UID与实际资源、冻结载荷/hash、GPU Execution/Display/分配、operation、原账、outbox及借用卷UID。已有实例没有因目录改名而清理或重建的要求。缺少存量或真实 GPU 证据时必须记录 NOT_RUN。

数据库变更前保存：原实表 OID、完整 ID/内容/审计摘要、PK/UNIQUE/CHECK定义与 convalidated、索引、默认值、owner/ACL、当前完整 instance 历史001..009 checksum。以只读 catalog 核对实际附属对象和外部依赖。未知版本、外部FK/view/trigger/RLS、命名冲突或owner不符明确失败，不猜测清洗。

实际入口隔离和恢复必须验证数据面：Service/Endpoints/EndpointSlice 正确不等于 NodePort 已更新。目标环境曾因停止 Notebook 的长 Service/port 名组合触发 kube-proxy nftables `comment too long, 128 characters maximum allowed`，整批同步失败、NodePort 保留旧后端；该行为与[Kubernetes #139515](https://github.com/kubernetes/kubernetes/issues/139515)一致，[上游修复](https://github.com/kubernetes/kubernetes/pull/139516)不能推定已包含于现有镜像。维护前核对实际同步错误，恢复时用新连接和真实鉴权请求确认后端；不能把401当隔离成功，也不能修改已正确的Endpoint或反复修补会被Notebook控制器回写的Service。涉及其他组件的升级/部署应另行取得明确授权，不因数据库已迁移而自动扩大范围。

## 正向切换

1. 关闭 Gov 新 CREATE 受理，停止新的规格写入；排空或可靠持久化在途请求及 worker。正常停止访问旧 SQL/RPC 的 Gov 和 Res 进程，等待已发起操作的持久事实确定。维护期间保留已有 Notebook/Pod，不变更其资源/Namespace/GPU。
2. 在启动服务或恢复 worker 前启动连续收证：记录待投递→ACK、清理→outbox→原 CREATE 退款的转换，不能恢复完后只查终态。
3. 使用独立 owner 的受管私有 DSN 文件显式执行新 Res：

   ```bash
   ANI_INSTANCE_OWNER_DSN_FILE=/managed/instance-owner.dsn \
   ANI_INSTANCE_RUNTIME_ROLE=resource_runtime \
   ./ani-resource-service -instance-migrate
   ```

   路径/角色是示例，替换为已核对目标；不将凭据写入命令、日志或源码。该入口先校验/补齐 instance 历史，再执行独立 compute 阶段。已有版本只验 checksum，不重放004，不禁用GPU、不改实例或审计内容。空库先完成历史链再移动实表。
4. 在独立 Gov 事务执行其正式 `migrate-compute-preset-permissions.sql`，按原 path/method 保留 API ID、五码保留 permission ID、角色与关系、启停及限制。不得重复首次播种或自动补权。按 Gov 正式机制刷新所有策略进程。记录两个数据库各自的实际提交状态，它们不具备跨库原子性。
5. 启动新 Res，再启动匹配新 Gov；核对 compute/instance readiness、新目录/管理权限、旧实表/旧RPC不存在及存量抽查。确保旧SQL/RPC进程全部停止后，才恢复受理并进行真实容器验收。

正向迁移在显式事务内有5秒锁等待和45秒语句期限，与原 instance 显式迁移使用同一 advisory lock。`ALTER TABLE SET SCHEMA/RENAME` 移动同一关系及附属对象，不复制行，不删除重建约束、不强制VALIDATE。compute.schema_version 单独记账；instance.schema_version与历史文件原字节不变。

runtime 仅有 compute schema USAGE、presets 的 SELECT/INSERT/UPDATE/DELETE及schema_version SELECT；无schema CREATE、owner或迁移元数据写权限。普通启动只检查readiness，不执行DDL。最终唯一实表是 compute.presets，附属对象去除compute_presets旧前缀；无旧名表/视图/别名/search_path回退/双写。

## 按提交状态恢复

事务未提交：该事务回滚，检查原对象/历史和匹配旧版本后恢复。Res或Gov已有一库提交：停止新写入及新旧进程，依据两库记录选择继续完成或分别逆向恢复；不得只退旧二进制。

Res 已提交且决定退回时，使用新候选二进制的显式入口：

```bash
ANI_INSTANCE_OWNER_DSN_FILE=/managed/instance-owner.dsn \
ANI_INSTANCE_RUNTIME_ROLE=resource_runtime \
./ani-resource-service -compute-restore
```

它验证正向checksum和全部原instance历史，再将同一表及附属对象改回instance旧名，删除本次compute账本/schema，不删除数据。DROP SCHEMA 不使用CASCADE，意外对象会导致整笔恢复回滚。恢复前须停止所有新旧读写者，并持有匹配旧 Gov/Res 版本。

Gov 使用配套 `restore-instance-preset-permissions.sql` 逆向恢复原code和owner，保留ID、关系与状态并刷新策略。两库均验证成功、旧版本readiness及受理/清理恢复检查满足后才开放受理。失败时保持受理关闭，记录具体已提交库及错误，不让旧二进制访问已改名表。

独占库须覆盖 fresh、已有库升级、重跑、锁超时、目标冲突、checksum错误、事务中途异常、runtime权限及正逆向恢复；另演练两库部分提交的协调恢复。真实环境不重复故障注入。源码/PG/RPC与真实Pod/GPU证据分别报告，新建成功不能代替存量无回归。
