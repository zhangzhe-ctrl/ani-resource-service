# ANI 镜像 MVP：架构、契约与逐批实施包 v1.0

日期：2026-09-30。**这是待实施设计和任务包，不是已运行代码或验收报告。** 本次只读取仓库并编写材料；没有修改远端仓库，没有连接用户 Fedora，没有执行生成、编译、测试、数据库迁移或集群写入。

## 交付目标

在 `ani-resource-service` 的同一 Go module、同一服务进程内增加 Image 领域。闭环为：

**ANI 自动开通租户镜像空间 → 租户直接 Push Harbor → ANI 登记固定 Digest 镜像 → 一个现有普通容器业务选择镜像 → 以租户只读身份实际拉取运行。**

平台公共镜像仍为私有 Harbor Project 内的平台共享资产。所有日常操作通过 ANI 或受控管理命令完成，不开放 Harbor 管理页面，不要求逐租户人工配置。

## 阅读和落库顺序

| 材料 | 作用 |
|---|---|
| [架构规格](../specs/image-mvp.md) | 范围、信任边界、关键流程、重试及凭证方案 |
| [API 规格](../specs/image-api.md) | 每个 RPC/HTTP 方法、字段、权限、错误及幂等 |
| [数据库规格](../specs/image-data.md) | 独立 schema、无 RLS、约束、查询、迁移与运行角色 |
| [方法与文件清单](../specs/image-methods.md) | 新增方法、归属文件、现有代码接入点 |
| [开发计划](image-mvp.md) | IMG-00～IMG-11、依赖、退出条件、预算 |
| [批次明细](image-mvp-batches/IMG-00.md) | 从第一批开始，每批已拆小任务和测试 |
| [Fedora 执行手册](../runbooks/image-mvp-fedora.md) | 本地编辑/推送，远端生成/验证，精确源码与安全边界 |
| [测试矩阵](../specs/image-acceptance.md) | API、DB、Harbor、Secret、真实业务负向验收 |
| [决策记录](../adr/0007-image-domain-and-private-registry.md) | 已确认范围及本方案工程取舍，避免实施时扩展 |
| [来源清单](image-mvp-sources.md) | 已核对代码、主源文档、未知项 |
| [GOAL](image-mvp-goal.md) | 一次交给执行 Agent，按依赖连续推进的提示词 |

`contracts/` 是本方案的**目标接口、DDL 和查询起稿**，不是可盲目覆盖仓库的补丁。Proto/sqlc/DDL 尚未经过 Fedora 生成和真实 PG 验证，执行批次必须补齐。签名、字段号和 SQL 约束是明确设计输入；只有实际基线发生冲突时才在对应批次记录兼容调整。

## 已冻结的阅读基线

- Resource：`zhangzhe-ctrl/ani-resource-service@a4ca2a0fcb18346fff27f682a5244874ca4c60c8`。
- Governance：`zhangzhe-ctrl/ani-governance@b7c249e4b420cbab78a2e8979eab874aba5ffe82`。
- Harbor API 参考：上游 `v2.15.0`。**不是对现网版本的断言，也不要求升级到该版本。** IMG-00 对照实际版本/OpenAPI。
- 未读取 `zhangzhe-ctrl/ANI` 的代码，不把它加入改动范围。
- 普通容器创建方和前端所属仓库：本次在已核对的非 ANI 来源中**未确认**。IMG-00 绑定真实仓库、提交、文件和方法；找不到时如实阻塞相关批次，不新造 Compute 服务，不改用 KServe 推理流程冒充普通容器，也不把验收驱动创建的 Pod 称为产品接入完成。

## 放入仓库时

把 `docs/specs/`、`docs/plans/`、`docs/runbooks/` 的新文档放到对应目录；在原 `docs/START-HERE.md` 增加链接。决策材料按下一可用 ADR 编号正式落盘，不覆盖已有 ADR。`contracts/` 先作为设计输入；IMG-01/02 将其迁入真实源码目录并建立链接，随后不再维护两份正式契约。

只在现有 `docs/execution/status.md` 增加 Image 区块，保留原 Network 内容；每批历史证据在 `docs/execution/records/IMAGE-MVP/<run-id>/`。`batches.json` 是静态任务依赖，不是第二份当前进度。禁止把本包的模板覆盖现有执行账本。

## 预算与验收层次

现有 Harbor、普通容器创建方、租户 Namespace 与页面组件可复用时，基础工作约 12～17 人天，加 3 人天缓冲，仍以 **15～20 人天**为目标。未就绪的基础设施、新建业务服务、独立开发前端框架不在此预算内。

**技术闭环通过**与**产品 MVP 完成**分开：真实 Harbor+受控 Kubernetes 驱动可以证明基础链路；只有真实产品创建入口与页面也完成相应验收，才标记产品 MVP 完成。
