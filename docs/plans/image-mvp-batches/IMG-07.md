# IMG-07 · 平台维护命令与 API 技术闭环

目标：不开放Harbor页面，先证明真实自动开通/Push/登记链路。

依赖：IMG-04, IMG-05, IMG-06。预计 0.5～0.75 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-07.1 | 受控管理模式 | -image-admin互斥解析；Platform.InitializePlatform/RegisterPlatformImage等 | 只用operator运行配置；不开放平台HTTP/RPC写入口；未知action失败 |
| IMG-07.2 | 平台镜像发布 | issue-platform-publisher + 0600输出；平台维护登记/更新/取消 | 发布仅platform；不以admin配置给普通用户；不操作未授权现有platform |
| IMG-07.3 | 测试部署配置 | deployments/image/配置说明/Secret引用、management入口隔离 | 不提交明文Secret；image关闭回归；不强制更换现有密码/全局网关 |
| IMG-07.4 | 真实Harbor探针 | scripts/image-smoke 的init/push/register/resolve阶段 | 两个run-owned空间自动创建；客户端直接Push；根Digest与登记一致；A凭证访问B失败 |
| IMG-07.5 | 故障证据与状态 | sourceSHA、run-owned IDs、脱敏输出、退出码 | 环境缺失标记live blocked；可完成脚本和离线测试但不得记真实闭环通过 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。
