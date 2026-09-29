# Image MVP 开发计划

本计划由12批、71个小任务组成。详细文件和目标方法见各批及 [方法清单](../specs/image-methods.md)。任务编号静态不复用；当前状态只在现有 `docs/execution/status.md` 的Image区块。

## 1. 顺序与工作量

| 批次 | 交付 | 默认依赖 | 人天 |
|---|---|---|---:|
| [IMG-00](image-mvp-batches/IMG-00.md) | 基线、接入点与 Fedora 执行边界 | — | 0.5～0.75 |
| [IMG-01](image-mvp-batches/IMG-01.md) | 契约、领域类型与纯规则 | IMG-00 | 0.5～0.75 |
| [IMG-02](image-mvp-batches/IMG-02.md) | Image schema、迁移与显式租户数据层 | IMG-01 | 1～1.5 |
| [IMG-03](image-mvp-batches/IMG-03.md) | Harbor 与 Secret 适配器 | IMG-01, IMG-02 | 1～1.5 |
| [IMG-04](image-mvp-batches/IMG-04.md) | 空间、凭证与失败恢复 | IMG-02, IMG-03 | 2～2.5 |
| [IMG-05](image-mvp-batches/IMG-05.md) | 登记目录与固定 Digest 使用 | IMG-02, IMG-03 | 1～1.5 |
| [IMG-06](image-mvp-batches/IMG-06.md) | Resource 安全入口与 Governance 适配 | IMG-01, IMG-04, IMG-05 | 1.5～2 |
| [IMG-07](image-mvp-batches/IMG-07.md) | 平台维护命令与 API 技术闭环 | IMG-04, IMG-05, IMG-06 | 0.5～0.75 |
| [IMG-08](image-mvp-batches/IMG-08.md) | 一种普通容器创建方接入 | IMG-05, IMG-06 | 1～1.5 |
| [IMG-09](image-mvp-batches/IMG-09.md) | 最小前端一页闭环 | IMG-06 | 1.5～2 |
| [IMG-10](image-mvp-batches/IMG-10.md) | 联合验收、隔离反例与最终回归 | IMG-07, IMG-08, IMG-09 | 1～1.5 |
| [IMG-11](image-mvp-batches/IMG-11.md) | 交付与自动推进收尾 | IMG-10 | 0.5～0.75 |

基础合计12～17人天，加3人天缓冲，以15～20人天为目标。这个预算仍以Harbor/现有owner/前端组件/租户namespace可复用为前提；缺失不通过新造服务强行塞进预算。文档内的小任务是验收分解，不是每个都需要独立半天会议/审批。

## 2. 自动推进和门禁等级

每批记录 `code`（实际实现）、`isolated`（Fedora单测/真DB/协议fixture）、`live`（真实Harbor/集群）、`product`（真实产品入口）四个状态，允许 N/A 但必须写原因。

IMG-00 baseline完成后，即使live环境缺失，IMG-01～06可按前一批**code+isolated**通过继续。IMG-07可先完成CLI/脚本/隔离测试，真实Harbor验证等待批准环境。IMG-08依赖实际owner绑定；IMG-09目录页可独立于owner完成，但09.6依赖08实际接入。IMG-10的真实产品验收要求07/08/09相关live输入齐备，不能仅靠它们的code通过。IMG-11可以先准备报告，但只有完整产品门禁通过才写MVP完成。

默认按编号连续执行。某一环境/仓库绑定阻塞时，保留blocked记录，继续下一个依赖满足的**独立**任务，不能跳过依赖编写“假成功”。用户无需每批确认；自动推进仅在当前明确授权、数据范围和工具会话内成立，不是无人值守后台作业承诺。

## 3. 三个里程碑

- M1：IMG-01～06通过。API/数据/自动初始化逻辑具备，定向和隔离验证通过；未连接真实Harbor时不能宣称自动Push链路已跑通。
- M2：IMG-07真实验证、IMG-08窄端口测试通过。可以验证Harbor→Kubernetes技术链路；若Pod由验收driver而非产品owner创建，只标技术闭环。
- M3：IMG-08/09/10实际产品验收通过，IMG-11完成审阅交付。才是最小产品MVP。

## 4. 测试与执行入口

新增的建议Make入口：`image-unit`（Image纯逻辑+adapter fixture）、`image-integration`（真PG角色/租户/幂等/密文）、`image-tenant-mutations`（真负例有效性）。这些由相应批次实现和验证，不提前假装存在。真实依赖驱动 `scripts/image-smoke`、`scripts/image-product-acceptance` 只读取已批准live profile，不自动识别并接管任意集群。

定向门禁各批执行；最终同时保留原仓的 `make verify`、`make integration`、`make race`、`make tenant-mutations`，增加Image覆盖但不改原命令意义。Governance/owner/frontend按IMG-00发现的原有命令执行，新增命令要在文档登记后才调用，不凭空写出不存在的CI脚本。

失败修复不跨出本模块去做泛化重构。发现既有失败先在干净基线复现并保留证据：若与Image无关，可继续独立代码工作但最终不可声称全量PASS；真实阻断须记录处理授权，不降门禁。

## 5. 跨仓交付策略

Resource先提交Proto与实现；Fedora生成后本地提交生成物并推送review分支。Governance只依赖实际存在、已推送的Resource契约SHA/pseudo-version；不存在的版本不能先写死。禁止Go workspace/local replace作为长期集成方式。普通容器与前端也按各自review分支执行。

每仓唯一代码作者路径仍在本地；Fedora只按SHA检出、生成和验证，不手改业务源码、不提交/推送。生成物带基线hash回传本地，审核范围后形成正常新提交，再把新SHA送Fedora验证。

## 6. 范围保护

不读/改ANI monorepo，不整仓迁移tx7do/框架，不重建IAM/租户/配额，不把Image变成通用资源调度器，不复制Network worker，不增加RLS。任务要求的“自动化”是Project/受限Robot/有限幂等恢复，不是引入工作流系统。

小任务没有填写真实文件绑定或证据时就保持未完成。禁止为了“每批自动往下走”增加自动merge、全局清理、删除日志、忽略非零退出码或将真实测试替换为mock。
