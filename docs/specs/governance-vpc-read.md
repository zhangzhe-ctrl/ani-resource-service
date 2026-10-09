# Governance VPC 只读接入

2026-09-22 用户要求完成 Governance AK/SK 签名查询闭环；整合 9e56e1c 中的 VPC 只读接收代码并增加 API Key actor。本规格限定新增 `vpc-read` 组合入口，补充 [ADR-0003](../adr/0003-defer-workload-authentication.md) 的历史暂缓决定，不将 NET-AUTH 或全量 IAM 集成标记为完成。

## 入口

使用现有 `/network.v1.NetworkService/GetVPC`。`ANI_NETWORK_MODE=vpc-read` 只装配真实 PostgreSQL repository、Network 用例、gRPC 和 admin；不启动 KC、观察器、worker、实例 owner 客户端。未设置 mode 或 full 保留历史入口及其认证延期边界；未知 mode 拒绝启动。部署本片必须显式指定 vpc-read，不能把 full 暴露给治理查询客户端。

TLS 1.3，`ANI_NETWORK_CLIENT_CA` / `ANI_NETWORK_TLS_CERT` / `ANI_NETWORK_TLS_KEY` 必需；server 精确 SAN ani-network-service。入站证书须经 CA 验证且精确 SAN ani-governance。只接受单值的 `x-ani-tenant-id`（非零规范 UUID）、`x-ani-actor`（`governance:user:<id>` 或 `governance:access-key:<id>`，id 为非零规范 uint32）、`x-ani-request-id`（非零规范 UUID）。缺失、重复、非法身份 Unauthenticated；请求 tenant 与可信委托范围不同 PermissionDenied；除 GetVPC 以外 RPC 和全部 stream PermissionDenied。以上在业务处理/SQL 之前执行。

这是复用 Governance 已有的受信服务委托协议；用户登录、角色和权限由 Governance 负责。本服务不复制 IAM Principal/Membership/Grant，不声称能独立复核 Governance 所断言的用户或 Key 的租户关系。它只信任专用 CA 下指定 workload；CA/私钥管控和生命周期是部署责任。

GetVPC 继续使用原业务校验和带 tenant + vpc ID 谓词的 SQL（包括 subnet_count 的同租户子查询）。跨租户对象与不存在对象都返回 NotFound，不根据对象真实归属产生不同错误。没有认证 fallback、默认 tenant 或平台全表查询。

## 数据与健康

原 owner-only `-migrate`、受限 runtime 角色和 schema checksum 检查保留。只读部署撤销 runtime DML，仅 SELECT；连接 DSN 设置 `connect_timeout=1`，服务端 RPC 5s，治理客户端 2s。读模式 readiness 按请求限时 1s 检查 PostgreSQL、角色及 schema，不依赖未运行的 worker。数据库中断时不可假报健康，恢复后可以再次读取同一记录。

本模式只返回持久业务事实和原有 observation_stale。实验 fixture 标记 available 只用于响应映射，不证明真实 KC VPC 可达。创建/删除、资源 reconciliation、子网/附件接口和数据面验证不属于本片。本批状态和证据见 [执行状态](../execution/status.md)；历史提交的验收不作为本批新版证据。

Resource 改名后的组合入口位于 `cmd/ani-resource-service/vpc_read.go`，Network 适配器位于 `internal/{biz,data,service}/network`。既有 `ANI_NETWORK_*` 环境变量、`network.v1` RPC、server SAN `ani-network-service` 与 client SAN `ani-governance` 保留，用于兼容既有部署和证书；不随 Go module 改名替换。

## Governance 对外详情格式

Governance 的 `GET /api/v1/networks/vpcs/{vpc_id}` 直接返回公开 VPC 对象（`id/name/cidr/state/...`），不增加 `vpc` 外层字段。网络操作、EIP、VPC SNAT 详情同样直接返回对应公开对象。Resource 的内部 gRPC 请求、响应消息和租户隔离合同保留；HTTP JSON 展开由 Governance 的 HTTP 绑定完成，不在 Resource 增设浏览器 HTTP 入口。列表的 `items/next_cursor/total` 及带额外业务字段的响应保留。
