# IMG-06 Resource 安全入口子门禁

源码 `af8f21ed107b94a592fadf64c2fba44b483686f1` 经 Fedora Buf/gofmt 返回15文件并按原文件 SHA256 验证后，提交 `71aa986078dfb64388d783a7b2de01b7a94b0027`。[新 SHA 完整门禁](IMG-06/71aa986078dfb64388d783a7b2de01b7a94b0027-verify/output.txt) exit 0，日志 SHA256 `831657e113640fa5302b61b65e9dc1eb20e15f69a1a470a031fe058395ece510`，2026-09-29T18:56:29Z—19:00:05Z。

IMG-06.1：`TenantService` 的11方法与 `wire*` 仅转换；`rpcError` 给出 `image.ani.io` ErrorInfo，未知错误不泄露来源字符串。协议未知枚举在用例前拒绝，凭证元数据没有 Secret 字段。内部 RuntimeService adapter 已实现但未注册 listener，不能算创建方接入。

IMG-06.2：Bootstrap仅追加image=3；独立typed配置、私有文件读取、Image PG readiness、Harbor/cipher/catalog/lifecycle装配。disabled不读新文件/不连接；启动不请求Harbor。仅 `runGovernance` 注册租户Image RPC，保留Network字段/SAN/旧迁移。精确11方法白名单与可信caller桥接。`TestGovernanceMTLSBoundary` 使用实际TLS/gRPC连接同时覆盖Image和原Network的正确JWT/AK actor、无证书、错误SAN/用途、重复身份头及跨tenant反例；`TestImageGovernanceAllowlistAndScope` 覆盖所有方法和内部/平台/空tenant拒绝。日志用框架Redacter，测试确认原请求到达handler但请求/凭证reply的sentinel不进入日志，Network日志行为保留。

命令：重新 `make generate` 后clean、Image/server/conf/composition/service定向race、全部Image真实PG+race及两个租户mutation、完整make verify、旧Network保护路径diff。均pass。既有重锁、CPU200%、MemoryMax2300M、swap0，短TMPDIR；scope `run-p749708-i9143483.scope`。两个PG容器 `c24f919a50ac06b2d38349302b60f70d6171bdb365c4840cd737cdf3826181b2` 和 `00b60a30df7842994eb14ace0514eed986816543c0ca401faad128b3f6b9f0b0` 均清理，docker ps空。

这是Resource子门禁，不代表IMG-06全部通过。Governance客户端/BFF/权限/HTTP链路仍在实施；无approved live profile或普通容器owner绑定，live/product继续blocked。
