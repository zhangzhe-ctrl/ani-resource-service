# IMG-07.4—.5 Harbor 技术驱动与证据

已验代码SHA `62ecc67b839dd1040468323f8c316189a0c30653`。
[Fedora完整门禁](smoke/evidence/62ecc67b839dd1040468323f8c316189a0c30653-verify/output.txt)
2026-09-29T20:56:14Z—20:59:14Z，exit 0，日志SHA256
`52de9e5bbe40ceaa7ed033190b4bb0d35459b0aeae3d899b0675172f6f95d4b7`。
归档包SHA256 `914c8cfd3460786387f0c79830dc15a43054afa55b4c347d7852f270ae6eac63`；
[逐文件清单](smoke/SHA256SUMS)保留失败、生成manifest、完整runner和supervisor。

`scripts/image-smoke`/`image_smoke.py` 的实际四阶段：平台CLI初始化/发布，两个真实
Governance租户空间及发布凭证，受限Robot直接skopeo Push，根Digest核对、固定Digest
登记、调用内部Runtime用例并用该tenant只读凭证验证平台/自身内容及跨租户拒绝。
请求字段来自当前公共DTO，不传tenant输入。`scripts/image-smoke-runtime` 是独立技术
helper，无listener，不是普通容器业务owner；不给浏览器admin或内部拉取材料。

预检要求批准profile、Fedora资源scope、源码/二进制/私有输入哈希、集群API/CA/UID、
Harbor实际版本、精确三个run-owned Project均不存在、两未开通测试tenant、固定小镜像
Digest和期限/字节预算。TLS开启、不跨源重定向；网络/404/5xx不能冒充隔离通过。依赖原始
输出不写证据；Secret只存本run私有临时文件，正常成功/失败退出时清除。事件先写意图/幂等键，
再写实际ID；同目录拒绝盲目重跑。外部资源保留供ID/ownership核对，没有自动GC/删除。

5个Python离线用例验证批准/范围/输入/预算边界、未批准时零命令/零目录、四阶段公共字段
编排、Secret不入日志、依赖故障不能假绿。Go helper的3个定向测试/race验证批准tenant范围、
私有文件/独占输出和输入校验。协议fixture仅是编排测试，不替代真实Harbor、真实Governance
部署或产品证据。操作输入、构建方法、实际限制见
[部署手册](../../../../../deployments/image/README.md#harbor-技术验收驱动)。

生成循环：源码 `fb92f8d87642dee4a48ed750c6cc36ed4a520bb9` 在Fedora格式化2个Go文件，
按base hash白名单回传为 `91f6ddc6c51a3092415f0688426b08145f70e8ce`。
其验证因测试fixture缺Governance地址而exit 1；回本地补齐fixture，同时将Project预查改为
与现有适配器相同的按名称GET，提交最终代码 `62ecc67...`。新SHA生成无diff后上述门禁pass。
另保留一次手工传错完整SHA的checkout失败（exit128），发生在任何生成/测试之前；不作为证据。

新SHA额外通过全部Image真实PG/race、13个进程故障恢复场景、三个mutation负例与完整
`make verify`，旧Network保护路径无diff。使用既有flock、CPU200%/MemoryMax2300M/swap0，
scope `run-p872439-i9263425.scope`。PG容器
`a8b0038369e1d747bbf9292403acb3eaa22e061f6d35462f113f075e46826e6f`、
`8d3c15b79cea60819067e75d6df2d27548fc5a443dc4602e15839fdecf15bbcc`
均按ID/ownership清理，归档Image fixture inventory为空。

IMG-07 code/isolated通过；live仍blocked：没有获批profile、实际Harbor/集群/测试tenant绑定。
没有运行live四阶段、创建共享Project/Robot/Namespace/Pod或宣称MVP完成。
