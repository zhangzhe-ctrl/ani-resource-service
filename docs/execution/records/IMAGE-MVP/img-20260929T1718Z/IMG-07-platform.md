# IMG-07 平台管理与部署配置子门禁

源码 `8b7a9f8932aad1194b7079f9c5f6dfa84b93cbe1` 经 Fedora 生成/格式化返回10文件；本地验证白名单、原文件和新文件SHA256后提交 `2b6a0998827e655a6164eca104d4813c0ce2c0ac`。该代码SHA的[完整门禁日志](platform/evidence/2b6a0998827e655a6164eca104d4813c0ce2c0ac-verify/output.txt) exit 0，2026-09-29T20:34:48Z—20:37:57Z，SHA256 `03db16dbffda12d31c3e1d9564b6af61017d827641d5977bc1c2067ae20b9457`。归档包SHA256 `d4c7f0e568b2774fb421ab0a650202342244153da1081488587061e1256bd437`；[逐文件清单](platform/SHA256SUMS)包含生成manifest、实际runner及supervisor记录。

IMG-07.1：同一二进制 `image_admin.go` 提供固定8动作，互斥其他管理模式；只从私有 operator/runtime 文件建立平台身份。严格JSON拒绝未知、重复字段或额外对象。`biz.Platform` 单独注入 `PlatformRepository`；租户生命周期无平台写入端口。平台SQL显式限定scope/tenant，固定平台事务锁、CAS及command快照保留幂等与丢响应恢复。

IMG-07.2：初始化不自动签发publisher；签发/显式轮换仅platform push/pull。Secret仅写operator限定目录的新0600文件，拒绝替换/符号链接/路径逃逸；stdout只有元数据。平台登记固定Digest，更新有CAS，取消只改登记。`TestPlatformLifecycleCatalogAndTenantBoundaries` 用真实PG验证平台/租户互相不可写、两类凭证权限、密文交付与过期擦除、旧代次重放、tag移动后command重放及跨actor拒绝。`TestPlatformLostProjectAndRobotRecovery` 验证未知Project阻塞和显式ID/证据恢复、Robot响应丢失不重复创建。Harbor侧是受控协议fixture，不能代替live。

IMG-07.3：`deployments/image/` 说明独立Image配置/数据库迁移、私有Secret文件、Governance精确受信入口、实际证书SAN、权限catalog无自动授予、8动作操作与回退边界。示例默认关闭；没有部署到任何共享环境。

实际命令见归档runner：新SHA `make generate` 无diff；定向race；全部Image真实PG/race（含13个进程退出/重启场景与新增平台用例）；三个mutation（租户SQL、复合FK、平台写scope）各自必须失败，再恢复生产字节；完整 `make verify`（测试/vet/build/依赖/边界）；旧Network迁移/API/README/AGENTS/sqlc保护路径diff。均pass。

Fedora使用既有flock、CPU200%、MemoryMax2300M、swap0与短TMPDIR，scope `run-p834381-i9215102.scope`。PG容器 `aa83b17289c2586143442c5be0c41efb8631b69e3bf15cf6e2b0bc3c5f8da2ff`、`ee6bd07fe8281249e527c08e1023027bfe01ae33e63579b04ffde591ec60f18f` 均按ID/ownership清理，归档inventory为空。

这份记录仅关闭IMG-07.1—.3的code/isolated门禁。IMG-07.4真实Harbor驱动仍待实现/验证；approved live profile与普通容器owner仍缺失，不代表IMG-07 live或产品链路通过。
