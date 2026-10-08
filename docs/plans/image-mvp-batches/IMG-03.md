# IMG-03 · Harbor 与 Secret 适配器

目标：只包装本闭环所需API，不导入完整Harbor管理SDK或复制它的业务实现。

依赖：IMG-01, IMG-02。预计 1～1.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-03.1 | 固定上游客户端 | NewHarbor/doJSON/mapHarborError | httptest覆盖TLS失败、跨host redirect拒绝、超时、响应上限、403/404/409/5xx与脱敏 |
| IMG-03.2 | Project适配 | GetProjectByID/FindProjectByName/CreatePrivateProject | 实版OpenAPI锁定metadata.public字符串、Location ID；不用虚构ownership自定义字段 |
| IMG-03.3 | Robot适配 | FindOwnedRobot/CreateRobot/GetRobot/SetRobotSecret/SetRobotDisabled | 确认RefreshSec实际HTTP method/path；指定Secret成功不依赖回显；找同名必须验证ownership描述和权限 |
| IMG-03.4 | 权限构造 | ValidateRobotPermissions；固定publisher/pull项目权限模板 | 断言无system权限、无CoverAll、无delete；publisherPush必须带Pull，返回用户名不硬编码前缀 |
| IMG-03.5 | 制品解析 | ResolveArtifact/GetArtifactByDigest/ReadRunnablePlatforms | tag解析后再次按digest读取；manifest/index正例；chart/附件/深度/超大/平台未知反例；不下载layers |
| IMG-03.6 | 加密与日志保护 | NewAESGCMKeyring/Seal/Open；typed secret envelopes | TestCipherWrongAAD/Tamper/KeyMissing；Secret哨兵不出现于日志、错误、command.result/candidate |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。
