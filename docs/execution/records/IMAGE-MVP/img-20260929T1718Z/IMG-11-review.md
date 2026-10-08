# IMG-11：后端审阅交付与阻塞续跑点

这是有界代码/证据交付，MVP未完成。当前任务状态仍仅在
[执行账本](../../../status.md#image-mvp)维护；完整方法/API/数据库合同、限制与恢复见
[HANDOFF](HANDOFF.md)，验收覆盖见[A01—A32映射](acceptance-evidence-62ecc67.md)。

## 固定源码与差异

| 仓库 | 已测试代码 | 已核对的文档候选 | 源码树 |
|---|---|---|---|
| Resource | `62ecc67b839dd1040468323f8c316189a0c30653` | `bd038b5a7813e6dd879ae54227955339c26e13ac` | 代码树 `e31614428bc50a96100bf305e7db4a0c9e17ac91`；文档候选树 `268c7e526d0f9365534394e5cf2bdc1f7993dbcc` |
| Governance | `d64d6ee478801795afadb4573ba25f8c2de6b7fc` | 同一SHA | `0a144cdcdeb545998f728c01e23e4141c44dc665` |

两仓review分支均为 `codex/image-mvp-20260930`。后续仅追加本记录、检查日志和账本，
通过相同Fedora检查确认文档后续提交不改变已验代码；最终分支HEAD以Git及交付回复为准，
不在记录中伪造自引用SHA。没有merge/rebase/amend/force-push或正式发布。

[源快照](final/evidence/final-retry/source-snapshot.json)包含两仓完整SHA/tree、基线差异路径、
Resource29个及Governance778个生成文件SHA256。Resource全部非文档Git条目（路径、模式、
对象ID）逐项与已测62ecc67相同；该条目集合SHA256
`2da164472c0a2ce60fa1173c72e5c78e9ff166cb443f2865b3598185b9399121`。
Image契约与Governance固定依赖71aa986相同，8个根Network迁移checksum、旧RPC/README/
AGENTS/integration/sqlc保护路径一致；两仓tracked tree clean。

## Fedora 检查证据

文档候选bd038b5：2026-09-29T21:35:56Z，host Fedora，scope
`run-p920477-i9300043.scope`；持原锁，CPU200%/2300M/swap0，exit0。
387个本地链接及锚点、源条目相等、生成文件哈希、4批72项归档checksum全部通过。
[原始日志](final/evidence/final-retry/output.txt)、[退出码](final/evidence/final-retry/exit)、
[runner](final/final-docs-run-v2.sh)、[snapshot检查器](final/final-snapshot.py)、
[supervisor](final/supervisor.txt)均保留。

初次scope `run-p920142-i9300000.scope` 的文档和Network checksum已通过，但源manifest
在Governance基线diff处exit1：Fedora浅克隆缺少基线d1a804f对象。初次按HEAD导出的Git bundle
仍受浅克隆边界影响，远端cat-file exit128。随后只为转运建立本地临时ref指向已核实的
基线对象，生成该ref的bundle并按预期旧值删除临时ref；Fedora fetch后cat-file为commit，
未改变源码HEAD/文件。原[失败日志](final/evidence/final/output.txt)和exit1保留，随后同一产品
SHA重跑exit0。首次直接用裸SHA创建bundle也曾拒绝empty bundle/exit128；这不是源码门禁。
没有用替换基线、忽略差异或远端业务代码修改解决检查问题。

返回tar SHA256 `67fe185db47cdd934edad5689a6542eec3c7c30c27f9de710b6875e0ce7cdc2e`，
[SHA256SUMS](final/SHA256SUMS)覆盖11项，含自身共12文件。通过日志SHA256
`aa6474e3720fb8534a00ad68f8591818ad7d98abb3d9e8aa18989f4191949f18`，源快照SHA256
`064a5af6f565482c78fa393e6d06032fb406b17f601e10ad3f985e876e340290`；转运已逐文件校验。

原Network全部integration/race/6 mutations结果及清理见[IMG-10](IMG-10-regression.md)。
Image verify/真PG/race/3 mutations与13场景进程恢复已在62ecc67通过；Governance
默认优化build/HTTP/回归通过，默认优化race仍保留OOM限制，不能声称所有配置全绿。

## 未完成与资源

用户明确普通容器没有测试条件，IMG-08及相关产品联调先blocked；前端尚未确定，IMG-09
保留blocked，本次不对接。Live profile未获批准，实际Harbor版本/CA/权限/Token TTL、
集群/Namespace/节点缓存及真实业务链路尚未验证。技术driver不能替代产品验收。

隔离PG/Redis均按实际ID/ownership清理，原Network3个PG额外独立确认不存在；没有共享
Harbor/集群写入。保留Fedora run/source/cache/证据及本地worktree，恢复入口为
[checkpoint](CHECKPOINT.md)。下一步需要上述条件成立，当前不自动部署或重新追问逐批继续。
