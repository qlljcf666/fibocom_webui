# WEBUI_160WAU_sdxlemur 组件解包与 adb 移植说明

适用平台：**FM160 / sdxlemur**（本组件取自 FM160W-AU 构建）
**不适用于 FM150AE / sdxprairie**（原因见第 5 节）
来源镜像：`160wau-sdxlemur-sysfs.ubi`
整理日期：2026-11-12

---

## 1. 概述

| 项目 | 内容 |
|---|---|
| 源镜像 | `160/160wau-sdxlemur-sysfs.ubi`（65,273,856 字节，约 62.2 MB） |
| 镜像格式 | UBI：PEB 256 KiB、LEB 248 KiB、249 个块（247 个数据块）；单卷 `rootfs`（247 块，autoresize） |
| 平台 | `sdxlemur`，QTI Linux，构建 2023-02-28（`/etc/version` = `6d93a6a73e`） |
| WebUI 版本标识 | **镜像内无 `webui_version` 文件**；前端构建号 **1677576217580 / 1677576251596** |
| 组件包 | `goahead git-r3`（清单 151 条）+ `libfibosocket0 git-r1`（2 条）+ `libfibo-utils0 git-r1`（2 条） |
| 架构 | `armv7vet2hf-neon`（ARM 32 位 armhf） |
| 提取结果 | **144 个文件 + 8 个目录 + 3 个符号链接**，文件合计 **3,859,610 字节** |
| 前端规模 | `/webs/web` 共 **125 个文件，3,290,143 字节** |
| 提取方式 | `ubireader_extract_files -k`（root 运行，保留原始权限/属主） |

> 这是 160WAU 的定制 WebUI：JS 为**现代浏览器 / 旧浏览器双构建**（`type=module` 的 `1677576251596` 与 `nomodule` 的 `1677576217580`，各 48 个，UI 内容相同），CSS 位于 `/webs/web/static/css`，`route.txt` **不含** `/fibo/fibologin`，`libgo.so` 仅导出 `websFiboapiOpen`。与通用 `WEBUI_V_02.02.10`（84 文件、单构建、含 fibologin）不是同一套，不可混用。

---

## 2. 交付目录

```text
WEBUI_160WAU_sdxlemur/
├─ rootfs/                          # 按设备原始绝对路径还原
│  ├─ webs/
│  │  ├─ web/                       # 前端站点（125 个文件，3,290,143 B）
│  │  │  ├─ index.html
│  │  │  ├─ favicon.ico
│  │  │  ├─ js/                     # 96 个文件（双构建，各 48 个）
│  │  │  ├─ json/permission.json
│  │  │  └─ static/
│  │  │     ├─ css/                 # 16 个文件
│  │  │     ├─ fonts/               # 5 个文件
│  │  │     └─ img/                 # 5 个文件
│  │  ├─ route.txt                  # 路由/鉴权配置
│  │  ├─ auth.txt                   # 认证数据（仅 schema 注释）
│  │  └─ *.crt / *.key              # TLS 证书与私钥（9 个）
│  ├─ usr/bin/
│  │  ├─ webs                       # 服务器主程序
│  │  └─ libgo.so                   # GoAhead 运行库
│  ├─ usr/lib/
│  │  ├─ libfibosocket.so.0.0.0     # Fibo Socket 客户端库
│  │  └─ libfibo_utils.so.0.0.0     # Fibo 工具库（libfibosocket 依赖）
│  ├─ etc/initscripts/
│  │  ├─ start-webs
│  │  ├─ webs-deamon
│  │  └─ webs-stop
│  └─ lib/systemd/system/
│     └─ webs.service
├─ package-meta/                    # opkg 元数据，仅参考，不推送
│  ├─ goahead.control / .list
│  ├─ libfibosocket0.control / .list / .postinst
│  └─ libfibo-utils0.control / .list / .postinst
├─ symlinks.txt                     # 3 个需在设备上重建的符号链接
├─ 文件清单.csv                      # 逐文件路径/权限/大小/SHA256/说明
├─ adb_push_webui.ps1               # 一键推送脚本
├─ 双击运行_上传WEBUI.bat            # 双击入口
└─ WEBUI_160WAU_sdxlemur_移植说明.md # 本文档
```

---

## 3. 文件清单与权限

### 3.1 前端站点 `/webs/web` 汇总

| 路径 | 文件数 | 大小(字节) | 权限 | 说明 |
|---|---:|---:|---|---|
| `/webs/web`（根级） | 2 | 5,160 | 0755 | `index.html` 874 B、`favicon.ico` 4,286 B |
| `/webs/web/js` | 96 | 2,642,643 | 0755（目录） | 现代浏览器构建 `1677576251596`（48 个，1,238,139 B）+ 旧浏览器 `nomodule` 构建 `1677576217580`（48 个，1,404,504 B） |
| `/webs/web/json` | 1 | 38,053 | 0755（目录） | `permission.json`，含 FM160/FG101/FG650 等 24 组菜单 |
| `/webs/web/static/css` | 16 | 266,018 | 0755（目录） | `chunk-vendors.ced7c85a.css`、`index.3bd8ea9e.css` 及 14 个页面分包 |
| `/webs/web/static/fonts` | 5 | 123,036 | 0755（目录） | iconfont / Element 图标字体 |
| `/webs/web/static/img` | 5 | 215,233 | 0755（目录） | 背景、侧边栏、登录插图、WLAN 图标 |
| **合计** | **125** | **3,290,143** | — | — |

> `index.html` 同时引用两套构建的 `chunk-vendors` 与 `index` JS，CSS 两套共用同一组 `static/css` 文件。

### 3.2 `/webs` 服务端配置与证书（11 个文件，全部 0755，root:root）

| 路径 | 大小(字节) | 说明 |
|---|---:|---|
| `/webs/route.txt` | 1,865 | 路由配置：`/fibo/webapi`（handler=fiboapi）、`/cgi-bin`、`/action`、`/`（jst）、OPTIONS/TRACE、`/goform`；**无 `/fibo/fibologin`** |
| `/webs/auth.txt` | 444 | 认证数据文件；仅 schema 注释，无实际用户 |
| `/webs/ca.crt` | 1,724 | CA 证书 |
| `/webs/ca.key` | 1,675 | CA 私钥（敏感） |
| `/webs/ec.crt` | 924 | EC 服务端证书 |
| `/webs/ec.key` | 302 | EC 私钥（敏感） |
| `/webs/roots.crt` | 256,338 | 根 CA 证书包 |
| `/webs/self.crt` | 1,444 | 自签服务端证书 |
| `/webs/self.key` | 1,679 | 自签服务端私钥（敏感） |
| `/webs/test.crt` | 1,444 | 测试证书 |
| `/webs/test.key` | 1,679 | 测试私钥（敏感） |

### 3.3 程序与依赖库

| 设备绝对路径 | 类型 | 权限 | 大小(字节) | 说明 |
|---|---|---|---:|---|
| `/usr/bin/webs` | 文件 | 0755 | 9,604 | GoAhead 服务器主程序；监听 `http://*:80`、`https://*:443`；`--home /webs` |
| `/usr/bin/libgo.so` | 文件 | 0755 | 134,336 | Fibocom 定制 GoAhead 运行库，导出 `websFiboapiOpen`；`webs` 经 RUNPATH `$ORIGIN` 加载 |
| `/usr/lib/libfibosocket.so.0.0.0` | 文件 | 0755 | 25,848 | Fibo Socket 客户端库，连接 `/dev/socket/socket_server` |
| `/usr/lib/libfibosocket.so.0` | 符号链接 | 0777 | — | → `libfibosocket.so.0.0.0` |
| `/usr/lib/libfibo_utils.so.0.0.0` | 文件 | 0755 | 128,820 | Fibo 工具库；`libfibosocket` 的 NEEDED 依赖 |
| `/usr/lib/libfibo_utils.so.0` | 符号链接 | 0777 | — | → `libfibo_utils.so.0.0.0` |

### 3.4 启停脚本与服务单元

| 设备绝对路径 | 类型 | 权限 | 大小(字节) | 说明 |
|---|---|---|---:|---|
| `/etc/initscripts/start-webs` | 文件 | 0755 | 786 | `start/stop/restart`：`start-stop-daemon -S -b -a /usr/bin/webs --home /webs` |
| `/etc/initscripts/webs-deamon` | 文件 | 0755 | 39 | systemd `ExecStart` 包装：前台执行 `/usr/bin/webs --home /webs` |
| `/etc/initscripts/webs-stop` | 文件 | 0755 | 137 | 停止脚本：按进程名 `webs-deamon` kill |
| `/lib/systemd/system/webs.service` | 文件 | 0644 | 379 | `Type=simple`、root、`Restart=always`、`Nice=-20`、`Before=persist-mount.service` |
| `/lib/systemd/system/multi-user.target.wants/webs.service` | 符号链接 | 0777 | — | → `/lib/systemd/system/webs.service` |

### 3.5 完整逐文件清单

[`文件清单.csv`](./文件清单.csv) 共 **155 行**（144 文件 + 8 目录 + 3 符号链接），列为：

`原始绝对路径 | 类型 | 权限(八进制) | 大小(字节) | SHA256 | 说明`

`js/` 与 `static/css/` 中带构建号/内容哈希的文件属于 Webpack 分包，**不可改名**。

---

## 4. 运行机制

1. **启动链**：systemd → `webs.service` → `/bin/sh /etc/initscripts/webs-deamon` → `/usr/bin/webs --home /webs`。
2. **端口**：程序内固定 `http://*:80` 与 `https://*:443`；HTTPS 使用 `/webs` 下的证书材料。
3. **配置**：`--home /webs` 使 `webs` 读取 `/webs/route.txt` 与 `/webs/auth.txt`；证书也在 `/webs` 下。
4. **后端接口**：本构建只注册 `/fibo/webapi`（`fiboapi` 处理器），由 `libgo.so` 导出；**没有 `/fibo/fibologin`**，登录/会话由 `webapi` 侧的既有逻辑处理。
5. **服务依赖**：`webs.service` 在 `init_sys_mss.service`、`ipacm.service`、`netmgrd.service` 之后启动，在 `persist-mount.service` 之前启动。
6. **运行身份**：root。
7. **端口冲突**：`webs` 固定占用 80/443；若目标运行 lighttpd，必须先停止/禁用。

---

## 5. FM150AE / FM160 兼容性

| 项目 | FM160（sdxlemur，本包同平台） | FM150AE（sdxprairie，2020 平台） |
|---|---|---|
| CPU/ABI | ARM32 armhf | ARM32 armhf |
| glibc | 通常 2.30 | 2.28 |
| 组件实际 GLIBC 需求 | `libgo` 最高 2.28；`libfibo_utils`/`libfibosocket` 2.15 | 符号层面 2.28 可满足 |
| Web 框架 | `fibo-app-framework` + `/dev/socket/socket_server` | 旧 `fibo_app_platform` + `/dev/socket/qmux_webs/server_webs` |
| `libfibo_utils` | 通常已有（本包也自带） | 原本没有（本包可补，但框架仍不匹配） |
| SELinux | 160WAU 为 disabled | 通常为 enforcing |
| `/webs` | 目标通常已有 | 原本没有 |
| 结论 | **同平台，可直接部署/替换** | **不可直接运行，需 sdxprairie 专用包** |

**FM150AE 不能使用本包的原因**：

- 本包的 `libfibosocket` 连接 `/dev/socket/socket_server`；FM150AE 的旧 `fibo_app_platform` 提供的是 `/dev/socket/qmux_webs/server_webs`，路径与协议均不匹配。
- FM150AE 的 `libgo` / `webs` 栈、`/fibo/webapi` 命令集与 2023 年的 sdxlemur 数据模型不同，登录与页面数据大概率失败。
- FM150AE 通常 SELinux enforcing，新增 `/webs` 与二进制需要额外策略。
- 即使补入本包的 `libfibo_utils`，socket/框架代差仍然存在。

**FM160 使用建议**：

- 若目标为 sdxlemur + `fibo-app-framework` 的固件（如 160WAU 同源版本），可整套替换；若目标已有 `webs`，先备份再替换。
- 若目标仍是 lighttpd + `WEBUI_V_02.02.28`，本包会用 `webs` 取代其 80/443 服务；需先停用 lighttpd。
- `permission.json` 主要覆盖 FM160/FG101/FG650 菜单；其他产品变体可能缺少专属页面，需实测。

---

## 6. adb 上传移植步骤

### 6.1 前置条件

- `adb devices` 可见设备；可 `adb root` / `remount` 写根分区。
- 目标为 **ARM 32 位 armhf**，且具备 `libdiag.so.1`、`libsqlite3.so.0`、glib、liblog、libtime_genoff、libsystemdq、libxml2（FM160 原厂固件通常已具备）。
- 备份现有 `/webs`、`/usr/bin/webs`、`/usr/bin/libgo.so`、`/usr/lib/libfibosocket*`、`/usr/lib/libfibo_utils*`。

### 6.2 一键脚本（推荐）

在本目录下执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\adb_push_webui.ps1
```

或直接**双击** `双击运行_上传WEBUI.bat`。

流程：获取写权限 → 备份到 `/data` → 建目录 → 逐文件推送 → 重建 3 个符号链接 → 恢复权限/属主 → 停止 lighttpd（如有）→ `daemon-reload`、`enable`、`restart webs.service` → 打印构建号/进程/端口。

### 6.3 手工命令

```bash
adb root
adb remount

# 备份
adb shell "cp -a /webs /data/webs.bak"
adb shell "cp -a /usr/bin/webs /data/webs.bin.bak; cp -a /usr/bin/libgo.so /data/libgo.so.bak"
adb shell "cp -a /usr/lib/libfibosocket.so.0.0.0 /data/libfibosocket.bak"
adb shell "cp -a /usr/lib/libfibo_utils.so.0.0.0 /data/libfibo_utils.bak"

# 建目录
adb shell "mkdir -p /webs/web/js /webs/web/json /webs/web/static/css /webs/web/static/fonts /webs/web/static/img /usr/bin /usr/lib /etc/initscripts /lib/systemd/system/multi-user.target.wants"

# 推送（推荐用脚本逐文件；目录推送在部分 adb 版本会多一层目录）
adb push rootfs/webs /webs
adb push rootfs/usr/bin/webs /usr/bin/webs
adb push rootfs/usr/bin/libgo.so /usr/bin/libgo.so
adb push rootfs/usr/lib/libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0.0.0
adb push rootfs/usr/lib/libfibo_utils.so.0.0.0 /usr/lib/libfibo_utils.so.0.0.0
adb push rootfs/etc/initscripts/start-webs /etc/initscripts/start-webs
adb push rootfs/etc/initscripts/webs-deamon /etc/initscripts/webs-deamon
adb push rootfs/etc/initscripts/webs-stop /etc/initscripts/webs-stop
adb push rootfs/lib/systemd/system/webs.service /lib/systemd/system/webs.service

# 符号链接（必须）
adb shell "ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0"
adb shell "ln -sf libfibo_utils.so.0.0.0 /usr/lib/libfibo_utils.so.0"
adb shell "ln -sf /lib/systemd/system/webs.service /lib/systemd/system/multi-user.target.wants/webs.service"

# 权限/属主
adb shell "chmod -R 0755 /webs"
adb shell "chmod 0755 /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0 /usr/lib/libfibo_utils.so.0.0.0 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop"
adb shell "chmod 0644 /lib/systemd/system/webs.service"
adb shell "chown -R root:root /webs /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0 /usr/lib/libfibo_utils.so.0.0.0 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop /lib/systemd/system/webs.service"

# 启动
adb shell "systemctl stop lighttpd 2>/dev/null; systemctl daemon-reload; systemctl enable webs.service; systemctl restart webs.service"
```

---

## 7. 权限一览

| 类别 | 路径 | 权限 | 属主 |
|---|---|---|---|
| 站点文件/目录 | `/webs/web/**` | 0755 | root:root |
| 服务端配置/证书 | `/webs/*.txt`、`/webs/*.crt`、`/webs/*.key` | 0755 | root:root |
| 主程序 | `/usr/bin/webs`、`/usr/bin/libgo.so` | 0755 | root:root |
| 依赖库 | `/usr/lib/libfibosocket.so.0.0.0`、`/usr/lib/libfibo_utils.so.0.0.0` | 0755 | root:root |
| 符号链接 | `/usr/lib/libfibosocket.so.0`、`/usr/lib/libfibo_utils.so.0`、`.../webs.service` | 0777 | root:root |
| 启停脚本 | `/etc/initscripts/start-webs`、`webs-deamon`、`webs-stop` | 0755 | root:root |
| 服务单元 | `/lib/systemd/system/webs.service` | 0644 | root:root |

---

## 8. 移植后验证

```bash
adb shell "grep -o 'index\.[0-9]*\.js' /webs/web/index.html | head -1"   # 期望 index.1677576251596.js
adb shell "pidof webs"
adb shell "netstat -lntp 2>/dev/null | grep -E ':80|:443'"
adb shell "ldd /usr/bin/webs"                                           # 检查 libgo/libfibosocket/libfibo_utils
```

浏览器访问 `http://<模组IP>/`：登录页、菜单、数据页面正常即说明 `webs`、`libgo`、`libfibosocket` 与底座 `fibo-app-framework` 链路可用。

本地完整性校验（PowerShell，在本目录执行）：

```powershell
$rows = Import-Csv .\文件清单.csv -Header path,type,mode,size,sha,desc | Select-Object -Skip 1
foreach ($r in $rows) {
    if ($r.sha -eq '-') { continue }
    $local = Join-Path (Join-Path $PWD 'rootfs') ($r.path.TrimStart('/'))
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $local).Hash.ToLower() -ne $r.sha) {
        Write-Host "MISMATCH $($r.path)"
    }
}
```

---

## 9. 回滚

```bash
adb shell "rm -rf /webs && cp -a /data/webs.bak.<时间戳> /webs"
adb shell "cp -a /data/webs.bin.<时间戳> /usr/bin/webs"
adb shell "cp -a /data/libgo.so.<时间戳> /usr/bin/libgo.so"
adb shell "cp -a /data/libfibosocket.so.0.0.0.<时间戳> /usr/lib/libfibosocket.so.0.0.0"
adb shell "cp -a /data/libfibo_utils.so.0.0.0.<时间戳> /usr/lib/libfibo_utils.so.0.0.0"
adb shell "ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0; ln -sf libfibo_utils.so.0.0.0 /usr/lib/libfibo_utils.so.0"
adb shell "systemctl restart webs.service"
adb shell "chmod -R 0755 /webs"
```

若回滚到 lighttpd 方案，还需 `systemctl enable --now lighttpd` 并停止 `webs.service`。

---

## 10. 注意事项

1. **平台限制**：仅面向 FM160 / sdxlemur；**不要部署到 FM150AE / sdxprairie**（旧框架 socket 与数据模型不匹配）。
2. **CPU 架构**：全部为 ARM 32 位 armhf（`/lib/ld-linux-armhf.so.3`）；目标 `uname -m` 应为 `armv7l`。
3. **依赖链**：`webs` → `libgo.so` → `libfibosocket` → `libfibo_utils` → `libdiag` / `libsqlite3`；`libdiag`、`libsqlite3`、glib、liblog、libtime_genoff 未随包提供，目标必须已有。
4. **包元数据与实际 GLIBC**：三个包 control 均写 `libc6 >= 2.31`，但实测算术符号最高 `GLIBC_2.28`（`libgo.so`），FM160 的 2.30 可运行；若用 opkg 安装会被依赖检查拒绝，adb 推送绕过。
5. **端口冲突**：`webs` 固定 80/443；目标若运行 lighttpd，先停止/禁用。
6. **符号链接不可丢**：`libfibosocket.so.0`、`libfibo_utils.so.0`、`multi-user.target.wants/webs.service` 必须重建。
7. **无版本文件**：本镜像没有 `webui_version`；不要伪造，识别以前端构建号与 `index.html` 为准。
8. **不要与 02.02.10 混用**：两套前端构建号、文件数、路由（fibologin）均不同；混拷会出现登录/分包不匹配。
9. **证书安全**：`/webs/*.key` 为明文私钥，原权限 0755（所有用户可读）；生产环境建议收紧为 0600 并替换自签/测试证书。
10. **`auth.txt` 无用户**：不要误改；本构建未注册 `fibologin`，认证由 `/fibo/webapi` 侧逻辑处理。
11. **SELinux**：160WAU 为 disabled；若目标为 enforcing，需为 `/webs`、`webs`、`libgo.so`、两个 so 增加上下文/策略。
12. **只读根分区**：`/webs`、`/usr/bin` 若为只读，需 remount 或使用厂商可写 overlay；运行时必须保持上述绝对路径。
13. **双构建前端**：`index.html` 同时引用两套 JS，`js/` 与 `static/css/` 的哈希文件名不可改名；不要只替换其中一套。
14. **可选配置**：`/etc/webui_url`（本镜像为 `my.usb`）不属于 goahead 包，本组件不包含、不覆盖。

---

## 11. 解包过程记录（可复现）

```bash
# UBI 结构
ubireader_display_info 160wau-sdxlemur-sysfs.ubi
#   PEB 262144 / LEB 253952 / 249 块 / 单卷 rootfs（247 块，autoresize）

# 保留权限解包（root）
ubireader_extract_files -k -o /tmp/t160x 160wau-sdxlemur-sysfs.ubi

# 组件定位
cat  /tmp/t160x/*/rootfs/var/lib/opkg/info/goahead.list        # 151 条
cat  /tmp/t160x/*/rootfs/var/lib/opkg/info/libfibosocket0.list # 2 条
cat  /tmp/t160x/*/rootfs/var/lib/opkg/info/libfibo-utils0.list # 2 条
```

核对结果：`goahead.list` 151 条、`libfibosocket0.list` 2 条、`libfibo-utils0.list` 2 条全部存在；`/webs/web` 125 个文件，与清单一致，无缺失。
