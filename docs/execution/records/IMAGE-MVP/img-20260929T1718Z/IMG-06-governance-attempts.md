# IMG-06 Governance 生成诊断记录

此文件记录尝试与恢复，不表示门禁已通过；当前任务状态只在 Resource `docs/execution/status.md`。

Governance 隔离工作树 `/home/chabking/workspace/.worktrees/governance-image-mvp-20260930`，review branch `codex/image-mvp-20260930`。原工作树未改动。Resource 固定契约为 `71aa986078dfb64388d783a7b2de01b7a94b0027`，Go module 解析为 `v0.0.0-20260929185545-71aa986078df`，无 local replace。以下均在 Fedora 本 run 中执行，未使用 live profile，也未触及共享 Harbor/集群。

生成中的真实失败与处置：

- 早期 `89a9f40` 使用默认代理时 sumdb 请求失败；只在本轮进程设置官方 GOPROXY/GOSUMDB，未关闭校验或修改全局配置。
- 同一版本尝试将既有 Module proto 加入 STANDARD lint，既有未加前缀枚举及 import 触发错误。恢复 Image 两份 proto 原有严格 lint 范围；Module 仍参与 Buf build/generate，并增加固定既有名称/编号及 IMAGE=14 的测试，不建立全局 lint 例外。
- `2192db7` 误用 `go run ./tools/localdeps/gow`（目录无 main）失败。后续改为仓库 `make gow`。
- `2eb43c9d929dd96a5c9fe1fea9bd9927101a0a0d` 首次 Ent 冷编译触发工具两分钟超时，子进程仍在本轮 scope。只停止已核对命令和目录的 `run-p789655-i9187579.scope`；SSH exit 141，确认 scope inactive，无残留本 SHA 进程。记录目录 `gov-2eb43c9d929dd96a5c9fe1fea9bd9927101a0a0d-generate` 不作为成功证据。
- 同一 SHA 新隔离目录 `-warm` 先预热 schema 编译，随后 Ent 仍 exit 1。直接诊断得到 `use of internal package .../internal/data/ent/schema not allowed`，因为工具从 repo root 建立临时 loader。未修改工具；使用仓库已有 `make -C app/admin/service ent`，保留五项固定 feature。诊断输出保留 `ent-diagnosis.txt`。
- 同一 SHA 新隔离目录 `-service-cwd`，生成 exit 0，但审阅发现暂存 OpenAPI 缺少既有 AK 创建 201 后处理，因此未导入该轮输出。
- 源码 `f2818ae3a788a1452fe329ab23af81ad96815a3c` 补上在暂存目录运行现有 `finalize-aksk-openapi.py`，重新生成 exit 0，2026-09-29T19:41:12Z—19:43:55Z，scope `run-p795397-i9177674.scope`。返回25文件，每一项均校验源 SHA、白名单、base SHA256 和返回 SHA256 后原样导入；只追加 Module/Ent IMAGE 枚举及 Image OpenAPI，Go module 只新增固定 Resource 契约，go.sum 为 tidy 的实际输出。

导入提交 `9690a811d257d2ff88e835bc064e1affb3bb126d` 已推送；新 SHA 门禁另行执行，不将生成成功当测试通过。各次均使用既有 flock、CPUQuota=200%、MemoryMax=2300M、MemorySwapMax=0，task-owned caches 与短 TMPDIR `/home/chabking/.im-1718`。旧失败目录保留，不 reset 或覆盖。

新增 `scripts/image-joint-integration` 只创建本 run 的私有 PG/Redis、随机密钥、loopback 端口和受限 runtime role，按容器 ID+ownership 清理。其下游是实际 mTLS 协议 peer；只证明 Governance HTTP/JWT/AK/Casbin/租户映射/响应边界，不构成实际 Harbor、Resource 持久化或产品验收。
