# IMG-08 · 一种普通容器创建方接入

目标：只接一种已存在的普通容器业务，准确区分测试driver与产品功能。

依赖：IMG-05, IMG-06。预计 1～1.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-08.1 | 确认接入分支 | 检查IMG-00绑定owner repo/SHA/Create方法/tenant环境查询 | 没找到明确标blocked；不新造Compute、不把KServe接口硬套为普通容器 |
| IMG-08.2 | 受信运行端口 | 同进程RuntimeImages或NewImageRuntimeServer/ImageRuntimeUnary + client | 只选择真实部署需要的一条；独立服务用固定证书身份；secretRPC绝不进Gov公共白名单 |
| IMG-08.3 | Secret准备 | PrepareTenantImagePullSecret + GetTenantPullMaterial | 同namespace、owner label、generation、缺失修复；禁止任意namespace和节点admin兜底 |
| IMG-08.4 | 镜像意图 | ResolveRegisteredImage/PersistResolvedImageIntent/ApplyRegisteredImage | 创建意图固定Digest；原业务幂等重试沿用意图；字段不能被用户raw镜像覆盖 |
| IMG-08.5 | Pod模板与边界 | 现有创建方PodTemplate/serviceaccount/namespace路径 | imagePullSecrets+Always；init/sidecar若拉私有平台镜像也授权到位；不修改无关调度/网络/配额 |
| IMG-08.6 | owner集成测试 | TestImageSelectionOwnership/TestSecretOwnership/TestRetryUsesDigest | 真实产品API创建的容器使用登记镜像；未跑live先只记code/isolated pass，不记产品接入通过 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。
