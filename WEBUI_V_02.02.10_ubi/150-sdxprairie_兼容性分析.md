# WEBUI_V_02.02.10_ubi 在 150-sdxprairie 上的兼容性分析

分析日期：2026-11-12
目标镜像：`160/150-sdxprairie-sysfs.ubi`（FM150 平台，构建 2020-08-04）
待上传组件：`fibocom广和通原厂WEB/WEBUI_V_02.02.10_ubi`（来源 `sdxlemur-sysfs.ubi`）

## 结论

**不能直接 adb 上传后正常运行。** 这不是路径或权限问题，而是**两代 Fibo 软件栈不匹配**：

1. 组件依赖的 `libfibo_utils.so.0` 在目标系统中**不存在**，`webs` 启动时动态链接即失败。
2. 组件通过 `libfibosocket` 连接 `/dev/socket/socket_server`（sdxlemur 新框架），目标 2020 年的 `fibo_app_platform` 使用 `/dev/socket/qmux_webs/server_webs`（旧框架），**socket 路径与协议代差**。
3. 目标 sysfs 内**没有任何 Web 静态资源、没有 `/webs`、没有 webs/lighttpd 服务**，属于“从零新增 WebUI”，不是替换升级。
4. 目标 SELinux 为 `enforcing`（MLS），新增二进制与 `/webs` 需要策略/上下文，否则可能被拒绝。

CPU 架构、glibc 版本、systemd 及部分底座库是兼容的，因此**理论上可做实验性移植**，但不建议直接用于量产。

## 1. 平台对比

| 项目 | 目标 150-sdxprairie | 组件 WEBUI_V_02.02.10 | 结论 |
|---|---|---|---|
| 平台 | `sdxprairie-mdm`，2020-08-04 | `sdxlemur`，2023+ | 不同代平台 |
| CPU/ABI | ARM 32 位 armhf，`/lib/ld-linux-armhf.so.3` | 同为 ARM 32 位 armhf | **兼容** |
| glibc | 2.28（`ld-2.28.so`） | 符号最高 `GLIBC_2.28`（`libgo.so`） | **兼容**（包元数据写 `>=2.31`，实测 2.28 足够，仍建议实机确认） |
| init | systemd + initscripts | systemd unit | **兼容** |
| 现有 Web 服务 | 无 `/webs`、无 webs/lighttpd、无 HTML（0 个） | `/webs/web` 84 个文件 + `webs` 服务器 | 目标缺失 |
| Web 后端 | `QCMAP_Web_CLIENT` + busybox `httpd`（未启用） | `webs` + `libgo.so`（`/fibo/webapi`、`/fibo/fibologin`） | **不兼容** |
| `libgo.so` / `libfibosocket` | 不存在 | 组件自带 | 需新增 |
| `libfibo_utils.so.0` | **不存在** | `libfibosocket` 的 NEEDED 依赖 | **阻断** |
| Fibo IPC socket | `/dev/socket/qmux_webs/server_webs` | `/dev/socket/socket_server` | **路径/协议不符** |
| `libdiag.so.1` | 存在（2020，151 KB） | `libfibosocket` 依赖 | 存在，但版本/符号需实机验证 |
| `liblog` / `libtime_genoff` / `libsystemdq` / `libxml2` / `libsqlite3` / glib 2.58 | 均存在 | 底座依赖 | **兼容** |
| `ipacm` / `netmgrd` / `init_sys_mss` 服务 | 均存在 | `webs.service` 的启动顺序依赖 | **兼容** |
| SELinux | `SELINUX=enforcing`，`SELINUXTYPE=mls` | 文件无目标策略标签 | 需策略适配 |
| 版本标识 | 无 `webui_version` | `WEBUI_V_02.02.10` | 目标原本无 WebUI |

## 2. 直接上传后的失败链

```text
webs
 └─ NEEDED libgo.so
      └─ NEEDED libfibosocket.so.0
           ├─ NEEDED libfibo_utils.so.0   ← 目标不存在（第一步即失败）
           ├─ NEEDED libdiag.so.1         ← 目标有，但为 2020 旧版
           └─ 连接 /dev/socket/socket_server
                 ↑ 目标旧框架提供的是 /dev/socket/qmux_webs/server_webs
```

预期现象：

- 启动 `webs` 时报动态链接错误：`libfibo_utils.so.0: cannot open shared object file`。
- 即使补上 `libfibo_utils`，`libfibosocket` 连接失败，字符串中的报错为：
  `client_connect: unable to open client socket, fd = %d`
- 即使 socket 能连上，旧 `fibo_app_platform` 的接口命令集与 sdxlemur 新框架不同，登录 `/fibo/fibologin`、数据接口 `/fibo/webapi` 大概率返回错误或超时。
- SELinux enforcing 下，还可能出现 `Permission denied` / `avc: denied`。

## 3. 已经兼容、无需担心的部分

- ARM 32 位 armhf 架构一致，无需换平台二进制。
- glibc 2.28 满足组件实际符号需求（最高 `GLIBC_2.28`）。
- systemd、`ipacm.service`、`netmgrd.service`、`init_sys_mss.service`、`persist-mount.service` 均存在，服务依赖名称能对上。
- `libdiag`、`liblog`、`libtime_genoff`、`libsystemdq`、`libxml2`、`libsqlite3`、glib 2.58 均已具备。
- 目标当前没有 Web 服务占用 80/443，端口可用。

## 4. 适配建议

### 推荐方案

1. **使用 FM150 / 150-sdxprairie 对应版本的 WebUI 包**（与目标 `QCMAP_Web_CLIENT`/旧 `fibo_app_platform` 配套），而不是 sdxlemur 的 `webs` 方案。
2. 若必须新增 Web 页面，优先基于目标现有 QCMAP Web 后端做前端适配，而不是直接搬 sdxlemur 的 `/fibo/webapi` 接口层。
3. 通过厂商获取该平台完整升级包/WebUI 组件，保证 `libfibo_utils`、socket 协议、SELinux 策略、认证模型全部配套。

### 实验性验证（不建议量产）

如果只是验证，可尝试：

1. 从同源 `sdxlemur-sysfs.ubi` 额外提取 `libfibo_utils.so.0.0.0` 并推到 `/usr/lib`，同时建立 `libfibo_utils.so.0` 链接。
2. 将 SELinux 临时设为 permissive（仅在实验环境），确认是否还有策略拦截。
3. 启动目标 `fibo_app_platform` 后检查 `/dev/socket/qmux_webs/server_webs`，再评估是否可用符号链接 `/dev/socket/socket_server` 指向旧 socket（协议是否兼容需实测）。
4. 观察 `webs` 日志与 `/fibo/fibologin` 返回，确认旧平台是否支持新接口命令。

即使上述都通过，旧平台缺少新接口所需的数据模型与命令，**功能完整性无法保证**。

### 不要做的事

- 不要为了这个组件替换目标 glibc（2.28 → 2.31+），会破坏整套系统。
- 不要长期关闭 SELinux。
- 不要覆盖或停用目标现有 `QCMAP_Web_CLIENT` / `fibo_app_platform`。
- 不要把 sdxlemur 的 `/webs` 与目标原有 Web 方案混用。

## 5. 实机确认命令

```bash
adb shell uname -m                 # 期望 armv7l
adb shell getenforce               # 当前为 Enforcing
adb shell ls -l /dev/socket        # 观察 socket_server 还是 qmux_webs
adb shell ldd /usr/bin/webs        # 检查 libfibo_utils / libfibosocket
adb shell journalctl -u webs -n 50 # 或 dmesg，查看启动失败原因
```

## 6. 证据来源

- 目标 UBI：`ubireader_display_info`、`ubireader_extract_files -k` 解包 rootfs；`readelf`/`strings` 检查 ELF 与依赖。
- 组件：`readelf -d` 得到 `webs → libgo.so → libfibosocket → libfibo_utils/libdiag`；`libfibosocket` 字符串含 `/dev/socket/socket_server`。
- 目标 `fibo_app_platform` 字符串含 `fibo_ipc_sock_server.c`、`/dev/socket/qmux_webs/server_webs`。
- 目标 `/etc/selinux/config` 为 `SELINUX=enforcing`；全盘未找到 `libfibo_utils*`、`socket_server` 服务、`/webs` 或 HTML 资源。
