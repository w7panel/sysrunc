# 218 集群集成测试（2026-11-23）

## 范围与现场

- L0 kubeconfig：`/root/.kube/218.config`，单节点 K3s `server1`。
- L0 正式 `default/w7panel-sysbox-installer` DaemonSet 仍为 1/1 Ready；
  `sysbox-runc` 和 `sysbox-runc-lite` RuntimeClass 仍存在。本次未卸载或替换它们。
- 新建隔离 CKM：`k3k-console-164315/ckm-sysrunc-it-1123`，L1 Server
  使用 L0 `sysbox-runc` 且 `hostUsers: false`。L0 尚未部署新 `sysrunc`
  RuntimeClass，因此此 CKM 不能证明新运行时已完成宿主迁移。
- L1 `default/sysrunc` Helm release revision 7，测试镜像
  `docker.io/library/sysrunc-it:218-20261123g` 只导入 L1 containerd，
  未发布到外部仓库。镜像基于已有 Sysbox deploy image，携带工作区构建的
  sysrunc 二进制和本次临时构建的静态 `fusermount3`。当前 Chart 为
  `deployed`，其内层二进制 SHA256 为
  `0c4a540eae212371f3aff806a2a012a526827f9ca0a928dfea3ceb39845f6f6c`。

## 已验证

| 检查 | 结果 |
| --- | --- |
| L1 agent、admission | 均 1/1 Ready；FS 与 snapshotter socket 存在 |
| CRI handler | `sysrunc` 的 `BinaryName` 指向 `sysrunc-inner/releases/.../bin/generic/sysrunc` |
| 二进制一致性 | 初轮 L1 与本地 `sysrunc` SHA256 均为 `803a889f3c6fb30d00faea5216df0ba3a9403bf1e8ea5ef912aed2d7d57d7c84`；当前镜像值见上方 |
| 普通 Pod | `default/sysrunc-it-lite` 为 Running；CRI sandbox `runtimeHandler: sysrunc`、`SANDBOX_READY`；nginx HTTP 返回欢迎页 |
| admission | 新 `sysrunc` FS Pod 不再自动设置 `spec.hostUsers`；专用 CKM 实测该字段为空 |
| Skill 预检 | `sysbox-in-sysbox-test` 的 `00-check-prereqs.sh` 通过；其余脚本硬编码旧 Chart/handler，按相同断言手动适配执行 |
| L0 FUSE | 专用 CKM Server 的 `/dev/fuse` 是字符设备且可打开；其 Pod 为 `sysbox-runc`、`hostUsers: false`，仅证明旧 L0 基线 |
| L2 空 PVC 初始化 | `sysrunc-skill-webroot-1123` 的 nginx `index.html`、`50x.html` 均存在，Pod 无 `sysbox/volume-init` 注解 |
| L2 重建持久化 | 重建前写入 webroot PVC 与 `/srv/data` 标记；新 Pod `sysrunc-skill-nginx-1123-74c74c77df-ptpf7` 中两处标记均保留 |
| L2 CRI 统计 | 新 Pod sandbox 为 `sysrunc`、`SANDBOX_READY`；可写层 mountpoint 非空，嵌套 kubelet `/stats/summary` 返回节点统计 |

## 失败与边界

1. 首轮普通 Pod 因 runc 未带 `seccomp` build tag 而失败。根 `Makefile`
   已加 tag，重建后普通 Pod 通过。
2. 首轮 FS daemon 未启动。内层 agent 现启动并检查 FS；重建后 socket 和
   容器预注册可用。
3. FS 预注册曾因 CKM 精简镜像无法执行动态链接 `fusermount3` 失败。
   本次临时测试镜像使用从 Debian fuse3 3.17.2 源码编译的静态 helper，
   预注册随后成功。正式 Dockerfile 已改为构建静态 helper；本地镜像
   `sysrunc-it:218-20261123h` 构建成功（ID `sha256:1038b3d43ff6e4fe2500f719689a5b5105f5abb9f9f071f188452b81c5583d27`），
   容器内 `ldd` 显示 helper 无动态依赖，`fusermount3 --version` 为 3.17.2。
   此镜像尚未导入 218 或发布。
4. 初轮 `sysrunc` handler 不支持 user namespace，admission 自动设置
   `hostUsers: false` 的 FS Pod 被 kubelet 拒绝。改为不设置后，Pod 进入
   OCI 创建，先报 UID/GID 映射缺失；加入无映射注册后，又报文件挂载的
   传播标志错误。临时修正文件挂载标志后，继续触发 runc 的 `/proc/sys`
   安全检查：`cannot be mounted because it is inside /proc`。进一步将
   例外限定为 sysrunc FS 的 FUSE 源，并去掉该目录的传播标志后，OCI
   初始化写入 `net.ipv4.ip_unprivileged_port_start` 曾报文件不存在。随后修正
   FS nsenter 与新版 runc 的握手和 PID 协议、相同 user namespace 的 `setns`、
   启动时递归读取 FUSE `cap_last_cap`，以及相同 user namespace 下 FUSE
   属性的 UID/GID。专用 CKM 复测已跨过 sysctl 错误，下一步在加载 seccomp
   notify filter 时遇到 `device or resource busy`。L0 CKM Server 仍用旧
   `sysbox-runc`，其进程已有 1 个继承的 seccomp filter；这与内核拒绝第二个
   notify listener 的行为一致，尚未通过改用新 L0 `sysrunc` 复验。失败 FS
   Pod 已删除；临时测试二进制已恢复为 Chart 版本，当前 runc SHA256 为
   `0c4a540eae212371f3aff806a2a012a526827f9ca0a928dfea3ceb39845f6f6c`，
   活动 FS 进程 SHA256 为
   `db41eaa7bbcf6889b3d263685369b19355913b2fddee7fa08fab42d61a649492`。
   FS Pod 仍未 Ready，seccomp notify 端到端仍未验收。当前 runc 也缺少
   idmapped mount 支持，不能伪造能力声明。
5. L0 正式 `sysrunc` RuntimeClass 不存在。当前宿主仍有多个业务 Pod
   使用旧 `sysbox-runc`/`sysbox-runc-lite`。新宿主安装脚本现只安装独立
   `sysrunc` 入口，但配置 containerd 需要重启单节点 K3s；尚未执行会影响
   业务 Pod 的宿主安装或旧组件移除。

失败 FS Pods 已删除以停止反复创建 sandbox。保留专用 CKM、Helm release
和成功的 `sysrunc-it-lite` Pod，供复核；本轮持久化 Deployment 已缩容
至零，保留两个 Bound PVC 和标记；未触及其他 CKM。

仓库内 `skills/sysrunc-in-sysrunc-test/scripts/check-status.sh` 已在该 CKM
实跑通过；它覆盖旧 L0 基线、新 L1 handler 和普通 L2 sandbox，不代替
FS 或 L0 新运行时验收。

## 2026-11-23 L0 debug Pod 续测

- 按要求创建固定到 `server1` 的 privileged debug Pod
  `default/sysrunc-node-debug-1123`，通过 `/host` 和 `chroot /host` 检查宿主，
  没有使用 SSH。原 containerd 模板已备份为
  `config-v3.toml.tmpl.before-sysrunc-20261123`，SHA256
  `4aa4fd360b7071ee74fee0470dd6f8388ad26388f73cc2cbd74506822e98cbe1`。
- 本地测试镜像 `sysrunc-it:218-20261123k` 的 ID 为
  `sha256:2086d93cf5b7ff3de19d9ada3524869ff01cad4c551835c1d2ce341031422d95`，
  经 debug Pod 导入 L0 K3s containerd。压缩包传输前后 SHA256 均为
  `f0fe3bed7510c8d30a38258c9f15e0a2d50f1d1a494ea49df6dee1ceaf555aa5`。
  仍是本地集成镜像，未发布。
- L0 `default/sysrunc-l0-it` Helm revision 1 已安装，`sysrunc` RuntimeClass
  存在；宿主 `sysrunc`、`sysrunc-fs`、`sysrunc-snapshotter` 服务均 active。
  CRI `sysrunc` handler 的 `BinaryName=/usr/bin/sysrunc`、
  `snapshotter=sysrunc`；三个二进制 SHA256 分别为
  `48a3b496c250786acab0f1a0a62f46542c6a0d21619408105c286d65e6a96d73`、
  `88be176037d3a010f4bc3b7902f9a8e831629fd208c98f21f286342f9d88c946`、
  `6806128214cdb7e58841396a7c9037461fd7e798e85dcd9b5f95ac1d118dd4c4`。
  旧 `sysbox-runc` 和 `sysbox-runc-lite` handler 仍在，未迁移业务。
- 首轮安装因扫描到 zombie 进程不可读的 `mountinfo` 失败；脚本已排除
  zombie。第二轮安装发现旧脚本错误地删除 `sysbox-runc` Pod；升级时另一段
  旧迁移逻辑再次删除它们并 cordon 节点。已立即停止该轮安装、uncordon，
  修正两处选择条件并加入无宿主 `sysrunc` Pod 时拒绝自动迁移的保护。
  受影响的旧 Pod 由控制器重建；最后核对旧 `sysbox-runc` Pod 均 Running、
  专用 CKM Server 2/2、节点 Ready 且可调度。此过程造成业务 Pod 重启，
  不能视为无扰动安装。
- `sysrunc.service` 原先调用 FS 不支持的 `--version` 导致失败，已修正。
  上轮留下 `Containers:null` 的空 FS 检查点，已通过 debug Pod 移至
  `/var/lib/sysrunc-deploy-k8s/fs-restart-empty-20261123.json`；脚本新增空检查点
  判定。宿主并存 Bats 6 项通过。
- **L0 `sysrunc` 仍不能承载 `hostUsers:false` 的 L1 Server Pod。** CRI
  `runtimeHandlers[name=sysrunc].features` 未报告 `user_namespaces`，尽管
  snapshotter 插件报告 `remap-ids`。根因是当前 `sysrunc features` 缺少
  `linux.mountExtensions.idmap.enabled`，而旧 `sysbox-runc features` 包含该项。
  必须实现真实 idmapped mount 支持并复测，不能仅伪造 feature 声明。
  因此专用 CKM 仍运行在旧 L0 `sysbox-runc`，L0 迁移和 FS/seccomp notify
  端到端验收仍未通过。
- L0 普通 Pod `default/sysrunc-l0-smoke-1123` 使用 `runtimeClassName:sysrunc`
  启动并 Ready；通过 `default/sysrunc-node-debug-1123` 的 `/host` 执行宿主
  `k3s crictl pods -o json`，确认 sandbox
  `9d099095061f2ca0615e370af3c0abc5f8f2d938db184a820768835c0ea438d4`
  为 `runtimeHandler=sysrunc`、`SANDBOX_READY`。debug Pod 内访问
  `http://10.42.0.25/` 返回 nginx 欢迎页。验收后已删除 smoke Pod；
  debug Pod 留作后续节点操作。

## 2026-11-23 L0 userns 与专用 CKM 续测

- 修复了 runc 的真实 idmapped mount 路径：OCI runtime-spec 升至 1.2.1，
  `features` 报告 idmap，宿主侧 `open_tree` + `mount_setattr` 后由容器侧
  `move_mount` 接入。`make build`、specconv 测试和宿主并存 Bats 6 项通过。
- 经 debug Pod 导入本地镜像 `sysrunc-it:218-20261123m`，L0 独立 Helm
  release `default/sysrunc-l0-it` 升至 revision 3；CRI 的 `sysrunc`
  handler 指向 `/usr/bin/sysrunc`、snapshotter 为 `sysrunc`，并报告
  `user_namespaces:true`。L0 测试 Pod `default/sysrunc-l0-userns-1123`
  设 `hostUsers:false` 后 Ready；容器 UID 映射为
  `0 33357824 65536`，宿主 CRI sandbox
  `443787d2b7f137f4637017c855d34a7b1d487dc293a22082ac1e26aa9dc3a694`
  为 `sysrunc/SANDBOX_READY`。安装脚本已在依赖齐全时跳过重复的
  `apt-get update`，避免 218 Ubuntu 软件源慢导致升级卡住。
- 本地镜像 `sysrunc-it:218-20261123n` 用于启用 L0 admission，Helm
  revision 4。新 webhook 仅匹配 `runtimeClassName:sysrunc`，不处理旧
  Sysbox Pod。专用 CKM 首轮切换失败：CKM controller 原先仅对
  `sysbox-runc` 添加 rootfs PVC 注解，`sysrunc` Pod 因缺 sidecar 在
  `/var/lib/rancher/k3s/server` 报权限错误。已在相邻
  `/root/workspace/w7panel-ckm` 仓库修正注解生成和 runtimeClass 切换逻辑，
  定向 Go 测试通过；本地 `ko.local:sysrunc-it-20261123b` 镜像经 debug Pod
  导入并短时用于 218 controller。CNB 仓库推送返回 `UNAUTHORIZED`，
  该镜像未发布。验证后 controller 已恢复原镜像
  `docker.cnb.cool/i0358/ai-cvm:1.1.146`、1/1 Ready。
- 第二轮切换得到 `sysrunc.io/rootfs-rw-layer` 注解和
  `sysrunc-rootfs` sidecar，但特殊目录还是普通 bind mount；
  L1 K3s 对 `/var/lib/rancher/k3s/server` 报权限错误。源码为 special
  path 增加递归 idmapped bind mount，测试通过；通过 debug Pod 原子替换
  宿主 `/usr/bin/sysrunc`，当前 SHA256 为
  `9e618e0b5164c0065d0b2d2ccc74aa5fc4536c738369aa8221b43e57648fa75b`。
  **此二进制更新尚未打入当前 L0 Helm 镜像。**
- 第三轮切换已越过上述权限错误，K3s 可打开 datastore，但嵌套
  `overlayfs` 挂载返回 `permission denied`；新 L0 `sysrunc` Pod 内也没有
  `/dev/fuse`。FS、seccomp notify 和完整 L1/L2 验收仍失败。专用 CKM 已
  回切 L0 `sysbox-runc`，新 Server Pod 2/2 Ready；旧业务 runtime 未迁移。

## 2026-11-23 L0 FUSE 设备与 L1 K3s 续测

- 修复了带 `sysrunc.io/rootfs-rw-layer` 意图的 Pod 自动取得 `/dev/fuse`
  的 OCI 设备、bind mount 和设备 cgroup 许可；Pod 无 `/dev/fuse` hostPath。
  带 `sysrunc.io/fs=true` 的独立 FS Pod 仍走 seccomp notify 注册。
- 为 L1 发布镜像构建静态 `fuse-overlayfs` 1.17 和 `fusermount3` 3.17.2；
  Alpine 构建阶段使用阿里云 apk 镜像；本轮未安装宿主 Ubuntu 依赖，因此
  未更改 218 的 apt 源。Docker 构建中两者 `readelf -l` 均无动态 loader。CKM controller 的
  Sysrunc rootfs 注解和 runtimeClass 切换修复已通过定向 Go 测试。
- L0 本地镜像 `sysrunc-it:218-20261123p` 已通过 debug Pod 导入节点，
  独立 Helm `default/sysrunc-l0-it` 升至 revision 6。宿主
  `/usr/bin/sysrunc` 与本地构建 SHA256 均为
  `634c630df40241fdfe1afebf92f7ac9aecc39aadf3001c63adc1c6a18d7d7e7d`。
  安装器 AppArmor 规则补入实际 `sysboxfs` FUSE 来源在新目录
  `/var/lib/sysruncfs` 的挂载许可。旧 Sysbox handler 和业务 Pod 保留。
- 第一次新 FS L1 Server 预注册因 AppArmor `fusermount3: mount failed:
  Permission denied` 失败；重载规则后越过预注册。随后 idmapped bind
  mount 的 `/proc/sys` 安全检查用 fd 路径判定而拒绝，已让检查同时使用
  OCI 来源路径与实际 fd 的文件系统类型。再试到达 sysctl 写入，但 FS
  虚拟 `/proc/sys/net/ipv4/ip_unprivileged_port_start` 不存在。CKM Server
  不启用 FS 模式，保留单独的 FS 验收门槛。
- CKM controller 测试镜像 `ko.local:sysrunc-it-20261123e` 用于专用
  `ckm-sysrunc-it-1123` 的 runtimeClass 切换；它设置
  `hostUsers:false`，不设置 FS 注解，选 K3s `native` snapshotter。
  L0 新 Server Pod 自动获得 `/dev/fuse` 字符设备，UID 映射为
  `0 807075840 65536`。`fuse-overlayfs` 的初次探测失败：已有 L1
  持久盘的 `/usr/bin/fuse-overlayfs` 仍指向旧动态 helper，精简 rootfs
  无法执行；直接挂载也返回 EPERM。`native` 已使 L1 K3s 越过快照器
  初始化，但 kubelet 报 `/dev/kmsg` 缺失，kube-proxy 报 iptables
  `Permission denied`，Server 不能 Ready。L1/L2、FS/seccomp notify、
  重新安装稳定性及发布件验收均未通过。
- 失败后仅专用 CKM 回切 L0 `sysbox-runc`，Server 恢复 2/2 Ready；
  线上 CKM controller 恢复镜像 `docker.cnb.cool/i0358/ai-cvm:1.1.146`
  并 1/1 Ready。新 controller 镜像只导入节点，未推送仓库；新 Sysrunc
  镜像也仅为本地集成构建。L0 新 release 保留用于后续独立测试。

## 2026-11-23 L1 权限续测

- 独立 L0 Pod `default/sysrunc-netadmin-1123` 使用 `sysrunc`、
  `hostUsers:false`、`NET_ADMIN` 和 `SYS_ADMIN`，Ready；容器内执行
  `/bin/aux/iptables -t nat -L POSTROUTING -n` 成功。仅为专用 CKM
  Sysrunc Server 添加这两项能力，并由 OCI runtime 对带 rootfs 意图的
  Pod 提供 `/dev/kmsg` 到 `/dev/null` 的 shim，不暴露宿主内核日志。
- 新本地镜像 `sysrunc-it:218-20261123q` 经 debug Pod 导入，L0 独立
  Helm release 升至 revision 7。宿主 `/usr/bin/sysrunc` 与构建 SHA256
  均为 `34819afcebd00f285b97aec802184e89a1d36027a467491abff5905048c5c92f`。
  新 CKM controller 测试镜像 `ko.local:sysrunc-it-20261123f` 也只导入
  节点，未发布。
- 再次切换专用 CKM 后，先前 `/dev/kmsg` 和 iptables 权限错误消失；
  K3s 仍退出，具体为 kube-proxy 写
  `/proc/sys/net/ipv4/conf/all/route_localnet` 遇到只读文件系统，以及
  kubelet bind mount `/var/lib/kubelet` 遇到 `permission denied`。
  L1 K3s、L2、FS/seccomp notify 及完整迁移仍未验收。
- 仅专用 CKM 再次回切 `sysbox-runc`，线上 CKM controller 恢复原镜像
  `docker.cnb.cool/i0358/ai-cvm:1.1.146`。独立 L0 Sysrunc Helm release
  保留 revision 7；旧业务 RuntimeClass、handler、installer 未卸载。

## 2026-11-23 AppArmor、proc 与 cgroup 续测

- 独立 `hostUsers:false` Sysrunc 测试 Pod 在默认 AppArmor 下即使具有
  `SYS_ADMIN`，执行 bind mount 仍被拒绝；设置容器 AppArmor
  `Unconfined` 后同一 bind mount 成功。另一个仅用于测试的 Pod 设置
  `procMount: Unmasked`，可在其私有网络命名空间写入并恢复
  `net.ipv4.conf.all.route_localnet`。据此仅为专用 CKM 的 Sysrunc
  Server Pod 加入两项配置；CKM 定向 Go 测试通过。
- CKM controller 本地测试镜像 `ko.local:sysrunc-it-20261123g` 经 debug
  Pod 导入并短时运行。专用 CKM 的 L0 Server Pod 为 `sysrunc`、
  `hostUsers:false`、`native` K3s snapshotter、AppArmor `Unconfined`、
  `procMount: Unmasked`。K3s kubelet和 kube-proxy 成功越过前轮的
  `/dev/kmsg`、iptables、`route_localnet` 与 bind mount 错误，随后
  kubelet `Failed to start ContainerManager`：创建
  `/sys/fs/cgroup/kubepods` 遇到 `read-only file system`。L1 Server
  未 Ready，L1/L2 与 FS 验收仍失败。
- 再次只回切专用 CKM 至 `sysbox-runc`，Server 2/2 Ready；线上 CKM
  controller 恢复 `docker.cnb.cool/i0358/ai-cvm:1.1.146`、1/1 Ready。
  L0 节点 Ready，独立 Sysrunc release 维持 revision 7。上述控制器
  镜像均未发布。

## 2026-11-24 cgroup、OOM 与 L1 Pod 续测

- 对照 Sysbox 后，确认 L1 容器还需要可写 cgroup v2、委派目录及
  `init.scope`。独立 `default/sysrunc-cgroup-probe-1124` 设置
  `hostUsers:false` 后，OCI cgroup mount 为 `ro`；在容器内另挂 cgroup2
  可得到 `rw`，但创建子组因宿主目录权限被拒。仅将该隔离容器的
  cgroup 目录及委派文件 chown 给其映射 UID 后，创建子组成功。
- Sysrunc 源码仅对 rootfs 意图容器把 cgroup OCI mount 改为 `rw`，
  让 runc 原有委派逻辑生效，并把初始进程移入 `init.scope`。
  第一次专用 CKM 测试中，kubelet 从 `read-only file system`
  前进到 `domain-invalid`；加入 `init.scope` 后，K3s kubelet
  成功启动，但 L1 原生 runc 设置子 Pod 的 `oom_score_adj`
  返回 `permission denied`。
- 按 Sysbox 的实现，在进入 user namespace 前为 rootfs 意图容器
  设置 `-999` 的 OOM 调整下限，然后恢复 OCI 的 QoS 值。再次测试后
  OOM 错误消失，L1 原生 runc 在打开子进程 mount namespace 时被拒。
  CKM 源码仅对 Sysrunc L1 Server 增加 `SYS_PTRACE`，定向 Go 测试通过；
  本地镜像 `ko.local:sysrunc-it-20261124-ptrace` 经 debug Pod 导入节点。
  第三次测试越过该错误，L1 Pod sandbox 转而因挂载 `sysfs` 得到
  `operation not permitted`。在 L1 Server 直接执行 `mount -t sysfs`
  也返回权限错误。**L1 Server 尚未 Ready，L2、FS/seccomp notify、
  重新安装稳定性及发布件验收仍未通过。**
- 本地 Sysrunc 测试镜像 `sysrunc-it:218-20261124-final` 的镜像 ID
  为 `sha256:fd2d5e13f919e2ef9758bd60b73b1826e0330839d249fd66d285b66b0bf82193`；
  L0 独立 Helm release 为 revision 10。镜像内、构建文件及宿主
  `/usr/bin/sysrunc` 的 SHA256 均为
  `3d664ecc5aaf9e8f9184f098bea36b2d0e3b5f2260e8f225c57e9b5f6192bf5d`。
  该镜像只在本地构建并导入节点，未发布。专用 CKM 已回切
  `sysbox-runc`，新 Server Pod 2/2 Ready；线上 CKM controller 已恢复
  `docker.cnb.cool/i0358/ai-cvm:1.1.146`、1/1 Ready。

## 2026-11-24 FS 恢复与独立 Pod 复测

- 节点 `sysrunc-fs.service` 因脏 checkpoint 拒绝启动。经 debug Pod 核对
  `/run/sysrunc/fs-restart.json` 为 `Clean:false`、无容器记录，宿主及进程
  mountinfo 未见存活的 `sysruncfs` 挂载；将原文件保存在
  `/var/lib/sysrunc-deploy-k8s/fs-recovery-20261124-0245/` 后启动服务。
- 服务恢复后，独立 `default/sysrunc-fs-sysctl-probe-1124` 的下一处失败是
  FS nsenter 进入 cgroup namespace 返回 `EPERM`。对照 Sysbox nsexec 对
  宿主拥有的命名空间先于 user namespace 进入的做法，调整 Sysrunc FS
  的 cgroup namespace 顺序，定向 `go test ./nsenter ./handler/implementations`
  通过。新 FS 二进制 SHA256 为
  `faefa3a73c85579dbe01512537409593c995c8819edcca9bbb42e7db115c0bb6`。
  服务干净停止（`Clean:true`、无容器记录）后通过 debug Pod 更新二进制并
  重启，旧文件备份于 `/usr/bin/sysrunc-fs.218-20261124-before-cgroup-order`。
- 该独立 Pod 现为 `1/1 Running`，`/proc/sys/net/ipv4/ip_unprivileged_port_start`
  读取为 `0`，`/proc/sys` 为 `sysboxfs` FUSE 挂载，容器内新建并卸载
  `sysfs` 成功。此结果仅覆盖 L0 独立 FS Pod；专用 CKM 仍在 L0 使用
  `sysbox-runc`，L1 原生 runc 的 `sysfs` 挂载及 L2 验收尚未复测通过。
  FS 修复仅更新节点二进制，尚未打入 Helm 镜像或发布。

## 2026-11-24 L0 Sysrunc 至 L2 集成验收

- 专用 CKM：`k3k-console-164315/ckm-sysrunc-it-1123`，当前 Server Pod
  `k3k-ckm-sysrunc-it-1123-server-c9ff6d647-m7sdm` 为 2/2 Ready。
  L0 Pod 与 CRI sandbox 均为 `sysrunc`，`hostUsers:false`，无
  `sysrunc.io/fs`；L0 Helm `default/sysrunc-l0-it` revision 10。测试期间
  旧 Sysbox 安装保留给其他工作负载。
- L0 原生 runc 在 L1 user namespace 内挂载 `sysfs` 曾返回 `EPERM`。
  Sysrunc 改为在切换 user namespace 前创建 detached sysfs mount 并通过
  mount FD 传入。L0 FS listener 不再放在 K3s Server 上，从而避免 L2
  安装自身 seccomp listener 时返回 `EBUSY`。CKM 控制器镜像
  `ko.local:sysrunc-it-20261124-reconcile-fs` 已经经 debug Pod 导入节点，
  只在本地测试；其镜像 ID 为
  `sha256:baeadbb11f3086f6877f72e5d425c9150426e514ed5c4ade9e1775e52c882bcf`。
- 新 Server 的 L1 FS 最初因 `/sys/devices/virtual/dmi/id/product_uuid`
  在用户命名空间中不可读而退出。FS 现对 `EACCES` 使用与文件不存在相同
  的零 UUID 兜底。静态 FS 二进制 SHA256 为
  `b58fce30d28682c02665763dc86764a493d70259985ceb28a05b1eafe9d48322`。
  snapshotter 以 `CGO_ENABLED=0` 构建，SHA256 为
  `66ccf016e4d991076fce8db79be07b146bbfc0d74947adf70afef5cdfad8d671`。
- L1 cert-manager CSI 原先因 `/tmp/cert-manager-csi-driver`，继而因
  `/var/lib/kubelet` 不是共享挂载而失败。对照 Sysbox 后，Sysrunc 的
  PVC 特殊挂载改为 `rshared`，并替换同目标的旧私有挂载。Server 重建后
  `/var/lib/kubelet`、`/run/k3s`、CSI 目录均为 shared；CSI driver
  3/3 Running，面板 agent 1/1 Running。L0 节点 `/usr/bin/sysrunc` 经
  debug Pod 备份更新，当前 SHA256 为
  `bbcd9e6b8475367df5b81cde348511fdbb4e53ec34f5ca64bc6666dac1ea5da9`。
- 本地集成镜像 `sysrunc-it:218-20261124-integrated`，镜像 ID
  `sha256:35e827e49c381b63a719946bc2282775fff2ad356d41c4afc37929d3de445caf`，
  含同哈希的 Sysrunc、FS 和静态 snapshotter；仅导入 L1 containerd。
  L1 Helm `sysrunc` revision 11 已部署；agent 1/1 Ready，FS 与
  snapshotter socket 可查询。L1 containerd 的 `sysrunc` handler
  `BinaryName` 指向当前 release，snapshotter 为 `sysrunc`。
- 新建 L2 `default/sysrunc-l2-nginx-uuid-1124` 与
  `default/sysrunc-l2-fs-uuid-1124` 均 1/1 Ready；CRI sandbox 分别为
  `SANDBOX_READY`、`runtimeHandler=sysrunc`。FS Pod 有
  `Seccomp: 2`、`Seccomp_filters: 1`，读取
  `/proc/sys/net/ipv4/ip_unprivileged_port_start` 得到 `0`，可写入并读取
  `/tmp` 文件。其 `/proc/sys` 仍显示 `sysboxfs` FUSE 挂载；这里只证明
  seccomp filter 已安装且实际 FS 功能正常，不把所有 FS 路径都标作无 FUSE。
- `local-path` 的两个 PVC
  `default/sysrunc-l2-webroot-uuid-1124` 与
  `default/sysrunc-l2-data-uuid-1124` 均 Bound。nginx 镜像的
  `index.html`、`50x.html` 自动初始化到空 webroot PVC；webroot 与
  `/srv/data` 标记跨 Pod 重建及 L0 Server 重建均保持，HTTP 读标记返回
  200。PVC 保留供复查。L1 没有其他 CSI StorageClass，本轮只覆盖
  `local-path`。
- 对相同 Chart 与集成镜像原样升级到 revision 11 后，L0 Server 的
  K3s container ID 与 restartCount、L1 agent UID 与 restartCount 均未变，
  三个 L2 Pod 仍为 1/1 Ready。Chart 包
  `dist/sysrunc-0.1.0.tgz` SHA256 为
  `6317035fca1fbf37d40e2e81a299fe3fffd0ed0a975b0d114b760ac9d358ffbe`。
  这是源码本地镜像验收；发布镜像与 Chart 仓库制品尚未验收，L0 Helm
  镜像也尚未更新到这次构建，节点上的 runtime 仍依赖手动更新。

## 2026-11-24 完整 Dockerfile 镜像与 L0/L1 续测

- 修正 `deploy/Dockerfile.sysrunc-k3s` 的 CentOS Stream DNF 来源为可配置的
  阿里云镜像，并从同一镜像下载 kubectl RPM。完整 Dockerfile 构建成功；
  `kubectl version --client` 为 v1.32.13，`fusermount3` 与
  `fuse-overlayfs` 均无动态链接器。最终本地镜像
  `sysrunc-it:218-20261124-verified` ID 为
  `sha256:420c5e667c3acd70bc509462ece2af2a4ced14a81edb96e9c0cb3ac4c4c251bf`。
  镜像内 Sysrunc、FS、snapshotter SHA256 依次为
  `bbcd9e6b8475367df5b81cde348511fdbb4e53ec34f5ca64bc6666dac1ea5da9`、
  `b58fce30d28682c02665763dc86764a493d70259985ceb28a05b1eafe9d48322`、
  `66ccf016e4d991076fce8db79be07b146bbfc0d74947adf70afef5cdfad8d671`。
- 安装器原先仅按版本号决定是否更新，版本同为 `0.1.0-0` 时会跳过新镜像
  的二进制。现增加镜像与宿主二进制哈希比较；K3s containerd 配置语义
  未变时跳过共享 K3s 重启。宿主并存 Bats 8 项及 Bash 语法检查通过。
- 完整 Dockerfile 的首个镜像
  `sysrunc-it:218-20261124-dockerfile`（ID
  `sha256:333b0e93d82a8a9dda3b753f02bfbcd5ac897d04daaee6c90eea0910b10dcf5c`）
  已导入隔离 L1，并部署到 Helm `default/sysrunc` revision 13。
  首轮 Helm 客户端经 L0 Pod 端口转发连接，L1 K3s 重启使连接中断，
  revision 12 停在 `pending-upgrade`；按相同 revision 的 manifest 回滚后，
  revision 13 为 `deployed`，agent 实际 imageID 与镜像 ID 一致。
  L1 release 中运行的是上述首个完整镜像，安装器安全修复只在随后
  构建的 `verified` 镜像中；三项运行时二进制哈希相同。
- 最终 `verified` 镜像通过 L0 `default/sysrunc-node-debug-1123` 导入节点。
  独立 L0 Helm `default/sysrunc-l0-it` 升至 revision 11，installer Pod
  imageID 为最终镜像 ID。安装日志确认哈希差异触发同版本更新，K3s
  containerd 配置不变且未重启共享 K3s；仅专用 Sysrunc CKM Server 被
  删除并重建。宿主三个二进制现与镜像哈希一致；节点 Ready、可调度，
  旧 Sysbox 工作负载仍运行。
- 当前专用 Server Pod
  `k3k-console-164315/k3k-ckm-sysrunc-it-1123-server-c9ff6d647-h25sk`
  为 2/2 Ready，L0 runtime `sysrunc`、`hostUsers:false`。L1 agent 1/1
  Ready，cert-manager CSI driver 3/3 Running。Server 重建后，将三个具名
  L2 测试 Pod 重建；普通、FS、双 PVC Pod 均 1/1 Ready。FS Pod UID 为
  `bc3e482b-2efc-4480-80ec-94c64f5eac45`，CRI sandbox 为
  `SANDBOX_READY`、`runtimeHandler=sysrunc`；`Seccomp:2`、
  `Seccomp_filters:1`。普通与 FS HTTP 均返回 200。webroot 与
  `/srv/data` 的标记 `sysrunc-webroot-1124`、`sysrunc-data-1124`
  均保留，两个 `local-path` PVC 保持 Bound。
- FS Pod 用 Perl 打开 `/proc/sys/kernel/random/uuid` 时，进程内 FD
  指向 `/memfd:sysrunc-proc-uuid (deleted)`，证明这一精确路径通过
  seccomp notify 的 `NOTIF_ADDFD` 注入。TAPD 需求所列 `statx`、
  `getdents64` 和一般 proc 路径匹配尚未实现；`/proc/sys` 仍为
  `sysboxfs` FUSE 挂载，不能宣称完整绕过 FUSE。
- 以上镜像均只在本地构建并导入集群，未推送仓库；Chart 也只使用本地
  `dist/sysrunc-0.1.0.tgz`。因此通过的是完整 Dockerfile 的本地制品
  集成测试，**发布仓库制品验收仍未运行**。

## 2026-11-24 L0 重复升级复验

- 用相同 Chart、相同 `sysrunc-it:218-20261124-verified` 镜像将独立 L0
  Helm `default/sysrunc-l0-it` 从 revision 11 升到 12，状态为 `deployed`。
  专用 CKM Server Pod UID 仍为
  `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd`，K3s Server container ID
  仍为 `containerd://bc06c186c78926c5d36aac7051f7e673ba694b45d1f98f3011028d299ea216eb`，
  restartCount 仍为 1。L0 installer Pod UID 仍为
  `e82d13ec-8560-47cd-add3-9e33983d79f4`，restartCount 仍为 0；
  `server1` Ready。
- `skills/sysrunc-in-sysrunc-test/scripts/check-status.sh` 对当前 L0 Server
  与 L2 普通、FS、双 PVC 三个 Pod 分别实跑通过：L0
  `runtimeClass=sysrunc`、`hostUsers:false`，
  L1 `sysrunc` handler 的 `BinaryName` 在当前 release 下且 snapshotter
  为 `sysrunc`，三个 L2 sandbox 均为 `SANDBOX_READY`、handler 为
  `sysrunc`，L2 `hostUsers` 均未设置。L1 agent 和 CSI driver 均为 1/1
  Ready；L1 Helm `sysrunc` revision 13 仍为 `deployed`。
- 普通、FS、双 PVC 三个具名 L2 Pod 均 Running，容器内 HTTP 均返回
  200。双 PVC Pod 的 `/usr/share/nginx/html/marker.txt` 与
  `/srv/data/marker.txt` 分别仍为 `sysrunc-webroot-1124`、
  `sysrunc-data-1124`；nginx `50x.html` 非空，两个 PVC 均 Bound。
  FS Pod 仍可读取 `/proc/sys/kernel/random/uuid`，当前进程显示
  `Seccomp:2`、`Seccomp_filters:1`。这不改变上节所述的 FUSE 绕过范围
  与发布制品未验收的边界。

## 2026-11-24 proc 目录与 statx 快路径续测

- 按 TAPD `1162789787001016334` 扩展 seccomp notify：`openat/openat2`
  对精确 `/proc/sys/kernel/random` 注入原生 procfs 目录 FD，由内核在此
  FD 上完成 `getdents64`；相对 `uuid` 打开仍注入 worker 生成的 memfd。
  `statx` 对这两个精确路径写回调用者缓冲区。`getdents64` 本身无需
  seccomp trap；`NOTIF_ADDFD` 无法填写 `statx/getdents64` 的输出缓冲区。
  未命中、被 mask 或带未支持标志的请求继续走内核/FUSE。
- 完整 Dockerfile 本地镜像 `sysrunc-it:218-20261124-proc-fastpath` ID 为
  `sha256:f9d3e32e0f6623c4a91d0f6fb720bbb80a1ce00d77e4e54922d395fadf319588`。
  镜像、工作区和 L1 当前 release 的 runtime/FS SHA256 均为
  `fba66f493ecfd4c9b5ee5d37ca59e3207060d4ed54696f7254bcad9bf3850428`、
  `35b3591ae0bf2ada6fa4ec6e9f0f7c1c44cd8a2781ed3349dc90ee55e42a78b4`。
  `go test ./seccomp` 和 `go test ./libcontainer/sysruncfs` 通过。
- 隔离 L1 Helm `default/sysrunc` revision 14 已部署此镜像；agent imageID
  与镜像 ID 一致。安装期间 L1 K3s 重启，三个具名 L2 测试 Pod 已重建。
  专用 L0 CKM Server 容器的 restartCount 由 1 增至 2；未操作其他 CKM。
  `check-status.sh` 分别通过：普通、FS、双 PVC Pod 的 CRI sandbox 均为
  `SANDBOX_READY`、handler `sysrunc`，L0 CKM 仍为 `sysrunc` 且
  `hostUsers:false`。三者 HTTP 均为 200，两个 PVC 保持 Bound，webroot
  和 `/srv/data` 标记保留。
- FS Pod 内的直接 syscall 探针通过：UUID `statx` 为 size=1024、
  mode=0444；`/proc/sys/kernel/random` 的目录 FD `fstatfs` 类型为
  procfs `0x9fa0`，`getdents64` 列出 `uuid`；相对 `statx` 仍为
  size=1024，相对 `openat` FD 指向 `/memfd:sysrunc-proc-uuid` 且读取
  37 字节。未命中的 `/proc/sys` FD `fstatfs` 类型仍为 FUSE
  `0x65735546`，普通 proc 文件 `statx` 成功。
- 当前加速范围仍限于上述两个精确路径。其余虚拟 proc 资源含有容器特定
  语义，不能直接把宿主 proc FD 注入容器；需要对应的直连 backing provider。
  这次镜像和 Chart 未发布到仓库，发布制品验收仍未运行。临时 overlay
  镜像和首次构建的无引用镜像已清理。

## 2026-11-24 七个虚拟 proc 文件直接处理

- `openat/openat2` 对 `/proc/cpuinfo`、`diskstats`、`meminfo`、`stat`、
  `swaps`、`uptime`、`loadavg` 直接调用已有的容器视角处理器，把快照注入
  memfd；`statx` 写回对应的虚拟 mode=0444、size=4096。只读 memfd
  设置了写入、扩容、缩容 seal，并以 `O_RDONLY` 注入；未匹配的路径、
  mask 和不支持的打开标志继续回退内核/FUSE。
- `/proc/slabinfo` 在专用 L1 user namespace 中无法读取宿主源文件，
  首轮集成探针发现它仍是 FUSE FD；该路径已从直接处理列表移除。
  FUSE 读取现在明确返回 `EACCES`，避免原先的 `EIO`。不能将这个路径
  记为绕过 FUSE。
- 完整 Dockerfile 最终本地镜像 `sysrunc-it:218-20261124-proc-handlers-ro`
  ID 为 `sha256:fa09ee02aa24ffd69ac23476aed1c873b6b5f4c108af06e6dc4419e5ce82b300`。
  L1 Helm `default/sysrunc` revision 17 已部署，agent imageID 与之相同；
  安装后的 FS SHA256 为
  `f9ad44ea1e5717a9f9b300944c932c1c656689b5752e4c24eda0c727ad832630`。
  使用相同 Chart 和镜像重复升级到 revision 18 后，专用 CKM 的 Pod UID、
  K3s Server container ID、restartCount，L1 agent UID/restartCount 以及
  L2 FS Pod UID 均未变化。
- FS Pod 内的 `proc-files-probe.c` 通过：七个文件均为不可写 memfd，
  `statx` 为虚拟属性，带 `O_NONBLOCK` 的打开仍是 FUSE FD；相对
  `/proc/meminfo` 打开与 `statx` 也通过。原 UUID/目录探针复跑通过。
  三个 L2 Pod 的 `check-status.sh` 均通过，CRI sandbox 为
  `SANDBOX_READY`、handler `sysrunc`；HTTP 均为 200，两个 PVC 保持
  Bound 且标记未丢失。`go test ./...` 在 FS 模块通过；修复了内存 FS
  测试隔离和目录测试对时间戳的不稳定断言。
- 当前 `/proc/sys` 的其余目录与可写虚拟文件仍通过 FUSE；发布仓库制品
  验收仍未运行。仅清理了无引用的中间 `proc-handlers`、
  `proc-handlers-v2` 镜像，保留当前镜像及前一稳定集成镜像。

## 2026-11-24 proc 目录 FD 安全回归

- 发现旧快路径把宿主 `/proc/sys/kernel/random` 目录 FD 注入 L2 后，
  未命中的相对路径可沿该 FD 解析到宿主 proc，破坏容器视图。已移除
  `openat/openat2` 的目录 FD 注入；目录及 `getdents64` 回退 FUSE，
  UUID 和七个只读文件的 memfd、精确 `statx` 快路径保留。
- `cd fs && go test ./...`、`make build`、完整 Dockerfile 本地构建通过。
  镜像 `sysrunc-it:218-20261124-proc-dir-safe` ID
  `sha256:2885330bc69b124873a20208f5f0b3ef32104b2713b3ec9e0b5504bf4e227fbd`。
  L1 当前 release 的 runtime、FS SHA256 分别为
  `ba10bb0e55d01a54a4aa17d37d9c75ca5bf8cf2a09b67979fa43a984c6630b80`、
  `6d7e1a4b540ffd22c83a6fa17d626b5c6798085483bab87c82b7fedf97baabdf`，
  与本地构建一致。镜像只导入隔离 CKM 的 L1 containerd，未发布。
- 首次 Helm revision 19 的 `--wait` 因安装器重启 L1 K3s、端口转发
  断开而停在 `pending-upgrade`。标准回滚到 revision 18 后以
  `--wait=false` 升至 revision 21，状态 `deployed`；相同 Chart/镜像
  再次升级至 revision 22。后一次升级未改变 L0 CKM Server 的 Pod UID
  或 K3s restartCount（8）、L1 agent UID 或 L2 FS Pod UID。
- K3s 重启后仅重建三个具名 L2 测试 Pod，PVC 保留。更新后的
  `proc-fastpath-probe.c` 在 FS Pod 中通过：random 目录 FD 的 `fstatfs`
  为 FUSE `0x65735546`，`getdents64` 含 `uuid`，相对
  `../hostname` 仍为 FUSE FD；相对 UUID 为 memfd、读取 37 字节，
  UUID `statx` 保持 size=1024、mode=0444。`proc-files-probe.c` 的七个
  只读文件 memfd、虚拟 `statx` 和不支持标志回退断言均通过。
- 三个 Pod 的 `check-status.sh` 均通过：L0 CKM 使用 `sysrunc` 且
  `hostUsers:false`，L2 sandbox 为 `sysrunc/SANDBOX_READY`，L2 未设置
  `hostUsers`。三个 HTTP 均返回 200；两个 `local-path` PVC Bound，
  webroot 与 `/srv/data` 标记分别仍为 `sysrunc-webroot-1124` 和
  `sysrunc-data-1124`。目录绕过 FUSE 与发布制品验收仍未完成。

## 2026-11-24 worker 生命周期与 rootfs 注解复验

- proc worker 的 `read_uuid` 请求改由对应容器的 goroutine 处理；容器
  未注册或注销后拒绝读取。旧 `sysbox/rootfs-rw-layer` 注解现在与新注解
  共用 rootfs 启用判定，使旧注解的 Pod 也获得预期 `/dev/fuse` 与
  `/dev/kmsg`；新注解值为 `false` 时优先关闭。另补齐
  `/var/lib/k0s` 持久特殊目录表。本地 FS 全套 Go 测试、worker race
  测试、runc 定向测试和完整镜像构建通过。k0s 与旧 rootfs 注解尚未在
  218 另建 Pod 验证。
- 最终本地镜像 `sysrunc-it:218-20261124-worker-lifecycle` ID 为
  `sha256:676fc8f5e30646720abfeeff80d4e477970ec9144c214f3fe02fe87c2710fc26`。
  L1 Helm revision 23 部署该镜像，agent imageID 一致；runtime/FS
  SHA256 分别为
  `c82ada710ff6a758f6b38d4fb6c55875f2843fdb6dbdb1280618cf3e2c08d893`、
  `d689eab09e94e943d78c4eb6c879f9a0886df7a934b1006d7ba70f43746119da`，
  与本地构建一致。`proc`、`fuse`、`ticker` 三个 worker 进程均在运行。
- K3s 重启后重建三个具名 L2 测试 Pod，保留 PVC。FS Pod 再次通过
  random 目录 FUSE 与 `../hostname` 防逃逸探针、UUID memfd 及七个
  只读 proc 文件探针。三个 `check-status.sh` 均通过，三个 HTTP 200，
  两个 PVC 标记仍在。相同 Chart 与镜像重复升级至 revision 24 后，
  CKM Pod UID 与 K3s restartCount（9）、L1 agent UID/restartCount（1）
  及 L2 FS Pod UID/restartCount（0）均未变化。
- 这仍是隔离 CKM 的本地镜像集成结果；其余 `/proc/sys` 可写路径和目录
  仍通过 FUSE，发布镜像与 Chart 验收未运行。
- 在 revision 24 的隔离 L1 中仅终止 proc worker PID 6123；supervisor
  于约 3 秒内启动新 PID 36655。原 FS Pod 未重建，随后直接重跑
  `proc-fastpath-probe` 通过，UUID 仍为 memfd 且读取 37 字节，证明
  worker 恢复后容器注册可重放。

## 2026-11-24 diskstats worker 与命名迁移复验

- `/proc/diskstats` 直接读取现由 proc worker 中对应容器的 goroutine
  计算，保留原 cgroup 视角，worker 请求失败时回退原处理器。FS 模块
  `go test ./...` 和 worker、seccomp、handler race 测试通过。三个组件
  `cmd/sysbox-*` 源码入口已迁至 `cmd/sysrunc-*`，根/组件 Makefile 与
  admission Dockerfile 构建入口已更新；完整 `make build` 通过。
- 最终本地镜像 `sysrunc-it:218-20261124-diskstats-worker` ID
  `sha256:52b80531103ac669a08c2a4007ca21631174c3d5db0fa8339330b0b5fe41df64`。
  L1 Helm revision 25 部署该镜像；L1 runtime 与 FS SHA256 为
  `c82ada710ff6a758f6b38d4fb6c55875f2843fdb6dbdb1280618cf3e2c08d893`、
  `e12a8dfe19ab0977ca61f189e35c9d3a56b3e0570b7293d7b7fab581683a8521`，
  与本地构建一致。
- K3s 重启后仅重建三个具名 L2 Pod，PVC 保留。FS Pod 的目录防逃逸、
  UUID、七文件探针均通过，其中 diskstats 为只读 memfd、虚拟
  `statx`、不支持的标志回退 FUSE。三个 `check-status.sh`、HTTP 200、
  两个 PVC 标记均通过。相同 Chart/镜像升级到 revision 26 后，
  CKM Server Pod UID 与 K3s restartCount（10）、L1 agent UID/
  restartCount（1）、FS Pod UID/restartCount（0）均未变化。
- 此轮仍为本地镜像集成测试；尚未使用发布仓库镜像/Chart，也未验证
  k0s 与旧 rootfs 注解的实际 Pod。其余虚拟 proc 路径仍需按权限、
  容器视角和写入语义逐项迁移。
- 独立 admission Dockerfile 的构建入口也已验证。首次构建因 Docker Hub
  超时，改用国内基础镜像后发现 `COPY . .` 把 Go 缓存带入上下文而写满
  Docker 存储；新增 `.dockerignore` 排除缓存，并允许指定 Debian 软件源。
  使用 DaoCloud 基础镜像、阿里云 HTTP apt 源及 goproxy.cn 后，独立
  镜像构建通过，容器内 `--version` 输出 `sysrunc-admission`。构建产生的
  无引用缓存约 2.9 GiB 已清理，临时 admission 镜像也已删除。

## 2026-11-24 swaps worker 与 IPC 源码命名复验

- `/proc/swaps` 的容器视角计算迁入对应容器的 proc worker；seccomp 快路径
  优先读取 worker，失败时回退原处理器。IPC 源码目录和 Go 包名从
  `sysboxFsGrpc` 改为 `sysruncFsGrpc`，原 protobuf service、方法名和
  socket 路径保持线协议兼容。IPC、FS、runc 的 Go 测试、FS 相关 race
  测试与根目录 `make build` 通过。
- 复用上轮已验证的完整安装镜像，叠加本轮四个新二进制形成**本地集成镜像**
  `sysrunc-it:218-20261124-swaps-ipc`，ID
  `sha256:68a96f559fc291706042eb633f191926bb26e2a0352ab8c037323357022858a4`。
  本轮完整 Dockerfile 构建因基础依赖缓存清理后下载耗时而中止；因此
  该镜像仅用于本轮代码的隔离集成测试，不算完整发布制品验收。
- L1 Helm revision 27 部署该镜像，installer Pod imageID 一致。L1
  runtime SHA256 为 `6f27bd2d29ff765bce9bce7f5c21d22021fc4994fa67ae56550fa23f8782f748`，
  FS SHA256 为 `c8907c2a3e5ebb815117da6871d0429d3df5c56f2a86af1fa892b0e0f1d8016c`，
  与本地二进制一致。K3s 重启后仅重建三个具名 L2 Pod，PVC 未删除。
- 两组 FS 探针通过；新增的 `/proc/swaps` 断言比较 worker memfd 与
  `O_NONBLOCK` FUSE 回退的内容，均为 81 字节且一致。三个
  `check-status.sh` 均通过，L0 CKM 使用 `sysrunc`、`hostUsers:false`，
  三个 L2 sandbox 为 `sysrunc/SANDBOX_READY` 且未设置 `hostUsers`。
  三个 HTTP 均为 200；webroot 和 `/srv/data` 标记仍分别是
  `sysrunc-webroot-1124`、`sysrunc-data-1124`。
- 相同 Chart 和镜像重复升级至 Helm revision 28 后，L0 CKM Pod UID
  与 K3s restartCount（11）、L1 agent UID/restartCount（1）、L2 FS Pod
  UID/restartCount（0）均未变化。
- 按 `skills/sysrunc-in-sysrunc-test/fixtures/legacy-rootfs-k0s-218.yaml`
  另建旧 `sysbox/rootfs-rw-layer` 注解测试 Pod 和独立 PVC。Pod 使用
  `sysrunc` 并注入 rootfs sidecar，Ready、PVC Bound、HTTP 200；
  `/dev/fuse`、`/dev/kmsg` 存在，`/var/lib/k0s` 为可写 idmapped 挂载。
  写入 `/var/lib/k0s/marker-1124` 后只重建该 Pod，标记
  `sysrunc-k0s-1124` 仍在。PVC 保留供复核。该测试验证了持久目录和
  旧注解路径；本次验收对象是 CKM K3s，无需启动 k0s。剩余 proc 路径和发布
  制品仍待验收。

## 2026-11-24 cpuinfo worker 复验

- `/proc/cpuinfo` 的容器视角计算迁入每容器 proc worker，seccomp 快路径
  优先请求 worker，失败时回退原 FUSE handler；tracee PID 丢失时 worker
  拒绝读取，避免取到宿主 cgroup。FS 全量 Go 测试、worker/seccomp/handler
  race 测试和 `make build` 通过。
- 本地集成镜像 `sysrunc-it:218-20261124-cpuinfo-worker` ID
  `sha256:4c7a169c0c1266c5e80898fa892cca94b0a798b4b9f2e28fc488068e2c9f2b8a`
  在隔离 L1 Helm revision 29 部署，installer imageID 一致。L1 FS
  SHA256 `3f3a1da283161572b7fd8bb0d4d372893c973855212e9ebbef1cec0feea73dde`
  与本地构建一致。该镜像仍是基于前一集成镜像叠加二进制，未发布。
- K3s 重启后只重建三个具名 L2 Pod，三个 `check-status.sh`、HTTP 200、
  UUID/目录安全探针和七文件探针均通过。三个 PVC 均 Bound，webroot、
  `/srv/data`、`/var/lib/k0s` 标记分别仍为 `sysrunc-webroot-1124`、
  `sysrunc-data-1124`、`sysrunc-k0s-1124`。
- 首次尝试逐字比较 CPUinfo 快路径与 FUSE 内容失败。检查完整输出后
  确认唯一差异是两次读取之间变化的 `cpu MHz`；去掉这些动态行后完整
  内容一致，双方均有 8 个可见处理器。`/proc/swaps` 仍做完整逐字比较。
  本轮未重复同镜像升级验证，也未完成完整 Dockerfile、发布制品、
  剩余 proc 路径及 getdents64 的安全直连。

## 2026-11-24 完整镜像与独立 daemon cgroup 复验

- 完整 `deploy/Dockerfile.sysrunc-k3s` 构建成功；Alpine 软件源改为可配置的
  清华镜像。镜像 `sysrunc-it:218-20261124-cgroup-v2` 的本地 ID 是
  `sha256:e91066c44518dd277305e585cf53e15d74e20a0a143d3b09ef42527323eb69e6`。
  L1 当前 agent 的 imageID 与此一致；FS 运行进程 SHA256 是
  `3f3a1da283161572b7fd8bb0d4d372893c973855212e9ebbef1cec0feea73dde`。
- revision 30 首次升级完整镜像时，旧 agent 被滚动替换，其子进程 FS
  随之退出。旧 L2 Pod 一度仍为 Running，但 FUSE 返回
  `Transport endpoint is not connected`，不能算无中断升级通过。
  `deploy/scripts/sysrunc-inner-k3s.sh` 随后把 FS、三个 worker 和
  snapshotter 在 exec 前移入 L1 的独立 `/sysrunc-daemons` cgroup；第一次
  实现因 Dash 后台函数的 `$$` 指向父 shell 而失败，改为 exec 新 shell。
- revision 32 验证 daemon 能在独立 cgroup 中启动。revision 33、34
  通过变更镜像标签触发真实 DaemonSet 滚动更新；FS PID 5993、
  snapshotter PID 6090 保持不变，两个进程均位于
  `0::/sysrunc-daemons`。L2 FS Pod UID
  `917e63ab-a028-4a3e-8e5c-e5d0b4ddaf13` 和 restartCount 0
  未变化；CKM K3s restartCount 保持 14。当前 L1 Helm revision 34
  的 secret 状态为 `deployed`，agent Pod `sysrunc-inner-agent-q7dpv`
  为 1/1 Ready，imageID 与上述完整镜像一致。
- 本次复查三个具名 L2 Pod 的 `check-status.sh` 均通过：L0 CKM Server
  使用 `sysrunc`、`hostUsers:false`；L2 sandbox 为
  `sysrunc/SANDBOX_READY` 且 `hostUsers` 未设置。三个 nginx Pod IP
  `10.52.0.23`、`10.52.0.22`、`10.52.0.24` 的 HTTP 请求成功；
  两个 PVC 分别保留 `sysrunc-webroot-1124`、`sysrunc-data-1124`，
  旧 rootfs 注解 Pod 的 `/var/lib/k0s` 标记仍为
  `sysrunc-k0s-1124`。三个测试 PVC 均为 Bound。
- Chart 的三个旧 `sysbox-*` 模板文件名已改为 `sysrunc-*`；
  `make chart` 的 lint、host 和 nested 渲染均通过。当前完整镜像是
  **本地集成镜像**，未发布。K3s Server 自身重启仍会结束该 PID
  namespace 内的 daemon，并使旧具名 L2 Pod 变 Unknown；发生时只重建
  测试 Pod，保留 PVC。CKM K3s 节点回滚、发布制品、
  `getdents64` 安全直连及其余 proc worker 迁移仍未验收。

## 2026-11-24 meminfo worker 源码检查

- `/proc/meminfo` 的容器 cgroup 计算已接入每容器 proc worker，缺失
  tracee PID 时拒绝读取；worker 请求失败时仍回退原 handler。
  `cd fs && go test ./...`、worker/seccomp race 测试和根目录
  `make build` 通过。本地新 FS SHA256 为
  `17d621459b2209204a465d690538180879c0217266bf2a2c735ff1e51945b1f9`。
- 完整镜像 `sysrunc-it:218-20261124-meminfo-worker` 已构建并直接导入隔离
  L1 containerd，本地及 L1 agent imageID 均为
  `sha256:7af2ca98c49279519fb63eb35ee19e6254d3fec0a119818ac6203916c5e5474f`。
  Helm revision 35 部署后因二进制变更重启 L1 K3s，CKM K3s
  restartCount 从 14 变为 15，旧具名 L2 Pod 变 Unknown。仅重建三个
  具名测试 Pod，未删除 PVC；三 Pod 均重新 Ready。
- 新 FS PID 6093 的 SHA256 为
  `17d621459b2209204a465d690538180879c0217266bf2a2c735ff1e51945b1f9`；
  FS 与 snapshotter PID 19347 均位于 `0::/sysrunc-daemons`。
  七文件探针通过：`/proc/meminfo` 是只读 memfd，`statx` 返回虚拟
  size/mode，带 `O_NONBLOCK` 的打开回退 FUSE；worker 与 FUSE 的
  `MemTotal` 相等。目录/UUID 防逃逸探针也通过。
- 三个 `check-status.sh` 均通过，L0 CKM 为 `sysrunc` 且
  `hostUsers:false`，L2 sandbox 为 `sysrunc/SANDBOX_READY` 且不设置
  `hostUsers`。三个 Pod HTTP 请求成功，webroot、`/srv/data`、
  `/var/lib/k0s` 标记仍分别为 `sysrunc-webroot-1124`、
  `sysrunc-data-1124`、`sysrunc-k0s-1124`；三个测试 PVC 均 Bound。
- 同镜像和 Chart 再次升级至 Helm revision 36 后状态 `deployed`，
  CKM Pod UID、K3s restartCount 15、FS PID 6093、snapshotter
  PID 19347、L2 FS Pod UID
  `65f594bb-fc73-4e3a-a260-bd7e1d8fdf91` 和 restartCount 0
  均未变化；七文件探针和 FS HTTP 再次通过。卸载 cgroup 清理代码只做
  shell 语法检查，尚未在仍有 L2 工作负载的现场执行卸载。
- 该镜像仍是本地集成镜像，未发布。`getdents64` 安全直连、剩余 proc
  与 ticker/FUSE worker 职责、CKM K3s 节点回滚和发布
  制品验收仍未完成。本地 Docker 已删除六个旧测试镜像标签和三个
  未启动、无挂载的临时构建容器；保留当前完整镜像与上一稳定的
  `cgroup-v2` 回退镜像。

## 2026-11-24 uptime worker 源码与 L2 复验

- `/proc/uptime` 的容器创建时间随注册消息传入每容器 proc worker；worker
  用 tracee PID 的 cgroup 计算完整快照，PID 或创建时间缺失时拒绝读取，
  失败时由 seccomp 路径回退原 FUSE handler。FS 全量 Go 测试、
  worker/handler/seccomp race 测试和 `make build` 通过。
- 完整镜像 `sysrunc-it:218-20261124-uptime-worker` 的本地 ID 与 L1
  agent imageID 均为
  `sha256:82af377428dc3af31c7b6155cc56a2f3e726cf1cf86a7e34f436744bddd6a782`。
  FS 二进制 SHA256 为
  `85a6403e427580982759ba00af9b00185ab0d103f05e8fb7567296cfa2d1f530`，
  与 L1 FS PID 6337 的 `/proc/6337/exe` 一致。
- Helm revision 37 部署后，二进制变化使 CKM K3s restartCount 从 15
  变为 16；旧三个具名 L2 Pod 变 Unknown。仅用原始清单重建这三个
  Pod，PVC 未删除，均重新 Ready。FS PID 6337 与 snapshotter PID
  19821 均位于独立 `0::/sysrunc-daemons`。
- FS Pod 的七文件探针通过：uptime 快路径为只读 memfd，虚拟 `statx`
  size/mode 正确，带 `O_NONBLOCK` 的打开回退 FUSE；worker 与 FUSE
  uptime 第一列在时间容差内。`/proc/meminfo` 的 `MemTotal` 对照、
  UUID/目录防逃逸探针也通过。三个 `check-status.sh` 均通过，L0
  CKM 使用 `sysrunc` 且 `hostUsers:false`；三个 L2 sandbox 为
  `sysrunc/SANDBOX_READY` 且未设置 `hostUsers`。三个 HTTP 请求
  成功，webroot、`/srv/data`、`/var/lib/k0s` 标记保留，三个测试
  PVC 均 Bound。
- 相同镜像/Chart 再次升级到 Helm revision 38，状态 `deployed`。
  CKM Pod UID、K3s restartCount 16、FS PID 6337、snapshotter
  PID 19821、L2 FS Pod UID
  `723243dc-252c-4eb0-ace2-e8d4b3f6ed4b` 和 restartCount 0
  均未变化；七文件探针再次通过。镜像仍仅在隔离 L1 使用，未发布。
  本地 Docker 现保留当前 `uptime-worker` 与上一个通过验收的
  `meminfo-worker` 镜像；旧 `cgroup-v2` 本地标签已清理，L1
  containerd 中仍有该回退镜像。

## 2026-11-24 ticker worker 采样与滚动升级复验

- ticker worker 不再只做进程存活检查。对
  `sysrunc.io/ticker=true` 的容器，它按 cgroup 与 PID namespace
  启动 loadavg 采样器，FUSE 与 seccomp 读取均优先请求该 worker；
  未启用 ticker 的容器仍用原 handler。注销容器时清理对应采样器。
  FS 全量 Go 测试、worker/handler/seccomp race 测试、`make build`
  和完整 Dockerfile 构建通过。
- 本地完整镜像 `sysrunc-it:218-20261124-ticker-worker` 与 L1 agent
  imageID 均为
  `sha256:d526ee05e5918eb3fdd5dd1ff2f1e04873c08c892d232bd1f6ae6ab2a9553ddb`；
  FS 二进制 SHA256 为
  `0dea931c892102c51111618c7f4ff2f99d03098362b2b7bc4b3f276bfc71fc85`，
  与 L1 运行进程 PID 6276 一致。
- Helm revision 39 更新二进制，CKM K3s restartCount 从 16 变为
  17；旧三个具名 L2 Pod 变 Unknown。仅用保存的原清单重建这三个
  Pod，保留三个测试 PVC。普通、FS、两 PVC nginx 三 Pod 与新
  `sysrunc-l2-ticker-1124` Pod 均 Ready；四个 sandbox 均为
  `sysrunc/SANDBOX_READY`，L0 CKM 用 `sysrunc` 且
  `hostUsers:false`，L2 均未设置 `hostUsers`。四个 HTTP 请求
  成功，webroot、`/srv/data`、`/var/lib/k0s` 标记仍保留。
- ticker Pod 带 `sysrunc.io/fs=true`、`sysrunc.io/ticker=true`。
  它的 `/proc/loadavg` 可读；持续约 15 秒 CPU 负载后，1/5/15
  分钟值从 `0.00 0.00 0.00` 变为 `0.13 0.04 0.01`。
  ticker worker PID 6292 持有两个 `anon_inode:[pidfd]`。
  删除该无 PVC 测试 Pod 后，worker pidfd 清空；重建并读取后
  pidfd 恢复，证明注册和注销清理路径实际执行。新 Pod UID 为
  `5c67dbd7-e908-4933-b85f-15bd2cc2f4ec`。
- 未启用 ticker 的原 FS Pod 继续通过七文件和目录/UUID 防逃逸
  探针；ticker Pod 的七文件探针也通过，`/proc/loadavg` 快路径为
  只读 memfd，带 `O_NONBLOCK` 打开回退 FUSE。
- 相同镜像重复升级到 revision 40，以及用同一 imageID 的别名
  触发真实 DaemonSet 滚动升级到 revision 41 后，CKM K3s
  restartCount 17、FS PID 6276、snapshotter PID 19678、
  ticker 采样 pidfd、FS Pod UID
  `36c8b19e-a867-458c-8dfd-169c7395a932` 和 ticker Pod UID
  均未变化；loadavg 读取与七文件探针继续通过。revision 42 将镜像
  标签恢复为 `ticker-worker`，agent Pod 再次滚动，以上状态保持；
  L1 中无引用的 rollout 别名已清理。当前 revision 42 为 `deployed`。
  本地 Docker 仅保留当前 `ticker-worker` 和上一稳定的
  `uptime-worker` 测试镜像。
- 镜像仍为本地集成制品，尚未发布。`/proc/stat`、其余 FUSE
  业务、`getdents64` 安全直连、CKM K3s 节点回滚和
  发布制品验收仍未完成。

## 2026-11-24 stat worker 与 L2 复验

- `/proc/stat` 的容器视角计算迁入每容器 proc worker。FS 全量 Go 测试、
  worker/handler/seccomp race 测试和根目录 `make build` 通过。完整本地镜像
  `sysrunc-it:218-20261124-stat-worker` 的 ID 与 L1 agent imageID 均为
  `sha256:02944e3d4706226d40ff0bcb435fa1032d80d52bb4e245a660819379324d1a39`；
  FS 二进制 SHA256 为
  `a4380deab4c9c74bac52dcd230a9b9e55f707554259589b13b1bfbe0b22124b3`，
  与运行中 FS PID 6455 的 `/proc/6455/exe` 一致。
- L1 Helm revision 43 部署后，CKM K3s restartCount 为 18。只重建四个
  具名 L2 测试 Pod，保留三个测试 PVC。普通、FS、两 PVC 和 ticker Pod
  均为 1/1 Ready；四个 `check-status.sh` 均通过，L0 CKM Server Pod
  UID `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd` 使用 `sysrunc` 且
  `hostUsers:false`，四个 L2 Pod 均不设置 `hostUsers`，CRI sandbox 为
  `sysrunc/SANDBOX_READY`。
- 更新后的 `proc-files-probe.c` 在 FS Pod 通过：七个文件均为只读 memfd，
  `statx` 给出虚拟 size/mode，`O_NONBLOCK` 回退 FUSE；`/proc/stat`
  worker 与 FUSE 视图均有 8 条 CPU 行，worker CPU 计数在相隔一秒的
  两次读取之间不下降。目录/UUID 防逃逸探针也通过，父目录相对
  `../hostname` 仍由 FUSE 处理。
- 四个 L2 nginx HTTP 请求均返回 200。webroot 和 `/srv/data` 标记为
  `sysrunc-webroot-1124`、`sysrunc-data-1124`，`index.html` 与
  `50x.html` 非空；独立旧 rootfs 测试 Pod 的 `/var/lib/k0s/marker-1124`
  仍为 `sysrunc-k0s-1124`。三个相关测试 PVC 均 Bound。ticker Pod 的
  `/proc/loadavg` 可读。
- 用相同 Chart 和镜像重复升级至 L1 Helm revision 44，状态为
  `deployed`。CKM Pod UID 和 K3s restartCount 18、agent Pod UID
  `7903fd6d-b992-4a5d-a5c9-ff99308c6227` 与 restartCount 1、
  FS PID 6455、snapshotter PID 19849、FS Pod UID
  `ca40513a-3982-4d7f-bd91-83ec48a52e78`、ticker Pod UID
  `415183fe-4c8a-4abf-b775-d0847162d68d` 均未变化；两个 L2 Pod
  restartCount 仍为 0。七文件及 `/proc/stat` 探针再次通过。
- 该镜像仅在隔离 L1 的 containerd 中使用，尚未发布。其余 FUSE 业务、
  `getdents64` 安全直连、CKM K3s 节点回滚和发布制品
  验收仍未完成。

## 2026-11-24 FS 协议文件改名与完整镜像复验

- TAPD 子项目命名检查发现 FS protobuf 源与生成文件仍以 `sysbox` 命名。
  两文件改为 `sysruncFsProtobuf`，Makefile、测试和注释同步更新；
  gRPC wire service `protobuf.sysboxStateChannel` 保留以兼容已有 socket
  客户端。`cd ipc && go test ./...`、根目录 `make build` 和完整
  `deploy/Dockerfile.sysrunc-k3s` 构建通过。
- 新完整本地镜像 `sysrunc-it:218-20261124-protocol-rename` ID 为
  `sha256:3e3fb7670fa55ff90161bf972253f60cc26b94251eaffeaddd9ce23bd0629acf`。
  runtime、FS、snapshotter 的构建 SHA256 分别为
  `605b2e7a4b2a499ed168d45d895b3ea70872fe14d3ee2e686e8fe49c54d72e63`、
  `246907c5dc4fd67b7d567ce0bd565dfa2df2e0b46c43444339e5e1da61662bc9`、
  `71c4789687f4c2d8c7f5967294f802dfd54e9b42f58ba4862c3836588dcb4fc0`。
  L1 agent imageID 与镜像 ID 一致，运行中 FS PID 6332 的 exe 哈希
  与构建 FS 一致。
- 首轮 Helm upgrade 因二进制更新触发 CKM K3s restartCount 18→19；
  L0 Pod port-forward 随 K3s API 断开，revision 45 留在
  `pending-upgrade`。此时新 agent 已 Ready 且运行新镜像。恢复转发后
  对相同 revision 45 manifest 执行 Helm rollback，revision 46 为
  `deployed`；仅重建四个具名 L2 测试 Pod，未删除 PVC。
- 四个 `check-status.sh` 均通过：L0 CKM Server UID
  `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd` 使用 `sysrunc`、
  `hostUsers:false`，L2 均未设置 `hostUsers`，sandbox 均为
  `sysrunc/SANDBOX_READY`。普通、FS、两 PVC、ticker Pod 的 UID 分别为
  `8c80d928-3425-4670-8291-9a19db55c095`、
  `9c13ae9d-1c99-41e0-b0f0-d9cef965c364`、
  `1812d035-3ef9-444f-965e-6386dc017e0e`、
  `d3cf55b0-6055-448c-b10a-72f74585f17e`，均为 1/1 Ready。
  四个 HTTP 均为 200；webroot、`/srv/data`、`/var/lib/k0s`
  标记及三个 Bound PVC 仍在。
- FS Pod 的七文件探针和目录/UUID 防逃逸探针均通过，包括
  `/proc/stat` 八 CPU、只读 memfd、FUSE 回退和计数单调断言。
  相同镜像/Chart 再升至 revision 47 后，CKM K3s restartCount 19、
  agent UID `7dafa147-f1f4-40c0-a47d-045a7b6f9f1c` 与 restartCount 1、
  FS PID 6332、snapshotter PID 19676、FS 与 ticker Pod UID 及
  restartCount 0 均未变化；探针再次通过。
- 镜像仍只在隔离 L1 使用，未发布。`getdents64` 安全直连、其余
  FUSE 业务迁移、CKM K3s 节点回滚和发布制品验收仍待完成。
  本地 Docker 已清理无容器引用的旧 `ticker-worker` 与
  `ko.local:sysrunc-it-20261124-reconcile-fs` 标签；L1 containerd 也
  清理了旧 `ticker-worker` 标签。保留当前镜像和上一稳定
  `stat-worker` 镜像作为回退。

## 2026-11-24 新旧 FS gRPC 服务并存与 L2 复验

- protobuf 增加主服务 `protobuf.sysruncStateChannel`，FS 同时注册旧
  `protobuf.sysboxStateChannel`；runtime 客户端优先调用新服务，仅当旧
  FS 返回 unknown service 时回退。IPC 单测覆盖新服务、旧服务回退与
  方法自身 `Unimplemented` 不重复执行，`cd ipc && go test ./...` 和
  根目录 `make build` 通过。
- 完整本地镜像 `sysrunc-it:218-20261124-protocol-service` ID 为
  `sha256:3d47612498f96a98a9a1037ba701188919520b79e01ac24686f150a04db6255b`。
  runtime、FS、snapshotter SHA256 分别为
  `5575d709b405ae2b6ccee76665aace1e0241bbe1a189453966f9366e7a10176b`、
  `1b7ee5dce4c429a6630e28f687c278081b5c1fe89b33366966aa7a5d951cc0d4`、
  `71c4789687f4c2d8c7f5967294f802dfd54e9b42f58ba4862c3836588dcb4fc0`。
  L1 agent imageID 与镜像 ID 一致；运行中 FS PID 6203 exe 哈希与构建
  FS 一致。
- 新镜像导入隔离 L1 后，用放在 CKM Server 容器内的 Helm 和校验过
  SHA256 的 Chart 包执行升级。revision 48 直接为 `deployed`，避免
  port-forward 在 K3s 重启时中断 Helm 客户端。CKM K3s restartCount
  从 19 增至 20，新 agent Ready。仅重建四个具名 L2 测试 Pod，PVC
  未删除。实际 L1 FS socket 上直接调用新旧两个 gRPC 服务，均返回
  `/var/lib/sysruncfs-inner`。
- 四个 `check-status.sh` 均通过：L0 CKM Server Pod UID
  `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd` 仍使用 `sysrunc`、
  `hostUsers:false`；普通、FS、双 PVC、ticker Pod UID 分别为
  `306c572c-5734-4c47-86dc-b45bb19ed7cc`、
  `e4ebcc72-847b-4637-90c2-bb20260d1ce7`、
  `a8f9a269-d1a0-4d7b-a87b-de86dc9b2e70`、
  `57f782a8-3a03-4c63-9e83-c423ea6879ce`，均为 1/1 Ready，
  `hostUsers` 未设置且 CRI sandbox 为 `sysrunc/SANDBOX_READY`。
  四个 HTTP 均为 200；webroot、`/srv/data`、`/var/lib/k0s` 标记
  与三个 Bound PVC 仍在。FS 七文件和目录/UUID 防逃逸探针通过。
- 同镜像/Chart 升级至 revision 49 后状态 `deployed`；K3s
  restartCount 20、agent UID `7ecc4fc0-8dc4-4b04-bfdc-1b96c2dd55b9`
  与 restartCount 1、FS PID 6203、snapshotter PID 19532、FS 与
  ticker Pod UID 及 restartCount 0 均未变化；七文件探针再次通过。
  镜像仍未发布，`getdents64` 安全直连、其余 FUSE 业务、CKM K3s
  节点回滚和发布制品验收仍待完成。
  本地 Docker 与 L1 containerd 已清理无引用的旧 `stat-worker` 镜像
  标签；保留当前 `protocol-service` 与上一稳定 `protocol-rename`
  镜像作回退。验收临时 Helm、Chart 包和 gRPC 探针已删除。

## 2026-11-25 getdents64 seccomp notify 直连与隔离 CKM 复验

- `/proc/sys/kernel/random` 的 `getdents64` 仅在原 FUSE 目录 FD、未被
  mask、有效 byte-offset cookie 和可安全写回 tracee 时由 seccomp notify
  直接返回。它通过 `pidfd_getfd` 共享原 FD 游标，不注入宿主目录 FD；
  非匹配目录和异常情况继续走内核/FUSE。`cd fs && go test ./...`、
  `go test -race ./seccomp ./worker ./handler/implementations`、
  `cd runc && go test ./libcontainer/sysruncfs` 和根目录 `make build` 通过。
- 完整本地镜像 `sysrunc-it:218-20261125-getdents-safe`，ID
  `sha256:904e60623cad37fbaf25968c20623483ee0b3d3ee48f637bab61f00ca6edf20d`
  在隔离 L1 Helm revision 50 `deployed`。四个具名 L2 Pod 重建后 Ready，
  `check-status.sh` 均通过，FS 目录探针返回 224 字节，分段读取、dup
  共享游标、rewind 和 `../hostname` 防逃逸通过。当时尚无独立命中日志。
- 为取得直接证据，FS 成功分支增加每进程一次的 info 日志。完整本地镜像
  `sysrunc-it:218-20261125-getdents-observe` ID 为
  `sha256:643f6cbaf9c148641660dc26ce621fcd34ca5210dbc07289993966770a875456`。
  runtime、FS、snapshotter SHA256 分别为
  `81ed9f59aba738f5e8ffa528c76397e1040bc20ecda9ff9c81fa4549006b3a92`、
  `0c2cc4a42218c5754f8de0c442c52f8c97f6f19e05a46ca0e5fdcaf5b1f2cc92`、
  `71c4789687f4c2d8c7f5967294f802dfd54e9b42f58ba4862c3836588dcb4fc0`。
  镜像压缩包校验后仅导入隔离 L1 containerd；Helm revision 51
  `deployed`，K3s restartCount 22。运行中 FS PID 6179 的 exe 哈希
  与上述 FS 哈希一致。FS Pod 直接 syscall 探针返回
  `getdents64 bytes=224`，`/var/log/sysrunc-fs.log` 同时出现
  `getdents64 fast path served /proc/sys/kernel/random through seccomp notify`。
  这是该路径实际命中直连分支的现场证据。
- 一次暂停 FUSE **repair** worker 的实验中，目标和 `/proc/sys` 对照目录
  均可读取；普通 FUSE 请求仍由 FS 主进程处理，因此该实验不能用作直连
  证据。worker 已恢复，误导性的临时探针源码已删除。
- revision 51 的四个 `check-status.sh` 均通过，L0 CKM Server 仍为
  `sysrunc`、`hostUsers:false`；L2 均未设置 `hostUsers`，CRI sandbox 均为
  `sysrunc/SANDBOX_READY`。FS 七文件与目录/UUID 探针通过，四个 nginx
  HTTP 均返回欢迎页。webroot、`/srv/data`、`/var/lib/k0s` 标记分别为
  `sysrunc-webroot-1124`、`sysrunc-data-1124`、`sysrunc-k0s-1124`；
  三个 PVC 均 Bound，未删除。FS Pod UID
  `cdb0de4f-acb8-4279-b7bd-766b8bc5bc97`，ticker Pod UID
  `6664f24a-d918-44d7-a19e-e1393f980f7d`。
- 相同镜像和 Chart 再升至 revision 52 `deployed`：L0 CKM Server UID
  `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd`、K3s restartCount 22、
  FS PID 6179、snapshotter PID 19792、agent UID
  `b85335fd-945d-4bec-b56f-a038941e2e4b`、FS 与 ticker Pod UID 及
  restartCount 0 均未变化；目录探针复跑通过。镜像仍未发布；其余 FUSE
  业务迁移、CKM K3s 节点回滚和发布制品验收仍未完成。
- 清理了 L1 containerd 中未被现有 Pod 引用的 23 个旧 `sysrunc-it`
  测试镜像标签，以及本地 Docker 的两个旧 protocol 镜像标签；保留当前
  `getdents-observe`、上一稳定 `getdents-safe` 和 admission 使用的
  `218-20261123g`。导入用压缩包及误导性实验探针已删除。清理后 L0
  CKM restartCount 仍为 22，四个 L2 Pod 均 1/1 Ready。
- 当前源码的 Chart 经 `make chart` 的 lint、host 与 nested 渲染通过，
  打包 `dist/sysrunc-0.1.0.tgz` SHA256 为
  `28bf23fd9003976364b5c881830f1805d726a99cc22aeeae223cfd6a2222b8ea`。
  校验后在隔离 L1 以相同镜像升至 revision 53 `deployed`；CKM UID、
  K3s restartCount 22、FS PID 6179、snapshotter PID 19792、agent 和
  FS/ticker Pod UID 均未变化，目录/UUID 探针继续通过。此 Chart 仍是本地
  包，尚非发布仓库制品。

## 2026-11-25 CKM K3s 范围确认与现场复查

- 验收对象为隔离 CKM 的 K3s，不启动 k0s。此前 `/var/lib/k0s` 的标记
  仅用于验证持久特殊目录；该 PVC 和标记保留。未完成的 k0s 下载已清理。
- L0 Server Pod `k3k-ckm-sysrunc-it-1123-server-c9ff6d647-h25sk`
  仍为 2/2 Running，UID `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd`，
  RuntimeClass 为 `sysrunc`、`hostUsers:false`，K3s restartCount 22。
  L1 Helm release `sysrunc` revision 53 仍为 `deployed`。
- 四个 L2 测试 Pod（nginx、FS、PVC、ticker）分别运行
  `check-status.sh` 全通过，均为 Running，未设置 `hostUsers`，CRI
  sandbox 为 `sysrunc/SANDBOX_READY`。从 CKM Server 容器内访问四个
  Pod IP 的 nginx 页面均成功；webroot、`/srv/data`、旧 rootfs 测试
  Pod 的 `/var/lib/k0s` 标记分别仍为 `sysrunc-webroot-1124`、
  `sysrunc-data-1124`、`sysrunc-k0s-1124`，对应三个 PVC 均 Bound。
  旧的 `sysrunc-l2-*-outerfs-1124` 和 `sysrunc-l2-smoke-1124` Pod
  显示 Unknown，本次未清理或计入四个具名验收 Pod。

## 2026-11-25 隔离 CKM 的 L1 安装版本回退与恢复

- 仅在隔离 CKM 内，将 L1 Helm `sysrunc` 从 revision 53 回退到已验证
  的 revision 50 内容（镜像 `218-20261125-getdents-safe`），生成
  revision 54 `deployed`。目标镜像已预置在 L1 containerd；L0 CKM
  Server Pod UID 未变，K3s restartCount 22→23。安装过程中 FS 和
  snapshotter socket 短暂不可用，随后 CKM Server 恢复 2/2 Ready，
  L1 agent Ready。新建 `sysrunc-l2-rollback-1125` Pod 的 CRI sandbox
  为 `sysrunc/SANDBOX_READY`，nginx 页面可访问。
- 再将同一 L1 release 回滚到 revision 53 内容（镜像
  `218-20261125-getdents-observe`），生成 revision 55 `deployed`。
  K3s restartCount 23→24，Server 恢复 2/2 Ready，L1 agent Ready，
  CRI `BinaryName` 恢复到 `ddb6c2fc8a6927029ebb1cf2525bef5222136cfcf2fc01c4ad1e0346721f9f6b`
  release 下的 `sysrunc`，snapshotter 仍为 `sysrunc`。
- 两次 K3s 重启使原四个 `restartPolicy: Never` 测试 Pod 显示 Unknown。
  只删除并按原 fixture 重建这四个具名测试 Pod，未删除 PVC；回退
  探针 Pod 已删除。重建后四个 `check-status.sh` 均通过，sandbox 均为
  `sysrunc/SANDBOX_READY`，四个 nginx HTTP 正常。webroot、`/srv/data`、
  `/var/lib/k0s` 的原标记保留，三个对应 PVC 均 Bound。
  FS 目录探针重新返回 224 字节并可 rewind；新 FS 日志记录
  `getdents64 fast path served /proc/sys/kernel/random through seccomp notify`。
- 本次证明隔离 CKM 的 **L1 安装版本**可回退并恢复；不证明不中断的
  工作负载升级，也未覆盖 L0 节点级运行时回退。测试用 Helm 和复制进
  FS Pod 的探针二进制已清理。

## 2026-11-25 ticker 读取移入容器 goroutine 与隔离 CKM 验收

- ticker worker 的 `read_loadavg` 改为发送给已登记、已启用 ticker 的
  容器 goroutine 处理；注销时取消该 goroutine 并释放 sampler。未启用
  ticker 的容器仍不创建 ticker 任务。`cd fs && go test ./worker
  ./handler/implementations`、`go test -race ./worker` 和根目录
  `make build` 通过。
- 完整本地镜像 `sysrunc-it:218-20261125-ticker-goroutine` ID
  `sha256:9c71fe4f32c2bac04026a520f18aa508d5611ff9630f953ebb96de2f5f081d8e`，
  FS SHA256 为 `6ffbf17d23c921b2aa88ef637090ae26bcccc6a053c7507c6bce724e70b5ee20`。
  镜像内 FS 文件与本地构建哈希相同；压缩包 SHA256
  `f6f926efc7c8c7bf4234e3059bd3060f4b1cef2c18cae2a7aa7a8d4236bd6aad`
  校验后仅导入隔离 L1 containerd，未发布。当前源码 Chart 包 SHA256
  `28bf23fd9003976364b5c881830f1805d726a99cc22aeeae223cfd6a2222b8ea`。
- 隔离 L1 Helm revision 56 `deployed`，K3s restartCount 24→25，
  CKM Server 恢复 2/2 Ready，agent Ready。运行中 FS exe 哈希与上面的
  FS 构建哈希一致；proc、fuse、ticker 三个 worker 进程均存在。
  只重建 K3s 重启后显示 Unknown 的四个具名 L2 测试 Pod，未删除 PVC。
- 四个 `check-status.sh` 均通过，L0 CKM Server 仍为 `sysrunc`、
  `hostUsers:false`；L2 均未设置 `hostUsers`，sandbox 均为
  `sysrunc/SANDBOX_READY`。ticker Pod 实际读取 `/proc/loadavg`
  返回 `0.00 0.00 0.00 0/9 36`；ticker 和 FS Pod 的七文件探针通过，
  FS 目录探针返回 224 字节并可 rewind，FS 日志记录新进程命中
  `getdents64` seccomp notify 直连。四个 nginx HTTP 均正常，webroot、
  `/srv/data`、旧 rootfs 测试 Pod 的 `/var/lib/k0s` 标记保留，对应三个
  PVC 均 Bound。
- 相同镜像和 Chart 再升级至 revision 57 `deployed` 后，CKM Pod UID
  `b29276c3-3494-4ad6-b7dc-ba62bd5f5ecd`、K3s restartCount 25、
  ticker worker PID 6124 和四个 L2 Pod UID 均未变化。导入用压缩包、
  临时 Helm、Chart 包和 Pod 内探针已清理；L1 containerd 与本地 Docker
  的旧 `getdents-safe` 测试镜像标签已删除，保留当前镜像和上一稳定
  `getdents-observe` 镜像用于回退。

## 2026-11-25 getdents64 count 宽度修复与隔离 CKM 复验

- `getdents64` 的 `count` 在内核中是 32 位 unsigned int，但 seccomp
  通知包含完整 64 位寄存器。修复前，FS Pod 的原始 syscall 探针以
  `count=16` 返回 `-1/EINVAL`，以 `count=0x100000010` 却由直连
  分支错误返回 224 字节；后者可能超过内核实际接受的长度。直连现在
  对高 32 位非零的请求放行到内核/FUSE。探针源码保留在
  `fs/seccomp/testdata/getdents_count_probe.c`。
- `cd fs && go test ./seccomp`、`go test -race ./seccomp` 和根目录
  `make build` 通过。完整本地镜像
  `sysrunc-it:218-20261125-getdents-count` ID
  `sha256:bad8cc20379c473cfe01dffc102071898445bf1e56e084274d2602a539fa0126`，
  FS SHA256 `752f7d156f8fc60f23ded8d5082733aabd33ae2b893de284d553909a26b2609d`
  与镜像内文件一致。压缩包 SHA256
  `808af711d102be51b44b5179784d939a70cfc568031c70d2dd13f5068126d497`
  校验后仅导入隔离 L1，未发布。
- L1 Helm revision 58 `deployed`，CKM K3s restartCount 25→27，
  Server 恢复 2/2 Ready，L1 agent Ready。只重建 K3s 重启后 Unknown
  的四个具名 L2 测试 Pod，未删除 PVC。运行中 FS exe 哈希与构建一致。
  同一原始 syscall 探针返回 `small=-1 errno=22 high=-1 errno=22`；
  正常目录探针仍返回 224 字节且可 rewind，新 FS 日志记录直连命中。
- 四个 `check-status.sh` 均通过，CRI sandbox 均为
  `sysrunc/SANDBOX_READY`，四个 nginx HTTP 正常，ticker Pod 的
  `/proc/loadavg` 可读；webroot、`/srv/data`、旧 rootfs 的
  `/var/lib/k0s` 标记保留，三个 PVC 均 Bound。相同镜像与 Chart 再
  升至 revision 59 `deployed`，CKM Pod UID、K3s restartCount 27、
  FS 进程和四个 L2 Pod UID 均未变化。临时镜像包、Helm、Chart 包和
  Pod 内探针已清理；旧 `getdents-observe` 镜像标签从本地 Docker
  与 L1 containerd 删除，保留当前镜像和上一稳定
  `ticker-goroutine` 镜像供回退。

## 2026-11-25 全新 Sysrunc CKM 验收

- 新建 `k3k-console-164315/ckm-sysrunc-fresh-1125`，L0 Server Pod
  `k3k-ckm-sysrunc-fresh-1125-server-77df96d5b8-h77rm` 使用 `sysrunc`、
  `hostUsers:false`，无需 Pod FUSE volume，`/dev/fuse` 可用；系统盘为
  `local-path` PVC。首次 K3s 启动因 `kubepods` controller 尚未就绪而
  退出两次，随后 cgroup delegation 生效，CKM、K3s node 和系统 Pod
  达到 Ready。这是重试后通过，不是首次启动无错误。
- 线上 CKM controller 仍为旧本地镜像
  `ko.local:sysrunc-it-20261124-reconcile-fs`，未安装本次拉取合并的
  `w7panel-ckm` 源码。它生成的 Server 缺少 `ckm-registries` volume；
  新 SystemTemplate 的 K3s 脚本因而无法复制 registry mirror，L1
  起初从 Docker Hub 拉取 pause image 超时。仅对本测试 Server 写入
  主集群 `registries` ConfigMap 的 `default.cnf` 并重启 K3s 后，系统
  Pod 完成启动。手工给 Deployment 增加 volume 曾被旧 controller 调谐
  撤销。合并后的源码已添加该 volume 和镜像源 ConfigMap，但本轮未
  将新 controller 部署到线上；这个冷启动修复仍需用新 controller 复验。
- 本地完整镜像 `sysrunc-it:218-20261125-getdents-count` ID
  `sha256:bad8cc20379c473cfe01dffc102071898445bf1e56e084274d2602a539fa0126`
  导入新 L1 containerd。源码 Chart 包 SHA256
  `28bf23fd9003976364b5c881830f1805d726a99cc22aeeae223cfd6a2222b8ea`；
  L1 `sysrunc` Helm revision 1/2 deployed，agent 与 admission Ready。
  CRI handler 的 `BinaryName` 指向该 release，runtime SHA256
  `907674f448f08d3f47beb84cf4088cc41e01f466c930e46aaa5355a5c4ac02f3`
  与本地构建一致；snapshotter 为 `sysrunc`，runtime platform mapping 已加载。
  `native` snapshotter 首次创建两个大镜像 Pod 曾超过 2 分钟并留下
  `CreateContainerError`，重试后恢复；当时 native snapshots 占用约 37 GiB。
- L2 `default/sysrunc-fresh-nginx-1125` 和 `default/sysrunc-fresh-fs-1125`
  均 Ready，CRI sandbox 均为 `sysrunc/SANDBOX_READY`。普通 nginx 的
  空 webroot PVC 自动含 `index.html`、`50x.html`；Pod UID 从
  `6dfbd734-a34a-4b57-9f90-2322ebc6501f` 换为
  `0eba0af2-25be-4087-8a88-4e3600edb750` 后，HTTP 返回 webroot
  标记，`/srv/data` 标记也保留。两个 `local-path` PVC 均 Bound。
  FS Pod 读取 `/proc/meminfo`、`/proc/stat`、UUID 并写入 `/tmp`；
  UUID fd 指向 `/memfd:sysrunc-proc-uuid`，进程有 1 个 seccomp filter。
  两个 L2 Pod 的 `check-status.sh` 均通过。
- 相同镜像与 Chart 升至 L1 revision 2 后，L0 Server UID
  `d121b428-5b3f-4475-b142-a64f7daa098e`、K3s restartCount 3、
  agent/admission UID 和两个 L2 Pod UID 均未变化，HTTP 标记可读。
  测试 CKM、Pods 与 PVC 保留供复查。本轮仅用本地镜像与 `local-path`；
  发布制品、L1 CSI 和合并后 CKM controller 的冷启动仍未验收。

## 2026-11-25 CKM controller CCR 镜像及 Sysrunc 权限复验

- `w7panel-ckm` Makefile 默认 `KO_DOCKER_REPO` 改为
  `ccr.ccs.tencentyun.com/afan-public/w7panel-ckm`。通过
  `make build-use-ko KO_IMAGE_TAG=sysrunc-security-218-20261125`
  构建并推送，镜像 digest 为
  `sha256:c2317c4fac568e50bc34c432f0d84e3a8c9addfb7881c78e7f33df93b007bad4`。
  L0 `default/w7panel-ckm` Deployment 已切换至该 tag，1/1 Ready；
  Pod 实际 imageID 与 digest 一致。切换前 Deployment JSON 备份于
  `/tmp/w7panel-ckm-218-before-sysrunc-security-20261125.json`。
- L0 Sysrunc 使用完整本地镜像
  `sysrunc-it:218-20261125-oci-security-exec`，image ID
  `sha256:2faaf4573686b8042818ab9aacfcd63117e7577b8e38f1f5a3e0af6d2b32d800`；
  运行中的 `/usr/bin/sysrunc` SHA256 为
  `7749f000c3b23478750630dd2e586d40f939829ba7856f54c37fe6b1d8cb402a`，
  与本地构建一致。隔离 L0 Helm `default/sysrunc-l0-it` revision 14
  `deployed`，installer 与 admission 均 Ready。此 Sysrunc 镜像仅导入
  L0 节点，未发布镜像或 Chart。
- 新建 `k3k-console-164315/ckm-sysrunc-security-1125`，CKM phase
  `ready`、clusterPhase `Ready`。Server Pod
  `k3k-ckm-sysrunc-security-1125-server-7d96dc999-hwkcr` 为 2/2 Ready，
  使用 `runtimeClassName: sysrunc`、`hostUsers: false`；K3s Server
  `privileged: false`，没有单独配置 capabilities、AppArmor 或 procMount。
  `ckm-registries` 与 `varlibrancherk3s` volume 存在，L1
  `registries.yaml` 自动生成；cert-manager、Higress 和
  `w7panel-offline-agent` 均 Ready，无需手工补 registry 配置。
- 第一轮本地运行时镜像使 L1 K3s 启动，但 `kubectl exec` 进程执行
  `iptables` 时权限不足；补齐嵌套系统容器 exec 的 OCI 权限转换后，
  在最终 Server Pod 内执行 `iptables -t nat -L POSTROUTING -n`
  成功，`CapEff` 为 `000001ffffffffff`、AppArmor 为 `unconfined`，
  `/proc/sys/net/ipv4/conf/all/route_localnet` 可写。
- 新 controller 也清除了旧测试 CKM Server 上此前为 Sysrunc 单独添加
  的权限。`ckm-sysrunc-fresh-1125` 与 `ckm-sysrunc-it-1123` 均恢复
  2/2 Ready。前者 L1 的 nginx 和 FS 测试 Pod 在两次 L0 Server 重建后
  为 Failed/Unknown；只重建这两个具名 Pod，未删除 PVC。新 Pod UID
  分别为 `2d37bf0b-299e-4dab-a614-b918ce2de4d4` 和
  `24c8d6f1-d0e7-4b5e-a526-4dfbb49891a7`，CRI sandbox 均为
  `sysrunc/SANDBOX_READY`，状态脚本通过。两个 `local-path` PVC
  `sysrunc-fresh-webroot-1125`、`sysrunc-fresh-data-1125` 仍 Bound，
  标记保留；nginx HTTP 返回 200，FS Pod 可读 `/proc/meminfo` 和 UUID、
  可写 `/tmp`。
- 本次新建 CKM 复验了 L1 K3s 冷启动；未在该 CKM 安装 L1 Sysrunc
  Chart，也未运行其 L2 工作负载。L2/PVC 结果来自已有的
  `ckm-sysrunc-fresh-1125`。本轮只有 `local-path` 存储验证。

## 2026-11-25 Sysrunc Chart 重施与 218 复验

- 仓库已有参考 Sysbox 结构的 `charts/sysrunc`，包含 installer、
  RuntimeClass、RBAC、admission 和 nested agent 模板；无需再创建第二套
  Chart。`make chart` 的 Helm lint、host 和 nested 渲染均通过。
- 在 218 隔离 release `default/sysrunc-l0-it` 上使用现有本地镜像
  `docker.io/library/sysrunc-it:218-20261125-oci-security-exec` 及
  `--reuse-values` 重施源码 Chart，revision 14→15 `deployed`。
  installer DaemonSet 1/1 Ready、admission Deployment 1/1 Ready；
  `sysrunc` 与旧 `sysbox-runc` RuntimeClass 均存在，旧 Sysbox
  admission Deployment 1/1 Ready。
- `ckm-sysrunc-security-1125` 仍为 Ready；其 Server 内 exec 的
  `CapEff=000001ffffffffff`、iptables 和 `route_localnet` 写权限检查
  通过。已有 `ckm-sysrunc-fresh-1125` 的 nginx、FS L2 Pod 仍使用
  `sysrunc/SANDBOX_READY`，UID 未变；两个 `local-path` PVC 仍 Bound，
  nginx HTTP 200，FS Pod 可读取 `/proc/meminfo`、UUID 并写入 `/tmp`。
  此次是相同本地镜像的 Chart 重施验证，未发布 Sysrunc 镜像或 Chart，
  也未在新建 CKM 上安装 L1 Chart。

## 2026-11-25 发布制品与内外层 release 统一

- 完整镜像已推送至
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:v0.1.0`，digest
  `sha256:075d7513282b9200387993c6a5e12578f5691a35f5aaffd5ba6a8e8e961663ab`。
  仓库设置公开后，无 Docker 凭据的 manifest 查询通过。Helm Chart
  `sysrunc-0.1.0.tgz` 作为 ZPK 应用 `w7panel-sysrunc` 的 1.0.0 版本附件
  发布，现场包 SHA256 为
  `b0be166ebded8bf8b8f845733c440b2756019c8e523f091c41015630f8c48431`。
- L0 原隔离 release `default/sysrunc-l0-it` 保留卸载历史，安装相同
  values 的新 release `default/w7panel-sysrunc`，revision 1 `deployed`。
  installer DaemonSet `w7panel-sysrunc-installer` 1/1 Ready，admission
  Deployment `w7panel-sysrunc-admission` 1/1 Ready，均使用已发布 CCR 镜像。
  旧 Sysbox 安装未改动。
- 专用 CKM `k3k-console-164315/ckm-sysrunc-fresh-1125` 的 L1 旧 release
  `default/sysrunc` 保留卸载历史，新 release `default/w7panel-sysrunc`
  revision 2 `deployed`。安装时 agent 更新触发 K3s 重启，导致等待中的
  Helm 客户端退出，revision 1 留在 `pending-install`；跳过卸载钩子清理
  该记录后重新安装成功。inner agent
  `w7panel-sysrunc-inner-agent` 1/1 Ready，admission
  `w7panel-sysrunc-admission` 1/1 Ready，两个 Pod 的 imageID 均为上述
  CCR digest。运行中 Sysrunc 二进制 SHA256 为
  `9dd862ae7f9f670763e2b03ef40c1a8e70d451f211d4f5830dd35eafbdde94d8`，
  与已发布镜像内 `/opt/sysrunc/bin/generic/sysrunc` 一致。
- 只重建该 CKM 内的两个具名测试 Pod，未删除 PVC。nginx Pod UID
  `04d398a2-aca1-4ec5-845c-981eb92c6f73`，FS Pod UID
  `7be02209-7fe4-4248-ae31-d02c350a9b17`；两者状态脚本通过，CRI
  sandbox 都是 `sysrunc/SANDBOX_READY`。nginx HTTP 200，webroot 中
  `index.html`、`50x.html` 及原有标记均存在，`/srv/data` 标记保留。
  FS Pod 可读 `/proc/sys/kernel/random/uuid` 和 `/proc/meminfo`。
  本轮未在另一专用 CKM `ckm-sysrunc-security-1125` 安装内层 Chart，
  也未测试 CSI StorageClass。

## 2026-11-25 L2 rootfs 持久层复验

- 在专用 `ckm-sysrunc-fresh-1125` 的 L2 创建
  `default/sysrunc-l2-rootfs-1125` 与独立 `local-path` PVC。Pod 使用
  `runtimeClassName: sysrunc` 和 `sysrunc.io/rootfs-rw-layer` 注解；
  admission 注入 `sysrunc-rootfs` sidecar。首次用 `restartPolicy: Never`
  时，应用容器在 sidecar OCI spec 可用前报
  `sysbox sidecar oci spec unavailable` 并停在 `StartError`。
- 改用正常的 `Always` 策略重建同名测试 Pod 后，应用容器首次尝试仍可能
  遇到该错误，但自动重试一次后 Ready。CRI sandbox 为
  `sysrunc/SANDBOX_READY`，nginx HTTP 200。在容器根文件系统的
  `/root/rootfs-marker-1125` 写入 `rootfs-1125-persist` 后，只重建该
  Pod，UID 从 `ddbd4eec-03db-40b2-898f-3f4f8084a88c` 变为
  `ff28c9e4-4e24-442c-9d56-e333f40f4116`。新 Pod 仍可读该标记且
  HTTP 200；rootfs PVC `sysrunc-l2-rootfs-1125` 保持 Bound。
- 因此首次 sidecar spec 错误仅在后续自动恢复 Ready 时可忽略；持续
  `StartError` 仍是失败。本轮验证了发布镜像在 `local-path` 上的 L2
  rootfs 持久化，未验证 CSI StorageClass。

## 2026-11-25 rootfs 复制与 seccomp 快路径发布验收

- 提交 `ecaa32b` 的 rootfs 复制恢复与三个只读 UTS 文件快路径先以
  `w7panel-sysrunc:v0.1.1` 发布。218 专用 CKM 的新空 PVC Pod
  `default/sysrunc-seed-v011-1125` 在首次复制时失败：内层
  `/usr/local/bin/rsync` 指向动态链接的 CentOS 程序，而最小 K3s rootfs
  没有 glibc loader。Pod 保留在现场，PVC 未删除。
- 修复镜像将 rsync 3.2.7 静态编译，并检查 `readelf` 无 INTERP，
  `rsync --version` 明确报告 ACLs 和 xattrs。`v0.1.2` 已推送到
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc`，digest 为
  `sha256:68a02d1f56570d2e03f9091da3528585dcd549ab1c0397f7dc5e9450a621f4ed`。
  Chart `sysrunc-0.1.2.tgz` SHA256 为
  `95ba2ec2bec32a3e7466b7eaa8f83fd04914f76f362765208c85ce636dfffbd1`，
  已作为 ZPK `w7panel-sysrunc` 1.0.2 版本发布。
- L0 `default/w7panel-sysrunc` revision 3、L1 同名 release revision 4
  均为 `deployed`；两层 installer 和 admission 的 imageID 都匹配上述 digest。
  L1 同版本重施到 revision 5 后，CKM Server UID
  `b3e7901e-f20e-4f4c-b911-87d43547e0d0`、重启数 3 和测试 Pod UID
  均未变化。L1 运行中的 runc 与 FS SHA256 分别为
  `304d03eea145dbd2635e32687b6ce0875d0ba35f5c32ee8349cff980a33629a0`
  和 `c6e7efcd6e32c9fcf8e7ef689b375e4f3c65454ce9797de03831d4523b108895`，
  与本地发布构建一致。
- 原失败的 `sysrunc-seed-v011-1125` 在 0.1.2 下自动恢复 Ready；空 PVC
  出现 `index.html` 和 `50x.html`，失败标记已清除。写入自定义首页与
  `persist-v012` 后只重建该 Pod，新 UID
  `afc44ceb-fbd8-43f6-bdac-cd367223be24` 仍读到两个文件且 HTTP 200。
  新建 `sysrunc-special-v012-1125` 验证 `/usr/share/nginx/html` 特殊目录
  首次复制；写入该目录与 `/root` 的标记后重建，UID 由
  `fce28600-99d9-4baf-9f5b-75668a231580` 变为
  `3beda2e7-a2b0-4b00-8e5f-64fb8c93f5e5`，标记保留且 HTTP 200。
- FS Pod `sysrunc-fresh-fs-1125` 的精确 `ostype`、相对路径 `osrelease`
  和 `version` 打开均得到 `memfd:sysrunc-*` 描述符。四个具名 L2 Pod
  的状态脚本确认 `sysrunc/SANDBOX_READY`；旧 nginx 与 rootfs HTTP 200，
  原 PVC 和 `/root/rootfs-marker-1125` 均保留。本轮只使用 L1 唯一的
  `local-path` StorageClass；CSI 与吞吐量化未运行。所有测试 PVC 保留。

## 2026-11-25 L0 Longhorn `disk-default` 复验

- `disk-default` 只存在于 218 外层 L0 集群，provisioner 为
  `driver.longhorn.io`。在 L0 `default` 创建两个独立的 1Gi PVC：
  `sysrunc-longhorn-seed-1125` 和 `sysrunc-longhorn-special-1125`。
  两者均 Bound，对应 PV 的 CSI driver 均为 `driver.longhorn.io`。
- 两个同名 Pod 使用 `runtimeClassName: sysrunc` 和
  `ccr.ccs.tencentyun.com/afan-public/nginx:latest`，实际镜像 digest 为
  `sha256:29cf9892ca1103e0b8c97db86f819fac1d9457b176bc77dd4f18ed2da4dd159f`。
  普通挂载用例的空 PVC 自动得到 `index.html` 与 `50x.html`。
  rootfs 用例通过 `sysrunc.io/rootfs-rw-layer` 的
  `persistentSpecialMounts` 和 `/usr/share/nginx/html` `specialPath`
  自动注入 `sysrunc-rootfs` sidecar，特殊目录同样完成首次复制；
  容器内该目录的挂载源为 `/dev/longhorn/pvc-50900d5a-fba7-4db2-a696-554ee0c70e4f`。
- 分别写入 webroot 标记，并在 rootfs 用例额外写入 `/root` 标记。
  删除并重建两个 Pod 后，UID 分别从
  `2a38a2a9-6c09-4fd6-973b-d0147104bf46` 变为
  `6f6e0b00-af28-426a-bb91-35cab8441da5`，以及从
  `5c2243bd-d51c-4fcd-9854-db2301c5f3a6` 变为
  `49dd44b5-6538-4db8-840e-649cf2dce72f`。
  三个标记及两个 nginx 默认文件均保留，两个 Pod 均 Ready，
  经 port-forward 访问均返回 HTTP 200。测试 PVC 保留。
- 本次验证的是 **L0 Longhorn CSI**；专用 CKM 内层 L1 仍只有
  `local-path` StorageClass，不能据此视为 L1 CSI 验收。未进行吞吐量化。

## 2026-11-26 FS 虚拟文件只读快路径源码集成

- 在三个 `/proc/pressure/{cpu,io,memory}`、两个
  `/sys/devices/system/cpu/{online,present}` 和两个
  `/proc/sys/kernel/{cap_last_cap,ngroups_max}` 精确路径上复用容器视角
  处理器，通过 seccomp notify 返回只读 memfd。普通只读打开才命中；
  `O_NONBLOCK` 等其他方式仍走原路径。OCI sysfs maskedPaths 也传给
  seccomp 判定，但新建 masked Pod 的端到端负例尚未运行。
- 完整 Dockerfile 构建在 Docker Hub 获取 `alpine:3.20` 元数据时超时，
  沙箱内外重试一致。本轮改用本地已有的已发布 `v0.1.2` 镜像作为测试基底，
  仅替换本地构建的 Sysrunc 和 FS 二进制；镜像
  `sysrunc-it:218-20261126-fs-virtual-fix3` ID 为
  `sha256:1d9c01762ef0bd0aa01eed4fa1fa2ac5d541d30adc0ffd63ac0f61fd387d505f`。
  此镜像只导入专用 CKM 的 L1 containerd，未推送 CCR 或发布 Chart。
- L1 `default/w7panel-sysrunc` revision 9 `deployed`，agent 与 admission
  Ready。运行中 runc 和 FS SHA256 分别为
  `c5df740aca100127713e662d70721d68a95238110829aaf0e953e825a4d468dc`
  与 `d702f540c917351374576af23254b6d01470d2d3f411b21d989898d9329dc8dd`，
  与本地构建相同。二进制更新触发 K3s 重启，两个 `restartPolicy: Never`
  的旧 FS/普通测试 Pod 变为 Unknown；只按原清单重建这两个 Pod，PVC 未删。
- 首版测试镜像暴露了已缓存的内核 sysctl 再读返回 NUL 的问题：旧直读循环
  未使用处理器替换后的缓存切片。修复后七个路径均连续打开两次，FD
  均为 `memfd:sysrunc-*`，内容正确；`cap_last_cap=40`、
  `ngroups_max=65536`。三个代表路径带 `O_NONBLOCK` 打开时 FD 仍指向
  原文件。FS 与普通 L2 Pod 的 `check-status.sh` 均通过，CRI sandbox
  均为 `sysrunc/SANDBOX_READY`；四个 nginx Pod 可访问，已有 webroot、
  `/srv/data`、rootfs 特殊目录标记和五个 `local-path` PVC 保留。
- 对 sysfs 掩码的复查发现，它不能直接参与 proc 挂载路径的相对匹配；
  最终镜像将 proc 挂载解析限定为 `/proc` 掩码，避免把 `/sys` 误判成
  `/proc/sys`。相应回归测试和 FS 全量 Go 测试通过。
- 本轮确认的是这七个精确只读路径的源码集成；其余虚拟目录、可写
  `/proc/sys` 与 `/sys` 路径仍由 FUSE 处理，未完成全量绕过或吞吐量化。

## 2026-11-26 installer 宿主挂载优化与 v0.1.3 发布

- TAPD 需求 `1162789787001016345`：host installer 将宿主 `/` 一次挂载到
  `/host`，部署脚本的宿主文件路径全部改用 `/host`，运行时配置仍写入宿主
  绝对路径；nested agent 和卸载 Job 同样挂载 `/host`，另保留必需的
  `/dev/fuse` 字符设备。删除原来的分散 hostPath 和 DBus 挂载。
- `bash -n`、host coexistence Bats 8 项、`make test`、host/nested Helm lint
  和模板渲染均通过。完整 Dockerfile 使用
  `docker.cnb.cool/i0358/docker-images-chrom/alpine:3.20` 构建静态辅助工具，
  发布 `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:v0.1.3`，digest
  `sha256:cb138e0586a711fe29537a79c8bfd8e854c3f1cdab2c6560a70008d9b3b3abbb`。
  `sysrunc-0.1.3.tgz` 发布到 ZPK 应用 `w7panel-sysrunc`，制品版本
  `1.0.3`，Chart 包 SHA256 为
  `6b5f5045e5a5152d7105484919dc2195d02c2f889208f0a4ae2b46470d66b46b`。
  镜像同时包含上一节已提交的 FS 虚拟文件快路径修改。
- 218 L0 `default/w7panel-sysrunc` revision 4、专用 CKM L1 同名 release
  revision 10 均为 deployed；内外层 installer/agent、admission 均 Ready，
  imageID 都匹配上述 CCR digest。实际 Pod 挂载：L0 installer 仅宿主 `/`
  到 `/host`，L1 agent 为 `/` 到 `/host` 加 `/dev/fuse`。L0 宿主安装标记
  为 `0.1.3-0`，宿主 sysrunc 与镜像二进制 SHA256 均为
  `26fe0cbbf8fccb37c35044e65feeadd5369af5d3bfd5f52fbcf7f58fb0b80ef8`；
  L1 release 中 sysrunc、FS 二进制 SHA256 与镜像一致。
- 升级时 K3s 重启导致两个 `restartPolicy: Never` 的 FS/nginx 测试 Pod
  变为 Unknown。按原清单仅重建这两个 Pod，未删除 PVC。两者状态脚本
  均通过，CRI sandbox 均为 `sysrunc/SANDBOX_READY`；FS 虚拟文件可读，
  FS/nginx/rootfs 三个 Pod 的 HTTP 均返回 200，webroot、`/srv/data` 和
  rootfs 持久标记保留。L0 两个 `disk-default` Longhorn PVC 仍为 Bound；
  本轮未重建对应测试 Pod。

## 2026-11-26 虚拟目录 getdents64 与 proc worker 源码集成

- 在专用 CKM `k3k-console-164315/ckm-sysrunc-fresh-1125` 的 L1
  `default/w7panel-sysrunc` release 测试本地镜像
  `sysrunc-it:218-20261126-virtualdirs-log`，镜像 ID 为
  `sha256:72897973d9f9d3aabf3bec4afd17f4f383c1e5b4cd5ef9be13075602a555a92a`。
  revision 12 `deployed`；运行中 FS SHA256
  `0be183d348585f20c872cfe2608b897346040e7b39f63996746a0312ee44712a`
  与本地构建一致。镜像只导入 L1 containerd，未推送 CCR 或发布 Chart。
- FS Pod 重建后，直接读取 24 个注册目录：21 个目录的 FS 日志各记录一次
  `getdents64 fast path served`，覆盖 `/proc/sys` 下的虚拟目录、
  `/sys/kernel` 及其子目录、`/sys/devices/virtual/dmi` 与 `id`、
  `/sys/module/nf_conntrack/parameters`。`/proc/sys/net/ipv4/vs` 在该
  内核中不存在；`/proc/pressure` 是原生 procfs 目录，无 FUSE 需要绕过；
  `/sys/devices/virtual` 下的 `powercap` 被 OCI mask，故保留 FUSE 回退。
  另建的无 mask 测试 Pod `sysrunc-dir-unmasked-1126` 因 Kubernetes 要求
  `hostUsers:false`，在内层创建 sandbox 时被 `fork/exec /proc/self/exe:
  operation not permitted` 阻止；没有到达目录读取，该 Pod 已删除。
  因此 `/sys/devices/virtual` 的无 mask 快路径本轮未验收。
  旧 random 目录的直接探针通过 224 字节遍历、32 字节分段、共享游标、
  rewind、相对 `../hostname` 的 FUSE 边界以及 UUID memfd。
- `/proc/pressure/{cpu,io,memory}` 和
  `/sys/devices/system/cpu/{online,present}` 的五个只读打开均返回
  `memfd:sysrunc-*`；pressure 内容非空，CPU 范围为 `0-7`。
  FS 与普通 L2 Pod 的 `check-status.sh` 均通过，CRI sandbox 均为
  `sysrunc/SANDBOX_READY`。nginx 和 rootfs HTTP 正常，
  `/root/rootfs-marker-1125` 保留；FS 日志无 panic、fatal、OOM、ENOMEM。
- 相同镜像与 Chart 重施到 revision 13 后，L0 CKM Server UID
  `1cae35d4-4f5a-4d3b-be20-4a3af4affe6e`、K3s 重启次数 5，
  FS、普通和 rootfs Pod UID 分别为
  `fffc712d-b902-4ea2-861c-064d9537da02`、
  `00b9fc35-5c82-44d2-b3f1-6ef36c957c1c`、
  `ff28c9e4-4e24-442c-9d56-e333f40f4116`，均未变化。
  镜像变更时两个 `restartPolicy: Never` Pod 变为 Unknown，已按原清单
  仅重建这两个 Pod；PVC 未删除。本轮未复测 L0 Longhorn `disk-default`。

## 2026-11-26 可写 sysctl 的只读快路径续测

- 测试镜像 `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:it-218-20261126-sysctl-read`
  已推送，OCI digest 为
  `sha256:299e819b981ef6db471a008326a4f78c0c728442d02d36cb4c4843f40e0a1161`。
  专用 CKM 的 L1 `default/w7panel-sysrunc` release revision 15 为
  `deployed`，inner-agent 运行镜像 ID 匹配该 digest。首次带 `--wait`
  升级时 K3s 重启使端口转发断开，revision 14 留在 `pending-upgrade`；
  对 revision 14 执行 Helm rollback 后 revision 15 恢复为 `deployed`。
- 两个 `restartPolicy: Never` 的 FS、nginx 测试 Pod 在 K3s 重启后为
  `Unknown`，从保存的清单仅重建这两个 Pod，随后均为 Ready。
  在 FS Pod 内读取十五个候选路径，其中十四个打开后的描述符为
  `memfd:sysrunc-*`；`/proc/sys/net/core/default_qdisc` 仍走 FUSE，
  且直接 `cat` 返回 `EINVAL`。源码已将它从新快路径清单移除；
  此镜像在该路径实际回退 FUSE，因此未把它计入十四条通过项。
- FS Pod 的 `/proc/sys` 挂载为只读，`O_RDWR` 打开
  `/proc/sys/kernel/panic` 返回 `EROFS`；本轮不能在该 Pod 验证写入后的
  容器数据一致性。普通 FUSE 请求与描述符仍由 manager 持有，
  FUSE worker 仅处理挂载自愈，这轮没有完成进程拆分。

## 2026-11-26 `default_qdisc` 与 netfilter 快路径续测

- 本地构建并推送测试镜像
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:it-218-20261126-sysctl-netfilter`，
  digest 为 `sha256:ed545df066ee54a59aad62f137b2e3c337d5d7d8fa3975e90119007a4a85d486`。
  专用 CKM 的 L1 `default/w7panel-sysrunc` revision 16 为 `deployed`；
  inner-agent 的实际 imageID 与该 digest 一致。
- L1 FS Pod 内重新检查二十条 `/proc/sys` 精确路径，二十个描述符均为
  `memfd:sysrunc-*`，无打开失败。`default_qdisc` 返回 `fq_codel`；
  五个 netfilter 文件分别返回非空数值。此前 218 内层 agent 上的宿主
  `default_qdisc` 文件不存在，原 FUSE 读取报 `EINVAL`；当前源码在宿主
  缺失时提供容器内默认值，并以单测验证容器内写入、非法值拒绝。
- 更新触发 L1 K3s 重启，L0 专用 CKM Server 恢复 2/2 Ready；FS 与 nginx
  两个 `restartPolicy: Never` 测试 Pod 从保存清单重建后均为 1/1 Running，
  其余 rootfs、特殊目录和普通 Pod 均恢复 Running。FUSE worker 的正常
  请求、状态和描述符交接尚未实现，本轮不构成完整 FS 进程拆分验收。
  随后源码增加的 proc worker 创建时间同步与恢复注册尚未打入此测试镜像。

## 2026-11-26 FUSE worker 生命周期与 UTS 虚拟值续测

- 本地测试镜像 `sysrunc-it:218-20261126-uts-memfd`（image ID
  `sha256:40c1bf339b1effcd4843a96390ac9d2b45dded5d518533c3d5b77221cbd51155`）
  导入专用 CKM 的 L1 containerd；L1 `default/w7panel-sysrunc` revision 19
  为 `deployed`。镜像未推送 CCR，也未发布 Chart。运行中 FS 主进程和
  proc、FUSE、ticker worker 均存在；FS 二进制本地 SHA256 为
  `612b45fdcbbf3c1af0fde5375f6c65aade26ebf6793c1d2400a95f4569c5011f`。
- revision 17 首次验证 FUSE worker 预注册重放：强制终止 FUSE worker 后，
  supervisor 约一秒内启动新进程；随后重建 FS Pod，Pod Ready，
  `/proc/sys/net/core/default_qdisc` 和 netfilter 等文件仍返回 memfd。
  预注册失败时仅撤销待注册状态，不删除已注册容器；对应源码测试通过。
- 发现旧 FUSE `/proc/sys/kernel/hostname` 返回 inner-agent 主机名，
  与 FS Pod 的 `hostname` 不一致。revision 18 将 hostname、domainname
  首次读取改为进入请求进程的命名空间；新建 FS Pod 的 hostname 文件
  与 `hostname` 命令一致，domainname 为 `(none)`。revision 19 再将这两条
  世界可读文件接入只读 memfd 快路径：描述符分别为
  `memfd:sysrunc-hostname` 和 `memfd:sysrunc-domainname`，内容仍正确。
  写打开继续走 FUSE；仅所有者可读的 sysfs 文件没有绕过权限检查。
- 镜像更新导致 K3s 重启，两个 `restartPolicy: Never` 测试 Pod 变为
  `Unknown`。按保存清单只重建这两个 Pod，PVC 未删除；FS、普通、rootfs、
  特殊目录测试 Pod，inner-agent 与 admission 均为 Running。普通 FUSE
  请求及内核连接仍由 manager 持有，worker 完整进程拆分尚未验收。

## 2026-11-26 FUSE 挂载点归属续测

- 本地测试镜像 `sysrunc-it:218-20261126-worker-strict`，image ID
  `sha256:9e527e68bb7d4f7ff88869197cc8421b8c5a6708f8ecef819b17dd99beaf0b84`，
  导入专用 CKM 的 L1 containerd；L1 `default/w7panel-sysrunc` revision 20
  为 `deployed`。镜像未推送 CCR，也未发布 Chart。
- 新注册容器的 FUSE 挂载点必须由 worker 创建；worker 返回错误时 manager
  不再悄悄本地创建。容器状态服务同时回滚失败预注册的 ID 和 netns 跟踪。
  `go test ./fs/...` 和完整构建通过。
- 镜像变更导致内层 K3s 重启；仅从保存清单重建两个变为 `Unknown` 的
  `restartPolicy: Never` 测试 Pod，PVC 未删。强制终止 FUSE worker 后，
  supervisor 启动新 worker；再次重建 FS Pod 仍为 Ready，hostname、
  `default_qdisc`、UUID 三条只读快路径返回正确的 memfd 内容。rootfs、
  特殊目录和普通 Pod 均 Running。正常 FUSE 请求与内核连接仍在 manager。

## 2026-11-26 每容器 FUSE 自愈任务续测

- TAPD `1162789787001016333` 指定 FUSE worker 处理“自愈”，并要求随容器
  生命周期创建、停止 goroutine。原自愈函数在 worker 全局命令循环中运行，
  每容器 FUSE goroutine 仅等待取消。本轮把修复请求分派给已注册容器的
  goroutine；未注册和已注销容器的请求直接报错，源码测试覆盖这三种状态。
- 本地镜像 `sysrunc-it:218-20261126-fuse-per-container`，image ID
  `sha256:6239515853d7d55091cec60fc6deb55730cd8c21562be3eca530286a66a23e12`，
  导入专用 CKM 的 L1 containerd；L1 `default/w7panel-sysrunc` revision 21
  为 `deployed`。运行中的 FS 二进制 SHA256
  `e7fe24f6a63b91c40d9a7c821919eeae4356d07978e2bef9aa7c9722037cca3b`
  与本地构建一致。未推送 CCR 或发布 Chart。
- 更新触发内层 K3s 重启，只重建两个 `restartPolicy: Never` 测试 Pod，
  PVC 未删。FS、普通、rootfs、特殊目录 Pod 及 inner-agent、admission
  均 Running；proc、FUSE、ticker worker 进程存在。本轮没有人为触发
  内核 FUSE 故障；修复命令分派由源码测试验证，218 仅验证运行集成。

## 2026-11-26 全局只读 proc 路径续测

- 本地镜像 `sysrunc-it:218-20261126-proc-global`，image ID
  `sha256:b348d9b042c33ff4c344809b44c94854e8ba4bbacb723e3fb4c44e03496b0999`，
  导入专用 CKM 的 L1 containerd；L1 `default/w7panel-sysrunc` revision 22
  为 `deployed`。运行中 FS 二进制 SHA256
  `55cce0a7ce8ed2dfe3094ce6a1bb50495cdb3aab2e6173856731f72906563c7e`
  与本地构建一致。未推送 CCR 或发布 Chart。
- 五个全局只读 `/proc/sys/kernel` 值由 proc worker 的容器 goroutine
  提供给直接读取路径：`cap_last_cap`、`ngroups_max`、`ostype`、
  `osrelease`、`version`。218 FS Pod 内五条描述符均为 memfd，读值
  分别符合当前内核；源码测试覆盖 worker 注册、读取和注销后的拒绝。
- 升级触发内层 K3s 重启，按保存清单仅重建两个 `restartPolicy: Never`
  测试 Pod，PVC 未删。FS、普通、rootfs、特殊目录 Pod 与 inner-agent、
  admission 均 Running。其余可写 proc sysctl 的处理器仍使用 manager
  的共享容器数据，尚未整体迁入 proc worker。
## 2026-11-26 Chart 安装器同名与 IPv4 快路径验证

- Chart 的 `host` 与 `nested` 模式均渲染 DaemonSet
  `w7panel-sysrunc-installer`、容器 `installer`，均使用
  `installer.image`；两个模式的 `helm lint` 通过。内层卸载 hook
  同时清理新名称与旧的 `w7panel-sysrunc-inner-agent`。
- 218 专用 CKM L1 `default/w7panel-sysrunc` 升至 revision 23，installer
  与 admission 均使用本地测试镜像
  `docker.io/library/sysrunc-it:218-20261126-ipv4-fastpath`
  (`sha256:a4bdf162df84940fe7ebec900d20942c1730973143bdc4c7973d104668853299`)。
  新 installer DaemonSet 1/1 Ready，旧 inner-agent DaemonSet 已删除，
  admission 1/1 Ready。
- K3s 重启后重建 FS 与普通测试 Pod，均 1/1 Running。FS Pod 读取
  `/proc/sys/net/ipv4/ping_group_range` 和
  `/proc/sys/net/ipv4/ip_unprivileged_port_start` 分别返回
  `/memfd:sysrunc-ping_group_range` 与
  `/memfd:sysrunc-ip_unprivileged_port_start`，内容分别为
  `65534\t65534` 与 `0`。
- 原 rootfs Pod 在 K3s 重启后持续报 `sysbox sidecar oci spec ambiguous`，
  按原始 Pod 清单重建同名 Pod 后 2/2 Running，根文件系统标记
  `/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`；PVC 保持 Bound。

## 2026-11-26 邻居阈值虚拟文件补全

- 旧镜像的 FS Pod 显示 `/proc/sys/net/ipv4/neigh/default/gc_thresh{1,2,3}`
  为虚拟文件，但读取均返回 `Invalid argument`；内层宿主内核已不提供对应
  sysctl。源码在无宿主文件时给三个文件分别初始化容器独立的
  `128`、`512`、`1024`，并让普通只读打开命中 seccomp memfd；写打开仍交
  FUSE 处理。源码测试覆盖初值、容器独立缓存和写后读。
- `go test ./fs/handler/implementations ./fs/seccomp` 与 `make build` 通过。
  本地完整镜像 `sysrunc-it:218-20261126-neigh-fastpath` 的 image ID 是
  `sha256:248c3073d2abe38117a3e800aa73d085dd5c0c0c011a3f5e0a099ba59d54225c`，
  已导入专用 CKM L1 containerd，未推送到远端仓库。
- 218 L1 Helm revision 24 的 installer 1/1 Ready、admission 1/1 Ready。
  K3s 重启后重建三个测试 Pod，FS、普通均 1/1 Running，rootfs 2/2
  Running，rootfs 标记保留且 PVC Bound。FS Pod 的三次只读打开分别得到
  `/memfd:sysrunc-gc_thresh{1,2,3}` 和上述数值。该 Pod 的 `/proc/sys`
  挂载为只读，写入在到达 FUSE 前被内核拒绝；L2 写路径本轮未验证。

## 2026-11-26 nf_conntrack hashsize 读取回退

- 218 FS Pod 的 `/sys/module/nf_conntrack/parameters/hashsize` 原先显示
  为 `0600` 虚拟文件，但读取返回 `Invalid argument`。内层 K3s 用户命名空间
  将宿主模块文件映射为 `65534:65534`，L1 FS 无权读取；可读的
  `/proc/sys/net/netfilter/nf_conntrack_buckets` 提供同一 bucket 数。
- handler 在模块文件不存在或无权限时，从 buckets 初始化容器独立缓存；
  保持 `0600` 文件权限，未将该路径加入 seccomp ADDFD 快路径。
  `go test ./fs/handler/implementations ./fs/seccomp` 和 `make build` 通过。
- 本地完整镜像 `sysrunc-it:218-20261126-hashsize-fallback` image ID
  `sha256:ede48fa002413783bef69e2085d094703e844e1ac45a814bbd12651ee0deb1f5`
  已导入专用 CKM L1、未推送。L1 Helm revision 25 的 installer 与
  admission 均 1/1 Ready。K3s 重启后 FS 和普通 Pod 重建为 1/1 Running，
  rootfs Pod 自动恢复为 2/2 Running，持久标记仍在。FS Pod 读取 hashsize
  与 nf_conntrack_buckets 均为 `262144`，三条邻居阈值仍返回 memfd 与
  `128`、`512`、`1024`。同一 FS Pod 中以 `nobody` 读取 hashsize 得到
  `Permission denied`，`0600` 权限仍生效。
- 随后修正缓存命中时仍先读取 buckets 的依赖：已有容器值时直接返回缓存。
  定向源码测试覆盖 fallback 文件消失后的读取。最终本地镜像
  `sysrunc-it:218-20261126-hashsize-cache` image ID
  `sha256:6bd09407ff7c7e76505fbc2038e816444c589ac7bdaceef1244e55a511046cb7`
  在同一专用 CKM 的 Helm revision 26 部署。K3s Server 收到安装器请求后
  正常退出并因回退等待短暂显示 `CrashLoopBackOff`，约 103 秒后恢复 2/2
  Running。installer、admission 均 1/1 Ready；重建的 FS、普通 Pod 均
  1/1 Running，rootfs Pod 自动恢复 2/2 Running、PVC Bound、持久标记保留。
  FS Pod 再次读得 hashsize 与 buckets 均为 `262144`，三个邻居阈值仍为
  memfd 与 `128`、`512`、`1024`。

## 2026-11-26 虚拟目录条目类型复验

- 在 revision 26 的 FS Pod 中，直接 `getdents64` 探针读取
  `/proc/sys/net/ipv4/neigh/default`，三个 `gc_thresh` 条目的 `d_type`
  均为 `4` (`DT_DIR`)，而 `stat` 将其显示为普通文件。原因是该 handler
  的 `ReadDirAll` 把所有虚拟条目固定标为目录，且后面的文件分支不可达。
- 修正为按 `EmuResource` 的 Kind、Mode、Size 构造目录条目，并补齐
  net/core 与 IPVS 虚拟目录的文件元数据。定向
  `go test ./fs/handler/implementations ./fs/seccomp` 与 `make build` 通过。
- 本地完整镜像 `sysrunc-it:218-20261126-dirent-types` image ID
  `sha256:0069802f424f28a7dd24f9a9cad59034083ef5d16e54f4d0381330e9fa643481`
  已导入专用 CKM L1、未推送。L1 Helm revision 27 的 installer、
  admission 均 1/1 Ready；K3s Server 2/2 Running。重建后的 FS、普通 Pod
  均 1/1 Running，rootfs Pod 自动恢复 2/2 Running；持久标记保留。
  同一个直接 syscall 探针再次读取该目录，三个文件的 `d_type` 均变为
  `8` (`DT_REG`)。三条阈值读值仍为 `128`、`512`、`1024`，hashsize 为
  `262144`。

## 2026-11-26 worker 并发与 Chart 单镜像核对

- `Supervisor.request` 改为按 worker 进程分别加锁，避免等待慢 FUSE
  回复时阻塞 proc 和 ticker；`go test ./fs/worker ./fs/cmd/sysrunc-fs`、
  `go test -race ./fs/worker` 和 `make build` 通过。镜像
  `sysrunc-it:218-20261126-worker-lock`（image ID
  `sha256:3a0f255a2aebb48ce3b80e6d4e339aab3eb3f6f394d45db2479962f86ab4f6a2`）
  已导入专用 CKM L1，未推送；Helm revision 28 已部署。
- 218 暂停 FUSE worker 后，新 FS Pod 首次预注册失败；supervisor 重启
  worker 后 Pod Ready。故障期间，已有 FS Pod 连续 10 次读取
  `/proc/meminfo`，耗时均为 4–7 ms。installer、admission 均 1/1 Ready，
  FS、普通 Pod 1/1 Running，rootfs Pod 2/2 Running，持久标记保留。
- Chart 的 L0/L1 installer 和 admission 统一读取 `installer.image`；
  admission 保留独立资源名。host/nested 的 `helm lint` 均通过。刻意传入
  旧 `admission.image.tag` 时，三个镜像引用仍一致。218 外层 installer、
  admission 目前均运行公开 CCR `v0.1.3`；专用 CKM 内层两者均运行上述
  本地测试镜像。内层 Chart 升至 revision 29，installer 与 admission
  均 1/1 Ready，FS 与普通 Pod 1/1 Running，rootfs Pod 2/2 Running。
  未发布新镜像或升级外层。

## 2026-11-26 内层 FS 主进程故障注入

- 专用 CKM L1 revision 29、测试镜像 `218-20261126-worker-lock` 下，
  原 FS 主进程 PID 6358。先确认 FS Pod 可读 `/proc/meminfo`，再向该
  进程发送 SIGKILL。安装器拉起新 FS 主进程 PID 107341，三个 worker
  也重新启动，但旧 FS Pod 读取 `/proc/sys/kernel/hostname` 和
  `/proc/meminfo` 均返回 `Transport endpoint is not connected`；日志还
  出现旧容器的 `unknown container` seccomp 通知。**此故障注入未通过**。
- 内层脚本启动 FS 时没有传 `--session-recovery`；该模式的 seccomp
  listener 保活当前依赖 systemd FD store。新进程的 socket Ready
  无法证明已有 Pod 的旧 FUSE 挂载已修复。此问题需要保留为 worker
  自愈验收缺口，不能把之前只暂停 FUSE worker 的成功用例当作证明。
- 为恢复测试环境，终止并重启专用 CKM 的 K3s Server；仅删除并按保存
  清单重建两个 `restartPolicy: Never` Pod，未删除 PVC。FS、普通 Pod
  恢复 1/1 Running，rootfs Pod 2/2 Running；FS Pod 再次读到虚拟
  hostname 和 meminfo，rootfs 标记仍是 `rootfs-1125-persist`。

## 2026-11-26 同一 worker 的跨容器请求并发

- manager 与 worker 的请求/回复增加 ID，并由 manager 单独读取回复再
  分发给等待者。worker 对有 ID 的读取和 FUSE 修复请求并发派发到各容器
  goroutine；因此一个容器的慢修复不再堵住同一 FUSE worker 中另一个
  容器的请求。源码测试使第一个修复保持阻塞，确认第二个先完成；
  `go test -race ./fs/worker`、相关 FS 包测试和 `make build` 通过。
- 本地完整镜像 `sysrunc-it:218-20261126-worker-mux` 的 image ID 为
  `sha256:e4052807f0bc0120594efb60e7b731a33c43b569bc9fb4a1a62a52e42d1e112f`，
  已导入专用 CKM L1、未推送。L1 Helm revision 30 deployed，installer
  和 admission 均 1/1 Ready，imageID 与本地一致。K3s 重启后仅重建
  两个 Never 策略测试 Pod，PVC 未删。FS、普通 Pod 均 1/1 Running，
  rootfs Pod 2/2 Running；FS Pod 的 meminfo、hostname 与邻居阈值
  读取正常，rootfs 持久标记保留。
- 218 此轮验证镜像部署和常规功能；跨容器的慢请求顺序由源码测试
  验证。FS 主进程异常退出后的旧挂载恢复仍未通过。
- 随后补上 worker 回复流已关闭时立即拒绝新请求，重新构建最终镜像
  `sysrunc-it:218-20261126-worker-mux-final`，image ID
  `sha256:bfae9450dba242e1c503c41d95e28c072e41dc9591c61449922b636d34377d51`。
  `go test -race ./fs/worker` 和 `make build` 再次通过。该镜像已导入
  专用 CKM L1，Helm revision 31 deployed；installer、admission 均
  1/1 Ready，运行 imageID 与本地一致。K3s 重启后重建两个 Never
  策略测试 Pod，FS、普通 Pod 恢复 1/1 Ready，rootfs Pod 2/2 Running。
  FS Pod 读得 meminfo、hostname、`gc_thresh1=128`；rootfs 标记保留。

## 2026-11-26 owner-only 虚拟文件只读快路径

- 四个原先只能走 FUSE 的 owner-only 文件加入普通只读
  `openat`/`openat2` memfd 快路径：`/proc/sys/fs/protected_{hardlinks,symlinks}`、
  `/sys/devices/virtual/dmi/id/product_uuid`、
  `/sys/module/nf_conntrack/parameters/hashsize`。注入前核对 tracee
  filesystem UID 与 FUSE 所呈现的容器 root 所有者；非所有者仍交由
  内核/FUSE 权限检查。注入的 FD 保留原 0600 或 0400 模式；`statx`
  同步设置虚拟 UID/GID。源码测试覆盖 fsuid 字段、路径模式和 memfd
  模式；`go test ./fs/seccomp ./fs/handler/implementations` 与
  `make build` 通过。
- 本地完整镜像 `sysrunc-it:218-20261126-owner-read` image ID
  `sha256:b81e3d6642cda4c3c6c542902a458f4638b7189224cd57b0493ea5a424cba151`
  已导入专用 CKM L1，未推送。Helm revision 32 deployed，installer 与
  admission 均 1/1 Ready，imageID 一致。K3s 重启后仅重建两个 Never
  策略测试 Pod，PVC 未删；FS、普通 Pod 均 1/1 Running，rootfs Pod
  2/2 Running，持久标记仍是 `rootfs-1125-persist`。
- FS Pod 中 root 打开四条路径的 FD 均为 `/memfd:sysrunc-*`；对应
  模式为 0600、0600、0400、0600，读值为 `1`、`1`、容器 UUID、
  `262144`。`nobody` 读取四条均被拒绝。FS Pod 的 `/proc/sys`
  是只读挂载，写打开被内核以 `EROFS` 拒绝，L2 写入路径本轮未验收。

## 2026-11-26 L0 systemd FUSE 修复 worker 故障注入

- 218 外层正式 `sysrunc-fs.service` 使用 `--session-recovery`，systemd
  FD store 上限 65536 且 Preserve=yes。创建一次性
  `default/sysrunc-l0-recovery-probe-1126`（RuntimeClass `sysrunc`，
  `sysrunc.io/fs=true`）后，其 `/proc/sys` 是 `sysboxfs` FUSE 挂载，
  hostname 和 UUID 可读；FD store 中有 3 个 listener。
- 执行 `systemctl restart sysrunc-fs.service` 后，主进程从 PID
  2274035 换为 3861908。Pod 保持相同 UID、Ready 且重启次数 0。
  立即读取时，旧 `/proc/sys` 挂载暂时返回 `Socket not connected`。
  之后日志分别记录两次 `FUSE mounts recovered; reopen stale descriptors`，
  故障处理计数为 attempts=3、successes=2、failures=0；该探针的
  `/proc/sys` 设备号从 `0:16632` 换成 `0:14904`，hostname 和 UUID
  恢复可读。修复从服务重启到日志成功约 45–60 秒，并非无中断切换；
  已打开的旧描述符仍需应用重新打开。
- 测试 Pod 已删除，FD store 回到 0；外层 FS 服务 Active，专用 CKM
  Server 2/2 Ready，内层 FS、普通 Pod 1/1 Running，rootfs Pod
  2/2 Running。此测试证明 L0 systemd 模式的 FUSE worker 实际执行了
  挂载修复；L1 非 systemd 模式的主进程 SIGKILL 恢复仍未实现。

## 2026-11-26 统一 installer 名称和镜像

- Chart 在 host 与 nested 模式均渲染 `w7panel-sysrunc-installer`
  DaemonSet、`installer` 容器，admission 保留独立名称。两者以及 nested
  卸载 Job 均只引用 `installer.image`；host/nested 模板和 lint 通过。
- 本地镜像 `sysrunc-it:218-20261126-product-uuid-worker`，image ID
  `sha256:20470ae06b35e732e931da78bd2e41b071a62b2657fe39abdeb2e6322e3a47c7`，
  已导入 218 专用 CKM L1，未推送。Helm revision 33 使用 `--reset-values`
  清除旧 `admission.image` 保存值。installer 和 admission 均 1/1 Ready，
  运行 imageID 与本地一致；L0 installer 仍使用公开 CCR 镜像。
- K3s 重启后仅重建两个 Never 策略测试 Pod，未删除 PVC。FS 和普通
  Pod 均 Ready，rootfs Pod 2/2 Running；虚拟 `product_uuid` 读值为
  `00000000-0000-0000-0000-ec0aa11033b4`，rootfs 持久标记仍为
  `rootfs-1125-persist`。本次读值验证没有单独证明生产路径必经 worker；
  源码 worker 测试覆盖注册、跨容器隔离与注销行为。
- 外层 `default/w7panel-sysrunc` 也升级到 Helm revision 5，清除了旧
  `admission.image` 保存值。DaemonSet 名称与容器名分别为
  `w7panel-sysrunc-installer`、`installer`；admission 独立命名。两者
  均使用同一公开 CCR 镜像 `w7panel-sysrunc:v0.1.3`。installer rollout
  成功，专用 CKM Server 保持 2/2 Ready，L1 installer/admission 均
  1/1 Running。L1 仍使用本地测试镜像，未进行正式镜像发布。

## 2026-11-26 `/proc/slabinfo` proc worker 路径

- 将 `/proc/slabinfo` 加入 exact-path 普通只读 `openat`/`openat2` 快路径，
  由已注册容器的 proc worker 读取；worker 失败时回退原 FUSE handler。
  `go test ./fs/seccomp ./fs/worker ./fs/handler/implementations` 和
  `make build` 通过。
- 本地镜像 `sysrunc-it:218-20261126-slabinfo-worker`，image ID
  `sha256:b8b84774066d73b144f04fc133eb7adbbdb49b5c23529e44363ef60fcb85c939`，
  已导入 218 专用 CKM L1，未推送。Helm revision 34 deployed，installer
  与 admission 均 1/1 Running，运行 imageID 与本地一致。K3s 重启后仅
  重建两个 Never 策略 Pod，未删除 PVC；FS 和普通 Pod 均 Ready，rootfs
  Pod 2/2 Running，标记仍是 `rootfs-1125-persist`。
- 该 CKM 的 nested user namespace 无权读取 `/proc/slabinfo` 后备文件。
  升级前后 `head /proc/slabinfo` 都返回 `Permission denied`；因此 218
  只能验证安全回退，不能证明成功读取时的 worker 命中或吞吐收益。
  宿主可读环境的 `TestProcWorkerSlabinfoRead` 已验证 worker 返回实际
  slabinfo 内容。同一 FS Pod 的 meminfo 与虚拟 product UUID 读取正常。

## 2026-11-26 IPVS 虚拟 sysctl 普通读取

- `/proc/sys/net/ipv4/vs/{conntrack,conn_reuse_mode,expire_nodest_conn,expire_quiescent_template}`
  加入只读 seccomp 快路径，仍读取原容器 handler 的值，写操作继续由
  FUSE 处理。只在宿主 IPVS 目录存在时启用，避免模块未加载时凭空
  暴露文件。源码测试核对四个路径的 handler 元数据与目录前提；
  `go test ./fs/seccomp ./fs/worker ./fs/handler/implementations` 和
  `make build` 通过。
- 本地镜像 `sysrunc-it:218-20261126-ipvs-read` image ID
  `sha256:864fb4ea458430b4fc9b5a5087e14afedccfa5dd876364516ecf6af2ef043bd9`
  已导入专用 CKM L1，未推送。Helm revision 35 deployed，installer 与
  admission 均 1/1 Running，运行 imageID 一致。K3s 重启后只重建两个
  Never 策略 Pod，PVC 未删。FS 与普通 Pod Ready，rootfs Pod 2/2
  Running 且持久标记仍是 `rootfs-1125-persist`；FS Pod 的 meminfo 与
  虚拟 product UUID 可读。
- 218 当前未加载 IPVS，L2 的 `/proc/sys/net/ipv4/vs` 在升级前后均
  不存在。这验证了缺失目录时不会误暴露虚拟文件；实际 IPVS 读取
  命中仍需在加载模块的环境验收。

## 2026-11-26 FUSE 只读资源交由 proc worker

- FUSE 的 cpuinfo、diskstats、swaps、meminfo、uptime、stat、slabinfo、
  pressure 与 CPU online/present 快照优先向已注册容器的 proc worker 读取；
  worker 不可用时回退原 handler。seccomp 快路径在 worker 失败后进入
  handler 时跳过再次请求，避免重复超时。loadavg 继续使用已有 ticker
  opt-in 逻辑。源码测试验证 worker 命中、错误回退和跳过重试；
  `go test -race ./fs/handler/implementations ./fs/seccomp ./fs/worker`
  与 `make build` 通过。
- 本地镜像 `sysrunc-it:218-20261126-fuse-proc-worker` image ID
  `sha256:a32639ea690e142ce47c832957eb939e2c45fa470333a2887c73aa6f8cf6ceda`
  已导入 218 专用 CKM L1，未推送。Helm revision 36 deployed；installer
  与 admission 均 1/1 Running，运行 imageID 与本地一致。K3s 重启后仅
  重建两个 Never 策略 Pod，PVC 未删。FS、普通 Pod 均 Ready，rootfs
  Pod 2/2 Running，标记仍是 `rootfs-1125-persist`。
- 使用静态探针以 `O_RDONLY|O_NONBLOCK|O_CLOEXEC` 打开 FS Pod 的
  `/proc/meminfo` 与 `/sys/devices/system/cpu/online`，避开普通只读
  seccomp memfd 快路径，实际读取均成功。两条路径是 FUSE 挂载，FS
  日志分别记录 `FUSE read for ... served by proc worker`，证明 FUSE
  回调实际使用了 proc worker。`/proc/pressure/cpu` 在该 Pod 是原生
  proc 文件，没有用于这项 FUSE 命中验证。
- 单 Pod 500 次和 1000 次 open/read/close 粗测中，meminfo 普通只读
  路径约 366/396 μs 每次，强制 FUSE 路径约 488/417 μs；CPU online
  普通只读路径约 489/464 μs，强制 FUSE 约 387/442 μs。结果随路径和
  运行次序波动，尚不能证明稳定吞吐收益；这项性能验收仍待更严格测量。

## 2026-11-27 CKM controller 调谐恢复与 FD 路径复核

- 218 原 `w7panel-ckm` controller 仍运行旧
  `docker.cnb.cool/i0358/ai-cvm:1.1.146`。它在 10:03 重建专用 CKM
  Server 时撤掉 `sysrunc.io/rootfs-rw-layer`，把 K3s snapshotter 从
  `native` 改为 `overlayfs`。新 Pod 因无法创建
  `/var/lib/rancher/k3s/server` 而 CrashLoop；直接回滚 Deployment 又
  被旧 controller 覆盖。
- `w7panel-ckm` 当前源码的 controller/resources 测试通过。使用项目
  `make build-use-ko` 将已提交源码构建并推送至
  `ccr.ccs.tencentyun.com/afan-public/w7panel-ckm:218-20261127-sysrunc-reconcile`，
  digest 为 `sha256:92b48bbe538bbd86d8e009dc7f8862196d90eb47cfb068cfa2f4991219767656`，
  CCR manifest 可读取。218 `default/w7panel-ckm` 只替换 manager 镜像，
  Pod 1/1 Ready、运行 imageID 与 digest 一致。原镜像为上述 ai-cvm
  tag，供回退参考。新 controller 恢复了专用 CKM Server 的 rootfs
  注解和 native snapshotter，Server 2/2 Ready。
- `/tmp` 在跨日后已清理。只删除两个 Unknown 的 Never 策略测试 Pod，
  用等效新清单 `/tmp/sysrunc-218-testpods.yaml` 重建；原 PVC 均保留。
  FS 与普通 Pod 1/1 Ready，rootfs Pod 2/2 Running，标记仍为
  `rootfs-1125-persist`。新普通 Pod 沿用两个原 PVC；新 FS Pod 用
  nginx 镜像运行睡眠命令并开启 `sysrunc.io/fs`。
- 新 FS Pod 中 `/proc/meminfo` 普通只读打开的 FD 指向
  `/memfd:sysrunc-meminfo`；`O_NONBLOCK` 打开的 FD 指向
  `/proc/meminfo`，两者均可读。FS 日志记录后者的 FUSE 回调由
  proc worker 提供数据。跨日的 CKM 重建使前一轮性能数字只能作为
  当时的粗测结果；稳定吞吐收益仍未验收。

## 2026-11-27 L0/L1 Chart 共用 installer 名称与镜像

- Chart 的 host、nested 模式均渲染 `w7panel-sysrunc-installer` DaemonSet，
  容器名为 `installer`；两层 installer、admission 和内层卸载 Job 均从
  `installer.image` 取镜像。`helm lint`、两种模式的 `helm template` 和
  `git diff --check` 通过。
- 使用 `make build-image IMAGE_TAG=218-20261127-unified-installer` 构建并
  直接推送 `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-unified-installer`；
  CCR manifest digest 为
  `sha256:a914eec08d632265613c3554ea9706efa4407c7bdee08177588dd0dd7304420f`。
- 218 外层 `default/w7panel-sysrunc` Helm revision 6、内层 revision 38
  均为 deployed；两层 installer DaemonSet 和 admission Deployment 都是
  1/1 Ready，镜像引用完全相同。外层更新运行时期间曾 cordon 节点并
  重建 CKM Server；三个已 Failed 的旧 Server Pod 带 K3K etcd finalizer，
  清除这些旧 Pod 的 finalizer 后安装器继续完成并 uncordon 节点。
  内层 Helm upgrade 在 API 转发断开后留下 pending revision 37，
  已按 revision 37 内容执行 rollback，生成 deployed revision 38。
- 专用 CKM Server 恢复 2/2 Ready；内层两个 `restartPolicy: Never` 测试
  Pod 因 Server 重建变为 Unknown，仅重建这两个 Pod，PVC 均保留。
  FS、普通 Pod 1/1 Ready，rootfs Pod 2/2 Ready，持久标记仍为
  `rootfs-1125-persist`。

## 2026-11-27 CKM snapshotter 同运行时自愈

- `w7panel-ckm` 的 `go test ./controllers ./pkg/resources` 和
  `git diff --check` 通过。按项目 `make build-use-ko` 推送测试镜像
  `ccr.ccs.tencentyun.com/afan-public/w7panel-ckm:218-20261127-sysrunc-snapshotter-reconcile`，
  digest `sha256:dccb00f3ea1e64ed28fc79a320be218222fa240b5b9a134a1cb8b5e62b2cca13`；
  218 `default/w7panel-ckm` manager 已运行该 digest。
- 手动将专用 CKM Server Deployment 的 `K3S_SNAPSHOTTER` 改成
  `overlayfs`。controller 在 runtimeClass 仍为 `sysrunc` 时自动恢复
  `native`，`sysrunc.io/rootfs-rw-layer` 注解保持；Deployment revision
  10 和新 Server Pod 2/2 Ready。
- K3s 重启使两个 Never 策略测试 Pod 变成 Unknown，仅重建这两个 Pod，
  PVC 保留。FS、普通 Pod 1/1 Running，rootfs Pod 2/2 Running，
  `/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`。

## 2026-11-27 L1 FS 崩溃恢复与 L0/L1 单镜像复验

- `make build-image` 推送了两版 CCR 测试镜像：
  `218-20261127-fs-crash-recovery` digest
  `sha256:5773c232ef27b9edfbcec1fc600fc4ea63dc4a69d4c4f82647feb3017ecb4528`；
  修复旧 checkpoint 边界的 `218-20261127-fs-crash-recovery2` digest
  `sha256:03dce51c9f19fd4f1efc62c79dca24b357cc797706c74547cfd980bdec48d3d2`。
- L1 无 systemd 的 installer 启动独立 FD broker 与 `--session-recovery`
  FS。对 FS 主进程发送 SIGKILL 后，broker 保留 seccomp listener，新 FS
  恢复两个旧 FUSE 挂载，日志记录 `attempts=2 successes=2 failures=0`。
  FS Pod UID 不变、容器重启数为 0，非阻塞读取 `/proc/meminfo` 成功，
  FUSE 日志确认由 proc worker 提供数据；rootfs 持久标记仍在。
- 第一版镜像在 CKM Server Pod 被 L0 更新重建后遇到旧 checkpoint：
  新 PID namespace 没有旧 broker listener，FS 拒绝启动。第二版在旧
  namespace 容器全部失效时丢弃 checkpoint；存在存活容器但缺 listener
  时仍拒绝恢复。L1 revision 40 使用第二版，Server 与测试 Pod 恢复。
- L0 revision 8 也升级到第二版。外层更新节点运行时并重建 CKM Server
  后，新 Server 2/2 Ready，节点 Ready；L0 与 L1 的 installer 和
  admission 四个容器的运行 `imageID` 均为上述同一 CCR digest。
  L1 Helm revision 40 为 deployed，内层 installer DaemonSet 1/1 Ready。
  两个因 Server 重建变为 Unknown 的 Never 策略测试 Pod 仅重建 Pod，
  PVC 均保留。FS 与普通 Pod 1/1 Ready，rootfs Pod 2/2 Ready；
  `/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`，FS Pod 的
  `/proc/meminfo` 可读。snapshotter 进程也在新 Server 中运行。

## 2026-11-27 FS checkpoint 的 fuse-overlayfs rename 修复

- 虚拟文件 backing read 现处理短读；短 proc/sys 写入返回错误，不再当作
  完整写入。handler 与 recovery 路径源码测试通过。
- 首版测试镜像 `218-20261127-virtual-io-fix` digest
  `sha256:336de70d0c00eab8f4a0a0f5caf7c333aea56f617c780fc35db53a47ac6ba6a6`
  在 L1 revision 41 暴露了 checkpoint 问题：CKM Server 的根文件系统为
  fuse-overlayfs，原 `/run/sysrunc/fs-restart.json` 是已有层的文件，
  `rename` 覆盖它返回 `ENOTDIR`，FS Pod 无法注册。将仅含空容器列表的
  旧 checkpoint 改名保留后，新 checkpoint 可以正常写入，FS Pod Ready。
- 修复版将新 checkpoint 写到容器 `/dev/shm/sysrunc/fs-restart.json`，
  首次启动仍可读取旧 `/run` 路径以迁移已有会话。`make build-image`
  推送 `218-20261127-checkpoint-tmpfs`，digest
  `sha256:4cf83d2dd2c89a3c5087a271739fef43c053955b70cb065132f73cdcf04b2482`。
  L1 Helm revision 42 deployed，installer 和 admission 均运行该 digest。
  K3s 重启后保留 PVC，仅重建两个 Unknown 的 Never 策略 Pod；FS、普通、
  rootfs Pod 均 Ready，rootfs 标记仍为 `rootfs-1125-persist`。
- 对新 FS 主进程发送 SIGKILL，重启后从 tmpfs checkpoint 恢复两个
  FUSE 挂载，日志记录 `attempts=2 successes=2 failures=0`。FS Pod UID
  `3bd0990d-4167-4a14-a4a7-2c12aafe689b` 不变、容器重启数为 0，
  非阻塞读 `/proc/meminfo` 成功。
- L0 Helm revision 9 也升级到相同镜像，节点 Ready 且可调度；外层更新
  重建专用 CKM Server 后，新 Server 2/2 Ready。L0 与 L1 installer、
  admission 四个容器的运行 imageID 均为上述同一 digest。L1 revision
  42 保持 deployed，新的 tmpfs checkpoint 已写入且随 FS Pod 注册增长；
  两个 Never 策略 Pod 仅重建 Pod，PVC 保留。FS 和普通 Pod 1/1 Ready，
  rootfs Pod 2/2 Ready，非阻塞读 `/proc/meminfo` 与持久标记复核通过。

## 2026-11-27 sysinfo 内存计算迁入 proc worker

- TAPD `1162789787001016333` 要求 seccomp 拦截保留在主进程，容器视角的
  proc 虚拟化和 syscall 业务交给按容器注册的 proc worker。将 `sysinfo`
  的 cgroup 内存计算加入 proc worker 请求；worker 不可用时保留 manager
  原计算作为兜底。worker 生命周期测试、seccomp 与 FS 命令测试通过。
- `make build-image` 推送
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-sysinfo-worker`，
  digest `sha256:30ee838b6be267e3e906c62c55df13184e34d855094bfdb2cd4b609a453fdb4c`。
  L1 Helm revision 43、L0 revision 10 均 deployed；两层 installer 和
  admission 四个运行容器的 imageID 均为该 digest。
- 在 L1 创建 256Mi 内存限额的临时 FS Pod，用名为 `free` 的静态探针调用
  `sysinfo`。返回 `total=268435456` 字节、`unit=1`，与 Pod 的
  `/sys/fs/cgroup/memory.max=268435456` 和 `/proc/meminfo` 的
  `MemTotal=262144 kB` 一致。已删除临时 Pod。
- L0 更新重建专用 CKM Server 后，Server 2/2 Ready，节点 Ready 且可调度；
  内层两个 Unknown 的 Never 策略 Pod 只重建 Pod、保留 PVC。FS 与普通
  Pod 1/1 Ready，rootfs Pod 2/2 Ready，rootfs 持久标记仍为
  `rootfs-1125-persist`，非阻塞读取 `/proc/meminfo` 成功。
- 同轮在 218 用 `ls` 触发 `/proc/sys/kernel/random` 和
  `/proc/sys/net/core` 的 `getdents64`，FS 日志确认两个目录均走 seccomp
  快路径；新增源码检查覆盖所有已启用的虚拟目录注册项。

## 2026-11-27 L0 回滚重装与统一镜像复核

- L0 cleanup Helm revision 13 的 installer DaemonSet Ready；宿主机二进制、
  安装标记及 K3s runtime 模板中的 Sysrunc 配置均清除，节点保持 Ready。
  重装到 revision 14 后，二进制、标记、配置和节点运行标签恢复。
- L0 revision 14 与 L1 revision 44 的 installer、admission 均使用
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-host-rollback2`，
  运行 imageID digest 均为
  `sha256:b8548134f402ca10ea65bb7281cef840c8f93029994eecf92681125fcccecae6`。
  两层 installer DaemonSet 同名 `w7panel-sysrunc-installer`；admission
  Deployment 均名为 `w7panel-sysrunc-admission`。
- L0 重装后，L1 两个 `restartPolicy: Never` 测试 Pod 变为 Unknown。
  仅重建 `sysrunc-fresh-fs-1125` 和 `sysrunc-fresh-nginx-1125` Pod，
  PVC 保留。两者及 `sysrunc-l2-rootfs-1125` 均 Ready；FS Pod 非阻塞读取
  `/proc/meminfo` 成功，rootfs 标记仍为 `rootfs-1125-persist`。

## 2026-11-27 相对虚拟文件路径快路径

- seccomp 路径解析现识别从原 FUSE 目录 fd 打开的 `./文件名`。
  `../` 和多级相对路径仍交给内核/FUSE 解析。源码 seccomp 测试通过。
- `make build-image IMAGE_TAG=218-20261127-relative-proc` 推送镜像，
  digest 为 `sha256:c953c44f801742b0120d4803a6a5c664ecead24b2a9dff9486d7c74b9eb895b6`。
  L1 Helm revision 45、L0 revision 15 均 deployed；四个 installer/admission
  运行容器的 imageID 都是该 digest。
- L1 FS Pod 从 `/proc` 打开 `./meminfo` 得到
  `/memfd:sysrunc-meminfo (deleted)`，从 `/proc/sys/kernel` 打开
  `./hostname` 得到 `/memfd:sysrunc-hostname (deleted)`；
  `./../proc/meminfo` 保持 `/proc/meminfo` FUSE fd。
- L0 镜像更新重建专用 CKM Server 后，新 Server 2/2 Ready。只重建两个
  `restartPolicy: Never` 的 FS 和普通测试 Pod，PVC 保留；FS、普通和 rootfs
  三个 L2 Pod 均 Ready，rootfs 标记仍为 `rootfs-1125-persist`。

## 2026-11-27 statx 虚拟元数据一致性

- L1 旧镜像中，`statx(/proc/sys/kernel/hostname)` 返回宿主机 inode、设备号和
  UID，与容器内 `stat()` 的 FUSE 元数据不同。内层宿主机没有
  `/proc/sys/net/core/default_qdisc`，而 FS Pod 的 FUSE 视图提供该文件。
  新实现从请求进程的 `/proc/<pid>/root` 视图获取元数据；无法获取时交给
  内核处理，避免伪造不完整的属性。
- 测试镜像 `218-20261127-statx-tracee` digest
  `sha256:d8f5be357817360f3844c34d18b7c0e76ac79378760d10b71daa0faa45aedd86`。
  L1 首次 Helm upgrade 因 K3s 重启中断停在 pending-upgrade revision 46；
  回滚到相同 manifest 后 revision 47 deployed。L0 revision 16 deployed。
  两层 installer 和 admission 四个运行容器的 imageID 都是该 digest。
- L1 静态 syscall 探针比较 `statx` 与 `stat`：`hostname`、宿主机缺失的
  `default_qdisc`、`meminfo`、`uuid` 四个路径的 mode、size、inode、
  UID/GID 和设备号均一致。L0 更新重建 CKM Server 后，FS、普通 nginx、
  rootfs 三个 L2 Pod 均 Ready；仅重建两个 Never 策略 Pod，PVC 保留，
  rootfs 持久标记仍为 `rootfs-1125-persist`。`./meminfo` 仍走 memfd。

## 2026-11-27 sysinfo 进程判断迁入 proc worker

- `sysinfo` 的进程名判断现在由注册容器的 proc worker 执行；manager 只在
  worker 不可用时执行旧判断和内存计算。源码 worker、seccomp、handler
  测试通过。
- 镜像 `218-20261127-sysinfo-worker-decision` digest
  `sha256:4551824947437c9812780a48ac2ac43789e58ada5e0f47b9d88cbdfaa8f2ba12`。
  L1 Helm revision 48、L0 revision 17 均 deployed；两层 installer、
  admission 四个运行容器的 imageID 均为该 digest。
- L1 临时 FS Pod 的 `memory.max=268435456`。同一静态 syscall 探针作为
  `/tmp/free` 返回 `total=268435456`、`unit=1`；复制为 `/tmp/not-free`
  后返回宿主机原始 `total=64980623360`。临时 Pod 已删除。
- L0 更新重建专用 CKM Server 后，新 Server 2/2 Ready。仅重建两个
  Never 策略测试 Pod，PVC 保留；FS、普通 nginx、rootfs 三个 L2 Pod
  均 Ready，rootfs 标记仍为 `rootfs-1125-persist`，`./meminfo` 为 memfd。

## 2026-11-27 缺失的虚拟 sysctl 迁入 proc worker

- `/proc/sys/net/core/default_qdisc` 和
  `/proc/sys/net/ipv4/neigh/default/gc_thresh{1,2,3}` 由每容器 proc worker
  保存；manager 保留 FUSE 故障兜底，Supervisor 在 worker 重启后回放写入值。
  同网络命名空间的容器共用 FUSE 状态，seccomp 快路径已对齐该共享状态。
- 218 L1 写入 `default_qdisc=fq`、`gc_thresh1=256` 后，FS Pod 普通读取与
  强制 FUSE 读取结果一致。定向终止 proc worker 后，新 worker 接管，
  两条读取路径仍返回写入值。测试后恢复为 `fq_codel`、`128`。
- L2 `/proc/sys` 为只读；临时 `hostUsers:false`、`procMount: Unmasked`
  Pod 因网络沙箱权限无法创建。写入测试改用 L1 Server 内该容器对应的
  FUSE 挂载点，临时 Pod 已删除。
- 修正并发写入到回放缓存与 worker 管道的顺序。最终镜像
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-virtual-sysctl-order`
  digest `sha256:8b58b9b776d6b481d80ee7aa3d8bea5b96a50602924cd79684d558174f9b6b8f`。
  L1 Helm revision 51、L0 revision 18 已部署；两层 installer、admission
  四个运行容器 imageID 均为该 digest。L0 更新重建专用 CKM Server 后，
  新 Server 2/2 Ready；仅重建两个 Never 策略测试 Pod，PVC 保留。
  FS、普通 nginx、rootfs 三个 L2 Pod 均 Ready，rootfs 文件
  `/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`，FS Pod 中
  `default_qdisc=fq_codel`、`gc_thresh1=128`。

## 2026-11-27 宿主机有初值的虚拟 sysctl 状态迁入 proc worker

- 扩展 worker 状态覆盖其余容器缓存的虚拟 proc/sys 和 nf_conntrack hashsize
  文件。首次读取从原 handler 取得完整宿主值并交给 proc worker；后续读取、
  成功写入和 worker 重启回放使用同一容器状态。写入校验和必要的宿主操作
  仍沿用原 handler。
- 镜像 `218-20261127-proc-sysctl-worker` 已推送到 CCR，digest
  `sha256:211b6da4bcf0ccad962a29ea99f024345e4c6967718c1404fc701c5ba41a6fbd`。
  L1 Helm revision 52 已部署。FS Pod 对应 FUSE 挂载点的
  `/proc/sys/vm/overcommit_memory` 从 `1` 写为 `2` 后，L2 容器读到 `2`，
  L1 宿主值仍为 `1`。定向终止 proc worker 后，新 worker 接管，FUSE 和
  L2 仍读到 `2`；测试后恢复为 `1`。L0 Helm revision 19 已部署，
  两层 installer、admission 四个运行容器的 imageID 均为该 digest。
  L0 更新重建专用 CKM Server 后，新 Server 2/2 Ready。仅重建两个
  Never 策略 Pod，PVC 保留；FS、普通 nginx、rootfs 三个 L2 Pod 均 Ready，
  rootfs 文件仍为 `rootfs-1125-persist`，FS Pod 中的 `overcommit_memory=1`。
  定向终止 L1 FUSE worker 后，Supervisor 启动新 worker；FS Pod 仍可读取
  `/proc/meminfo` 和 `overcommit_memory`。该 Pod 只配置 `sysrunc.io/fs=true`，
  未配置 ticker 注解。

## 2026-11-27 移除 Sysbox 名称及注解兼容

- 测试镜像 `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-sysrunc-only`
  digest `sha256:d0c8c226bee4dd57ed4efcdfa8990d299b35acc54beaa00a40ab193b298368f6`。
  L1 Helm revision 55、L0 revision 21 均 deployed；内外层 installer、
  admission 四个运行容器的 imageID 均为该 digest。
- L0 更新重建专用 CKM Server 后，Server 2/2 Ready。仅重建两个
  `restartPolicy: Never` 且处于 Unknown 的测试 Pod，PVC 保留；L2 FS、普通
  nginx、rootfs 三个 Pod 均 Ready。FS Pod 的 `/proc/sys` 挂载来源为
  `sysruncfs`，`/proc/sys/kernel/ostype`、`osrelease` 和 `/proc/meminfo`
  可读；普通 nginx 的 PVC webroot 有 `index.html`、`50x.html`；rootfs
  标记 `/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`。
- 临时 Pod 仅配置旧 `sysbox/rootfs-rw-layer` 和 `sysbox/volume-init`
  注解，admission 未注入 sidecar，Pod Ready；现有 `sysrunc.io/rootfs-rw-layer`
  Pod 有 `sysrunc-rootfs` sidecar。临时 Pod 已删除。

## 2026-11-27 移除 mgr 残留后的 218 发布测试

- 源码提交 `a0112a3` 构建的测试镜像已推送至 CCR：
  `ccr.ccs.tencentyun.com/afan-public/w7panel-sysrunc:218-20261127-a0112a3`，
  digest `sha256:8fe90f9d2abcf6f2dcd564e9021e296450fa9f269b9d5aa7a3524f69e1b9a2cb`。
  本地 Chart `sysrunc-0.1.3.tgz` SHA256 为
  `fce676c2482c2792f117b82b59a0ab8a7c1db4ce5f390978404b3fc433ec203b`；
  这轮部署使用仓库 Chart，未将同版本 Chart 重新发布到 ZPK。
- 专用 CKM `k3k-console-164315/ckm-sysrunc-fresh-1125` 的 L1 Helm 升至
  revision 56，L0 `default/w7panel-sysrunc` 升至 revision 22，均为
  `deployed`。两层 installer、admission 四个运行容器的 imageID 均为上述
  digest。L0 宿主和 L1 安装的 `sysrunc` SHA256 都是
  `0a6e6f659d0bf41ee61626216061e6cf84ee75c1a0976b5baabd380d4288b279`；
  FS 为 `951eab4f8d145ab39ad4e6925b84ba1a41cecae98232a02f8803721a4beb65ca`，
  snapshotter 为 `1dc628e39a468df576064a58b4e9e0b60e05d5a225c44eb25c40eebe0b047140`。
- L0 更新后 CKM Server `k3k-ckm-sysrunc-fresh-1125-server-7b6d8cdf4f-rjkd5`
  恢复为 2/2 Ready，`hostUsers:false`。两只 `restartPolicy: Never` 测试 Pod
  处于 `Unknown` 后仅重建这两只 Pod，保留所有 PVC。L2 普通 nginx、FS 和
  rootfs Pod 均 Ready；普通 nginx 的 CRI sandbox 使用 `sysrunc`，HTTP 200，
  webroot 和 `/srv/data` 标记保留且 `index.html`、`50x.html` 存在。rootfs
  Pod HTTP 200，`/root/rootfs-marker-1125` 仍为 `rootfs-1125-persist`。
  FS Pod 的 `/proc/sys` 来源为 `sysruncfs`，UUID、ostype、meminfo 可读。
  `sysrunc-special-v012-1125` 的 `/usr/share/nginx/html` 仍是 PVC 上的
  idmapped 挂载，`special-marker-v012` 保留。
- 临时 Pod `sysrunc-rootfs-false-a0112a3` 和
  `sysrunc-rootfs-empty-a0112a3` 分别使用 `false` 与空 rootfs 注解；
  两者均 Ready，只有 nginx 容器，没有 rootfs sidecar 或 PVC 挂载。
  它们作为命名测试资源保留，未删除 PVC。
- L1 用同一 Chart 和镜像重施至 revision 57 后，CKM Server UID
  `5803d086-5643-45e3-b637-86a2a615c82d`、K3s restartCount `1`、
  installer/admission Pod UID 和三个主要 L2 Pod UID 均未变化。
  内层只验证了 `local-path` PVC；CSI StorageClass 场景未运行。
