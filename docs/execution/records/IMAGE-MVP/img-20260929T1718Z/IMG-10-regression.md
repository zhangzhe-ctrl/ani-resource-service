# IMG-10：原 Network 门禁与独立清理证据

Resource固定代码 `62ecc67b839dd1040468323f8c316189a0c30653`，树
`e31614428bc50a96100bf305e7db4a0c9e17ac91`。本记录只证明隔离回归，
不证明IMG-10的真实Harbor/普通容器产品闭环。用户已明确普通容器没有测试条件，
IMG-08及相关联调先阻塞；前端未确定，IMG-09保留blocked。

## 执行与结果

2026-09-29T21:03:04Z—21:28:52Z，host Fedora，cwd
`/home/chabking/ani-image-mvp-runs/img-20260929T1718Z/repo/resource/62ecc67b839dd1040468323f8c316189a0c30653`。
持既有 `net05a-heavy.lock`；scope `run-p909592-i9314307.scope`，CPU200%、
MemoryMax2300M、Swap0，原始20m单次测试超时，实际scope peak1.8G。
使用task缓存、`TMPDIR=/home/chabking/.im-1718`、Go1.26.7-X:nodwarf5、sqlc1.31.1。
完整[runner](regression/resource-final-regression.sh)、[原始日志](regression/evidence/IMG-10/62ecc67b839dd1040468323f8c316189a0c30653-network-regression/output.txt)、
[退出码](regression/evidence/IMG-10/62ecc67b839dd1040468323f8c316189a0c30653-network-regression/exit)、
[supervisor](regression/supervisor.txt)已归档。

| 实际命令或检查 | 结果 |
|---|---|
| `make integration TOOLS_DIR=<task tools>` | pass；原全量PG套件，Network data689.048s |
| `make race TOOLS_DIR=<task tools>` | pass；原全量race，Network data763.516s，无race报告 |
| `make tenant-mutations TOOLS_DIR=<task tools>` | pass；GetVPC/GetOperation/ListVPCs/GetSubnet/ListSubnets/GetAttachment，6项真实行为断言均发现移除tenant条件 |
| 原SQL及sqlc恢复、tracked tree clean | pass |
| baseline根迁移、Network RPC/sqlc、README、AGENTS、原integration脚本bytes diff | pass |
| 3个fixture实际ID不存在 | pass；最终独立检查再次确认 |
| 无批准profile时实际 `scripts/image-smoke` 拒绝 | driver exit1符合预期；workdir未创建，未访问live环境 |

上述命令顺序执行，`set -Eeuo pipefail`；最终exit0。`make verify`及Image独立真PG/race、
13次实际进程退出恢复、3项Image mutation已经在同一个62ecc67代码通过，见
[IMG-07](IMG-07-smoke.md)。Governance对应测试与默认优化race OOM限制仍见
[IMG-06](IMG-06-governance.md)，不因Resource全量通过而消除。

原 `scripts/integration` 和断言没有改动。外层执行函数仅给原PG的docker run追加
本run ownership label及memory-swap=768m，原memory768MiB/1CPU保留，删除前核对
ID清单和label。旧mutation脚本硬编码 `.tools/bin/sqlc`；本owned checkout中增加
被忽略的symlink，指向已核对模块身份的task sqlc1.31.1；[操作及clean证明](regression/evidence/IMG-10/62ecc67b839dd1040468323f8c316189a0c30653-network-regression/sqlc-tool-path.txt)。
没有通过改SQL断言、提高资源预算或跳过旧用例获得pass。

## 清理与归档

integration、race、mutation的3个PG完整ID及独立 `docker inspect` exit1/不存在结果见
[cleanup.json](regression/cleanup.json)，2026-09-29T21:29:55Z检查完成。Image fixture与
本run Network label inventory均为空。Image/Governance先前PG/Redis清理见其对应记录。
共享Harbor/集群无写入；run目录、缓存、源码副本、证据和本地独立worktree保留。

第一次归档程序将Docker错误文字写成大小写敏感的 `No such object`，实际客户端输出
`error: no such object`，导致归档assertion exit1；未创建或删除资源，也未改变门禁结果。
保留[first runner](regression/closeout-archive.py)及失败scope。第二次只修复该大小写匹配，
[new runner](regression/closeout-archive-v2.py)仍要求inspect exit1及确切“不存在”错误，并核对
fixture inventory；scope `run-p919795-i9304116.scope` exit0。前一次不完整归档目录保留。

回传tar SHA256 `7cc26316984d517272a3dcc24199ef1d455ba8faf7516ebe7cfe7e23c7e643a3`；
[SHA256SUMS](regression/SHA256SUMS)覆盖tar内13项，包含自身共14文件；v2 runner另按
本地原稿逐byte比对转运。Network日志SHA256
`ed497281828a783c2c6af6c78bbae1f86a3308350932f1307541bc49689c9bb9`。

随同归档的早期文档检查固定 `e3142cf5ea891a7439945ef12de33ff687f8b5d6`，21:28:52Z—
21:28:55Z、scope `run-p919518-i9304067.scope` exit0：368个本地链接/锚点、8个Network
迁移checksum、post62仅文档、Image生成契约等于Governance依赖71aa986。
[原始日志](regression/evidence/IMG-11/e3142cf5ea891a7439945ef12de33ff687f8b5d6-docs/output.txt)
SHA256 `a24ca9f4565821f92cc7d1451f9a6820d76f828c4709c5e3770fbde04b36d43e`。
这份旧文档检查不冒充后续交接记录的最终检查；后续精确提交另行验证并归档。
