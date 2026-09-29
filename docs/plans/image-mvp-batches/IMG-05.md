# IMG-05 · 登记目录与固定 Digest 使用

目标：实现已登记版本目录，不扩成Harbor完整资产管理。

依赖：IMG-02, IMG-03。预计 1～1.5 人天，含开发自测。

所有新增脚本、Make target和测试名均为本批待实现目标，不是声称仓库已有。先在本地编码/提交/推送；所有生成、格式化、编译、数据库、容器和测试只在 Fedora 按精确SHA执行。

## 小任务

| ID | 工作 | 文件/方法与动作 | 可验证完成条件 |
|---|---|---|---|
| IMG-05.1 | 登记事务 | Catalog.RegisterImage + ApplyTenantRegistration | 先归属检查再Harbor查询；结果Digest不可变；同key重放不重新跟Tag |
| IMG-05.2 | 查询详情 | GetImage；FindTenantRegistration/FindPlatformRegistration | 跨tenant404、platform只读；能显示自己的取消记录但不能用于Resolve |
| IMG-05.3 | 分页筛选 | ListImages/NewCursorCodec/EncodeCursor/DecodeCursor | filters在LIMIT前；两页匹配无漏项；跨tenant/scope/filters的cursor拒绝；search转义 |
| IMG-05.4 | 元数据修改 | UpdateImage + version CAS | 只允许四类元数据；请求不能改digest/owner；用途不等于适配认证 |
| IMG-05.5 | 取消登记 | UnregisterImage；软取消和幂等 | 不调用HarborDELETE；历史任务意图不变；同内容重新登记产生新image_id |
| IMG-05.6 | 运行解析 | Runtime.ResolveImageForWorkload | 按digest验证当前存在和target platform；拒绝取消/他tenant；503不得抹除记录 |

## 执行顺序与退出门禁

按表中小任务顺序实施；可在一个源码提交中完成紧密相关的小任务，但每个ID都要有独立完成证据。契约与生成检查、定向单测应先于真实依赖测试。失败先修复本批，不通过删测试、改成skip、用mock冒充真实依赖解决。

每个小任务记录：实际文件/方法、输入和结果SHA、Fedora命令及退出码、日志路径/hash；不包含Secret。当前进度只更新Resource现有 `docs/execution/status.md` 的Image区块；历史结果放对应run records。

未配置Harbor/真实集群/普通容器owner/前端时分别记明确blocker；不阻塞不依赖该项的开发与隔离测试。批次的code/isolated门禁通过不自动等于live或product通过。依赖执行判定见主计划，不能把一个blocked任务改写成不需要实现。

本批可验收部分完成后自动选择下一个依赖满足的批次；没有可推进批次或触及未授权写操作时，报告阻塞和恢复入口。不得为等待用户逐批发送“继续”而主动停在已经通过的普通批次。
