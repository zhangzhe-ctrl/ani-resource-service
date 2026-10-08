# 列表总数：2026-10-08 验证记录

业务链为受信调用方 → 列表用例 → 真实 PostgreSQL → Resource 响应 → Governance HTTP DTO。原断点是所有 Resource 列表响应均没有 `total`，并非前端取字段失败。

## 固定来源与保护边界

2026-10-08 经 HTTPS fetch 和 ls-remote 确认远端最新 main：

- Resource：`8089a78d4f5937b8e47e1873bf0849d8ba97d5c5`。原本地 Image 分支为 `d32c4f438b43b8cb7534486c73e116e4d26c890a`，与 main 源码相同，但缺少合并提交。
- Governance：`9e4c5c7fa7a0dc16bdaf2afe6745e28414ba9105`。原本地 `refactor/quota-contract-convergence` 分支不包含最新 main。
- 两仓均从上述 main 建立任务独占 `codex/list-totals` 工作区；原工作区的 status.md、Image 清理记录和 Governance `_agent/` 均保留。

[Resource 源码清单](resource-source.sha256)覆盖代码、生成输入/输出、工具、测试及规格共 361 文件，清单 SHA-256 为 `536c90cefc2e750722f24e605b16dc4a319dd319ce8436842434b07106bc2bac`。
[Governance 源码清单](governance-source.sha256)覆盖代码、API、工具、测试及迁移共 2107 文件，清单 SHA-256 为 `da702d3b50be7ea902e4f1849f05ae1f0fc978e7baa21f06709ea1df1f7ddff2`。
两份清单均在远端逐文件核验，退出码 0；执行记录和日志不包含在源码清单中。

## 实现范围

| Resource 列表 | total 的统计范围 |
|---|---|
| VPC | 当前租户、name/state，默认排除 deleted |
| Subnet | 当前租户、vpc/name/state，默认排除 deleted |
| EIP | 当前租户可见的 public、tenant-managed EIP，name/state |
| LoadBalancer | 当前租户已受理产品记录、vpc/subnet/name/state/exposure |
| VlanNetwork | 当前集群、vlan 种类、name/state |
| EgressGateway | 当前集群、egress_gateway 种类、name/state |
| PublicAddressPool | 当前集群的 public 地址池、name/state |
| IntranetAddressPool | 当前集群的 intranet 地址池、name/state |
| NodeInterface | 授权后的节点筛选清单长度；此接口不分页 |
| Image | 当前 scope/tenant 的有效登记、search/purposes/accelerator |

Proto 均新增字段 3：`int64 total`，保留既有字段编号和游标。数据库列表总数和本页数据使用同一 RepeatableRead 只读事务；count 不应用 cursor/limit，空后续页仍可返回非零 total。业务规格分别在 VPC/Subnet、出网、LB 和 Image 规格维护。

Governance 目前对应公开列表只有 VPC、EIP、Image，三者均已补齐 DTO、透传和 OpenAPI。Network 客户端及其测试的 protobuf import 改为 Resource 模块；TLS SAN `ani-network-service` 和权限范围保留。

## 执行与结果

实际主机：`ssh ubuntu`（`i-8yg2l7u8`），任务根目录 `/home/ubuntu/workspace/ani-network-service-runs/list-totals-20261008/`。Go 1.26.7，Buf 1.60.0，sqlc 1.31.1；GOMAXPROCS=2、GOFLAGS=-p=2。数据库与 Redis 均为脚本创建的独占容器，脚本完成后删除；未操作业务库、集群或部署。

Resource PG 镜像为 `docker.io/library/postgres@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280`。Governance PG/Redis 使用 `scripts/image-joint-integration` 中的固定 digest。

| 命令/证据 | 结果 |
|---|---|
| 初始 `scripts/integration -run TestVPCListTotalAcrossPagesAndFilters -v ./internal/data/network` | fail：真实业务响应缺少 total；[原始失败](red.log) |
| `make generate BUF=<pinned-buf> SQLC=<pinned-sqlc>` | pass：Proto/sqlc 由固定工作流生成 |
| Network 总数及相关可见性数据库用例 | pass；[数据库结果](network.log) |
| Image 登记分页和 use case 数据库用例 | pass；[镜像数据库结果](image.log) |
| Governance `make api` | pass：完整暂存生成链，仅写回 4 项变更；[生成结果](governance-generate.log) |
| Governance Network/Image 定向测试 | pass；[定向结果](governance-tests.log) |
| Governance `scripts/image-joint-integration`，指定本次 Resource test binary | pass；[HTTP 结果](governance-http.log) |
| Resource `make verify BUF=<pinned-buf> SQLC=<pinned-sqlc>` | pass；[完整门禁](verify.log) |

执行的定向命令（各仓根目录，继承上述远程环境）：

```bash
scripts/integration -run 'Test(VPCListTotal|SubnetListTotal|EgressListTotals|LoadBalancerListTotal|PublicTenantInterfacesHide|LBSchemaFrom0006)' -v ./internal/data/network
scripts/image-integration -run 'TestCatalog(CASAndPagination|UseCasesAndRuntimeIsolation)' -v
go test -mod=readonly -count=1 -run 'Test(Network|Image)' ./app/admin/service/internal/service ./app/admin/service/internal/data ./tests/imagecontract
```

数据库验证覆盖跨页、筛选、空列表、删除导致的空后续页、租户隔离、系统 EIP 隐藏、未受理 LB 隐藏、公网/内网池区分和节点筛选。Image 验证覆盖字面量搜索、用途、加速器、取消登记排除及 platform/tenant 隔离。

HTTP 验证同时包含协议边界的非零 `total="37"`、空列表 `total="0"`，以及真正的 Governance HTTP → mTLS → Resource Catalog → PostgreSQL 空列表。Harbor 和网络 Provider 仅在外部边界使用既有替身；未声称真实 Harbor/网络数据面或生产环境验收。

## 验证限制与恢复信息

Governance 联编使用任务私有 `go.work` 选择本次 Resource 源码，未把 replace/go.work 写入任何正式仓库。其 go.mod 仍锁定此前公开的 Resource 版本；发布时必须先发布包含 total 的 Resource 版本，再更新 Governance 的依赖版本。以上是发布前验证快照。用户随后授权按 Resource main → Governance 依赖更新 → Governance main 顺序发布；后续独立模块验证和发布记录维护在 Governance 的接口集成登记中。部署仍为 `not_verified`。

最初 Governance 全包测试因未准备无关 Asynq Redis 环境而 fail；未删除或放宽该测试，后续只执行本次 Network/Image 定向范围。收尾源码门禁的执行目录曾纳入运行中的 TMPDIR/清单，Unix socket 测试还受过长 TMPDIR 影响；修正为短的任务私有 HOME TMPDIR，日志/清单放在排除的 `.work/` 目录，未修改验证器或 LB 业务代码。
