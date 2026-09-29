# IMG-02 · 独立数据域和真实 PostgreSQL

验证代码 SHA `7e4069d655b6fd723b4d7d9803e21804d945c89e`，已推送 review 分支。所有生成、格式化、测试在 Fedora；[完整命令与日志](IMG-02/7e4069d655b6fd723b4d7d9803e21804d945c89e-verify/output.txt)，exit 0，2026-09-29T17:56:43Z—17:58:48Z。[runner](runners/resource-round02.sh) 使用既有重锁、CPU 200%、MemoryMax 2300M、swap 0、任务缓存和短 TMPDIR。

| Task | 实现和证据 |
|---|---|
| IMG-02.1 | `migrations/image` 独立嵌入流；真 PG 首建/重复执行/checksum 篡改拒绝；保护路径 git diff exit 0 |
| IMG-02.2 | `ApplyImageMigrations`、`CheckReady`、`-image-migrate`；`TestRuntimeRoleAndMigration` 验证 DDL/TEMP/SET owner/跨域访问/RLS/版本表写入拒绝；原 Network migrate 未改变 |
| IMG-02.3 | 独立 sqlc entry 和4个查询源；全部 tenant/platform 显式作用域；新 SHA 重新生成无差异，边界 fixture 通过 |
| IMG-02.4 | Space reserve、Command 状态 CAS、目录与幂等结果同事务；真实 PG 验证并发 slug/CAS、重放、分页筛选、提交失败回滚、凭证代次和可用性 |
| IMG-02.5 | `scripts/image-integration` 真 PG 18.6，固定镜像 digest、任务私有 DSN 文件和角色/库；7 个 imageintegration 测试全部执行且 race 通过，无 skip |
| IMG-02.6 | 隔离 git archive 副本削弱租户谓词/复合 FK，分别命中 `TestTenantQueriesAndAtomicCatalog`、`TestTenantCompositeFKAndPlatformFK` 断言 exit 1；runner exit 0；原2个输入文件 hash 未变 |

通过：定向 race、真实 PG + race、2项 mutation、make verify（生成/边界/tidy/unit/vet/build/module/diff）。普通 verify 仍不是全量 Network PG 测试；既有 integration/race/tenant-mutations 保留，最终批次重跑。

PG 资源限制实测 memory=805306368、memory_swap=805306368、NanoCPUs=1000000000。容器 `623877c92e7bcb857d4dc85a66718b112ccd645919aa6565865c2577eebb62e8` 和 `612209db30d1496310fbae07613139e4dac8106600ee6232a397b15de121cece` 均由 ownership label 核对后删除；结束后 docker ps 空。未访问共享 DB/Harbor/集群。此批 live/product 为 n_a，不能据此宣布真实 API 或产品闭环。
