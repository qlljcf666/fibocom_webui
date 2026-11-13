# WEBUI_V_02.02.10_ubi 在 160wau-sdxlemur 上的兼容性分析

分析日期：2026-11-12
目标镜像：`160/160wau-sdxlemur-sysfs.ubi`（FM160W-AU，sdxlemur，2023-02-28）
待推送组件：`fibocom广和通原厂WEB/WEBUI_V_02.02.10_ubi`（sdxlemur，构建 1691637850659 / 2023-08）

## 结论

**目标有 WEB，而且是一套完整可用的 WebUI。** 待推送组件在**平台层面兼容、可以运行**（ARM32、glibc、依赖库、socket、systemd、SELinux 全部对得上），但它不是 160WAU 的原配 WebUI，而是**同平台另一个产品构建**：

- 目标：125 个前端文件、96 个 JS、CSS 在 `/static/css`、双构建号、`route.txt` 无 `fibologin`、`libgo.so` 只导出 `websFiboapiOpen`。
- 组件：84 个前端文件、51 个 JS、CSS 在 `/css`、新增 `/fibo/fibologin` 路由、`libgo.so` 同时导出 `websFiboapiOpen` 与 `websFiboLoginOpen`。

因此：**整包替换可以跑起来，但会改变登录流程与页面集合，可能丢失 160WAU 定制功能；叠加推送会残留旧文件；只推前端不可用。**

## 1. 目标现状（160wau-sdxlemur）

| 项目 | 值 |
|---|---|
| 平台 | `sdxlemur`，QTI Linux，构建 2023-02-28 |
| glibc | 2.30 |
| SELinux | `SELINUX=disabled`（MLS 策略类型但未启用） |
| Web 服务 | 已有 `/webs`、`webs`、`libgo.so`、`webs.service`（已 enable） |
| 前端规模 | 125 个文件；96 个 JS；CSS 位于 `/webs/web/static/css` |
| 构建号 | `1677576217580`、`1677576251596`（双构建） |
| 菜单配置 | `permission.json` 38,053 B，含 FM160/FG101/FG650 等 24 组菜单 |
| 版本标识 | 无 `webui_version` 文件 |
| goahead 包 | `git-r3`，清单 151 条 |
| 服务端能力 | `route.txt` 无 `/fibo/fibologin`；`libgo.so` 仅导出 `websFiboapiOpen` |
| 依赖库 | `libfibo_utils.so.0.0.0` 128,820 B；`libdiag.so.1.0.0` 196,324 B；`libfibosocket` 25,848 B |
| Fibo 框架 | `fibo-app-framework`，socket 路径 `/dev/socket/socket_server` |

## 2. 组件现状（WEBUI_V_02.02.10_ubi）

| 项目 | 值 |
|---|---|
| 前端规模 | 84 个文件；51 个 JS；CSS 位于 `/webs/web/css` |
| 构建号 | `1691637850659` |
| 版本标识 | `WEBUI_V_02.02.10` |
| 菜单配置 | `permission.json` 43,814 B，25 组菜单（比目标多 `FG160netWorkMenu`） |
| goahead 包 | `git-r3`，清单 110 条 |
| 服务端能力 | `route.txt` 含 `/fibo/fibologin`；`libgo.so` 导出 `websFiboapiOpen` + `websFiboLoginOpen` |
| 二进制 | `webs` 9,604 B、`libgo.so` 134,336 B、`libfibosocket` 25,848 B（尺寸与目标相同，SHA256 不同） |
| 依赖 | `libfibosocket` → `libfibo_utils.so.0`、`libdiag.so.1`；目标均已有 |

## 3. 兼容性对照

| 项目 | 目标 160wau | 组件 02.02.10 | 判断 |
|---|---|---|---|
| CPU/ABI | ARM32 armhf | ARM32 armhf | **兼容** |
| glibc | 2.30 | 实际最高 `GLIBC_2.28`（包元数据写 `>=2.31`） | **可运行**；opkg 依赖检查会拒绝，adb 推送绕过 |
| SELinux | disabled | 文件无标签需求 | **兼容** |
| systemd / webs.service | 已有且相同 | 相同 unit | **兼容** |
| Fibo socket | `/dev/socket/socket_server` | 同路径 | **兼容** |
| `libfibo_utils` / `libdiag` | 已有 | `libfibosocket` 依赖 | **兼容**（保留目标现有库即可） |
| 80/443 端口 | `webs` 在用 | 同端口 | 替换后由新 `webs` 占用 |
| 前端/路由/登录 | 旧构建，`webapi` 登录 | 新构建，`fibologin` 登录 | **产品差异** |
| 160WAU 定制页面 | 125 文件/双构建 | 84 文件/单构建 | **可能缺失** |

## 4. 三种推送方式的结果

### 4.1 整目录替换（推荐用于实验）

停止 `webs`，将原 `/webs` 整体移走，再推入组件完整 `/webs` + `webs`/`libgo`/`libfibosocket`：

- 服务端、路由、前端三者属于同一构建，内部一致，**预期可正常启动、登录、加载页面**。
- 保留目标已有 `/usr/lib/libfibo_utils.so.0.0.0`、`libdiag.so.1.0.0`。
- 风险：页面集合由 125 变为 84，双构建/AU 定制功能可能缺失；菜单多出 `FG160netWorkMenu`，与 160WAU 不完全对应。

### 4.2 叠加推送（当前脚本的默认行为）

`adb_push_webui.ps1` 会备份后**在原目录上覆盖**，不会删除目标旧文件：

- 新 `index.html` 只引用 `/js`、`/css` 下的新文件，旧的 96 个 JS 与 `/static/css` 会残留但不再被引用。
- 功能上通常能启动，但目录混杂、占用空间，后续排障容易误判。
- **不建议**作为干净替换方案。

### 4.3 只推前端

不可用。目标 `libgo.so` 没有 `websFiboLoginOpen`，而组件前端会请求 `/fibo/fibologin`；只换 `index.html`/JS 会导致登录失败。必须连同 `webs`、`libgo.so`、`route.txt` 一起换。

## 5. 建议

1. 若目标现有 WebUI 正常，**保留原配**，不要为了换成 02.02.10 而覆盖。
2. 若必须使用 V_02.02.10：在实验模组上做**整目录替换**，先完整备份 `/webs`；验证登录、菜单、160WAU 特有页面后再考虑量产。
3. 量产优先向厂商获取 **160WAU 对应构建**的 WebUI 包，而不是通用 sdxlemur 包。
4. 不要用 opkg 安装组件包：其 `libc6 >= 2.31` 声明与目标 2.30 不符，会被依赖检查拒绝；adb 推送不受此限制，但需自行确认运行库。

## 6. 实验步骤（整包替换）

```bash
adb root; adb remount
adb shell "systemctl stop webs; cp -a /webs /data/webs.orig; rm -rf /webs"

# 推入组件完整目录与三个二进制
adb push rootfs/webs /webs
adb push rootfs/usr/bin/webs /usr/bin/webs
adb push rootfs/usr/bin/libgo.so /usr/bin/libgo.so
adb push rootfs/usr/lib/libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0.0.0
adb shell "ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0"

adb shell "chmod -R 0755 /webs; chmod 0755 /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0; chown -R root:root /webs /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0"
adb shell "systemctl daemon-reload; systemctl enable webs.service; systemctl restart webs.service"

# 验证
adb shell "cat /webs/web/webui_version; pidof webs; netstat -lntp | grep -E ':80|:443'"
```

回滚：`rm -rf /webs && cp -a /data/webs.orig /webs && systemctl restart webs`，并恢复原 `webs`/`libgo.so`（脚本第 2 步已备份到 `/data`）。

## 7. 证据来源

- 目标 UBI：`ubireader_extract_files -k` 解包；`/webs` 125 文件、96 JS、`static/css`、`permission.json` 38,053 B；`route.txt` 无 fibologin；`libgo.so` 仅 `websFiboapiOpen`；glibc 2.30；SELinux disabled。
- 组件：`/webs` 84 文件、51 JS、`css/`、`webui_version`；`route.txt` 含 fibologin；`libgo.so` 含 `websFiboLoginOpen`；`libfibosocket` NEEDED `libfibo_utils`/`libdiag`；GLIBC 最高 2.28。
- 目标 `fibo-app-framework` 与 `libfibosocket` 均使用 `/dev/socket/socket_server`，socket 层匹配。
