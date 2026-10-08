# IMG-01 · 契约和纯领域门禁

Resource code SHA `109ddcc8916fbee2ca5fdcf77e0f38c8cbef30b2`，Governance code SHA `f535b64d7da25061f5742befdcdfe52c5aef1615`，均已推送 `codex/image-mvp-20260930`。

- IMG-01.1：Resource 11 租户方法、2 内部方法；Fedora Buf lint/build/generate 通过，固定生成物和格式回传 manifest 在本目录。
- IMG-01.2：Governance catalog 安全 DTO + 11 HTTP 契约；暂存目录定向生成，公开契约测试拒绝 tenant/namespace/admin 字段，secret 仅限签发/重置。尚无 HTTP 实现或部署。
- IMG-01.3：纯 biz 类型、RuntimeImages/SecretCipher 和所需数据端口；4 个真实 Image 分层违规 fixture 被拒绝。
- IMG-01.4：TestValidationMatrix / TestMetadataAndFilters，错误 host/project/ref/UUID/枚举/长度等反例通过。
- IMG-01.5：TestCallerFailClosed / TestSecretRedactionAndErrorReason，通过匿名、错误身份、跨租户、空 tenant 平台旁路及通用编码泄密反例。
- IMG-01.6：两仓均从已推完整 SHA 生成，经白名单/base hash 审核回传提交；新 SHA 再生成无差异。

[Resource 完整 verify + 定向 race](IMG-01/109ddcc8916fbee2ca5fdcf77e0f38c8cbef30b2-verify-short-tmp/output.txt) exit 0；[Governance 生成一致性和契约测试](IMG-01/gov-f535b64d7da25061f5742befdcdfe52c5aef1615-verify/output.txt) exit 0。所有命令 Fedora，历史重锁，CPU 200%/2300M/0 swap；命令 runner 在 runners。

原 Resource verify [exit 2](IMG-01/109ddcc8916fbee2ca5fdcf77e0f38c8cbef30b2-verify/output.txt) 保留：已有 Unix socket 测试在长 run TMPDIR 失败。[未改代码的基线复现及短 TMPDIR 对照](IMG-01/socket-path-repro/output.txt) baseline long exit 1，baseline/candidate short exit 0。使用本 run 的 `/home/chabking/.im-1718` (0700)，未修改旧测试/产品，也未使用系统 /tmp；记录的修复后验证仍为同一完整 code SHA。

此批纯契约/规则的 live/product 为 n_a；Harbor、DB、业务入口并未由此通过。普通 make verify 的 PostgreSQL case 没有 DSN 会 skip，不代替 IMG-02 真实 PG 或最终 integration/race/tenant-mutations。未创建 live 资源；远端源码、缓存、短 TMPDIR 留作本 run 后续验证。后续文档提交不改变上述 code SHA。
