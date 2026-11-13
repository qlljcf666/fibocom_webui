# WEBUI_V_02.02.10 组件解包与 adb 移植说明

适用模组：Fibocom **FM150AE / FM160**
组件版本：**WEBUI_V_02.02.10**
来源镜像：`sdxlemur-sysfs.ubi`
整理日期：2026-11-12

---

## 1. 概述

| 项目 | 内容 |
|---|---|
| 源镜像 | `160/sdxlemur-sysfs.ubi`（79,429,632 字节，约 75.7 MB） |
| 镜像格式 | UBI 镜像：PEB 256 KiB、LEB 248 KiB、共 303 个 PEB；单卷 `rootfs`（UBIFS，301 个数据块） |
| 平台标识 | `sdxlemur`（Fibocom 5G 模组 Linux 系统） |
| WEBUI 版本 | `/webs/web/webui_version` → **WEBUI_V_02.02.10** |
| 组件归属 | opkg 包 `goahead`（`git-r3`，armv7vet2hf-neon），清单 `/var/lib/opkg/info/goahead.list` 共 110 条 |
| 服务端 | `/usr/bin/webs`（GoAhead 服务器，ARM 32 位）+ `/usr/bin/libgo.so`（Fibocom 定制 GoAhead 运行库） |
| 附加依赖组件 | opkg 包 `libfibosocket0`（`git-r1`），提供 `libfibosocket.so.0` |
| 提取结果 | **102 个文件 + 8 个目录 + 2 个符号链接**，文件合计 **2,680,363 字节** |
| 提取方式 | `ubireader_extract_files -k`（root 运行，保留原始权限/属主） |

与之前 `system.img` 中的 **WEBUI_V_02.02.28 + lighttpd** 不同，本 UBI 是 **WEBUI_V_02.02.10 + webs(GoAhead)**，前后端配套，不能混用两套文件。

---

## 2. 交付目录

```text
WEBUI_V_02.02.10_ubi/
├─ rootfs/                          # 按设备原始绝对路径还原
│  ├─ webs/
│  │  ├─ web/                       # 前端站点（84 个文件，2,239,672 B）
│  │  │  ├─ index.html
│  │  │  ├─ favicon.ico
│  │  │  ├─ webui_version
│  │  │  ├─ css/                    # 19 个文件
│  │  │  ├─ js/                     # 51 个文件
│  │  │  ├─ json/permission.json
│  │  │  └─ static/{fonts,img}/     # 10 个文件
│  │  ├─ route.txt                  # 路由/鉴权配置
│  │  ├─ auth.txt                   # 认证数据（仅 schema 注释）
│  │  └─ *.crt / *.key              # TLS 证书与私钥（9 个）
│  ├─ usr/bin/
│  │  ├─ webs                       # 服务器主程序
│  │  └─ libgo.so                   # GoAhead 运行库（/fibo 接口处理器）
│  ├─ usr/lib/
│  │  └─ libfibosocket.so.0.0.0     # Fibo Socket 客户端库
│  ├─ etc/initscripts/
│  │  ├─ start-webs                 # 启停脚本
│  │  ├─ webs-deamon                # systemd 前台启动包装
│  │  └─ webs-stop                  # 停止脚本
│  └─ lib/systemd/system/
│     └─ webs.service               # systemd 服务单元
├─ package-meta/                    # opkg 元数据，仅参考，不推送
│  ├─ goahead.control / goahead.list
│  └─ libfibosocket0.control / .list / .postinst
├─ symlinks.txt                     # 需在设备上重建的 2 个符号链接
├─ 文件清单.csv                      # 逐文件路径/权限/大小/SHA256/说明
├─ adb_push_webui.ps1               # 一键推送脚本
└─ WEBUI_V_02.02.10_移植说明.md      # 本文档
```

---

## 3. 文件清单与权限

### 3.1 前端站点 `/webs/web` 汇总

| 路径 | 文件数 | 大小(字节) | 权限 | 说明 |
|---|---:|---:|---|---|
| `/webs/web`（根级） | 3 | 4,990 | 0755 | `index.html`、`favicon.ico`、`webui_version` |
| `/webs/web/css` | 19 | 276,320 | 0755（目录） | 主样式、第三方样式、页面分包样式 |
| `/webs/web/js` | 51 | 1,579,601 | 0755（目录） | `index` 主逻辑、`chunk-vendors`、49 个异步分包 |
| `/webs/web/json` | 1 | 43,814 | 0755（目录） | `permission.json` 菜单/权限定义 |
| `/webs/web/static/fonts` | 5 | 123,036 | 0755（目录） | iconfont / Element 图标字体 |
| `/webs/web/static/img` | 5 | 211,911 | 0755（目录） | 背景、侧边栏、登录插图、WLAN 状态图标 |
| **合计** | **84** | **2,239,672** | — | — |

> 本固件把 `/webs/web` 下所有普通文件统一打成 **0755**、目录 **0755**，属主 `root:root`；与设备原状一致即可。

### 3.2 `/webs` 服务端配置与证书（11 个文件，全部 0755，root:root）

| 路径 | 大小(字节) | 说明 |
|---|---:|---|
| `/webs/route.txt` | 1,909 | 路由与鉴权配置。定义 `/fibo/webapi`（handler=fiboapi）、`/fibo/fibologin`（handler=fibologin）、`/cgi-bin`、`/action`、`/`、`/goform` 等 |
| `/webs/auth.txt` | 444 | 认证数据文件；当前**只有 schema 注释、没有实际用户**，登录由 `fibologin` 处理器完成 |
| `/webs/ca.crt` | 1,724 | CA 证书 |
| `/webs/ca.key` | 1,675 | CA 私钥（敏感） |
| `/webs/ec.crt` | 924 | EC 服务端证书 |
| `/webs/ec.key` | 302 | EC 私钥（敏感） |
| `/webs/roots.crt` | 256,338 | 根 CA 证书包 |
| `/webs/self.crt` | 1,444 | 自签服务端证书 |
| `/webs/self.key` | 1,679 | 自签服务端私钥（敏感） |
| `/webs/test.crt` | 1,444 | 测试证书 |
| `/webs/test.key` | 1,679 | 测试私钥（敏感） |

### 3.3 程序、脚本与服务单元

| 设备绝对路径 | 类型 | 权限 | 大小(字节) | 说明 |
|---|---|---|---:|---|
| `/usr/bin/webs` | 文件 | 0755 | 9,604 | GoAhead Web 服务器主程序，ARM 32 位 ELF；监听 `http://*:80`、`https://*:443`；运行时以 `--home /webs` 读取配置 |
| `/usr/bin/libgo.so` | 文件 | 0755 | 134,336 | Fibocom 定制 GoAhead 运行库；导出 `websFiboapiOpen`、`websFiboLoginOpen`，即 `/fibo/webapi`、`/fibo/fibologin` 的业务后端。`webs` 通过 RUNPATH `$ORIGIN` 从同目录加载 |
| `/usr/lib/libfibosocket.so.0.0.0` | 文件 | 0755 | 25,848 | Fibo Socket 客户端库，`webs` 经它与底座服务通信 |
| `/usr/lib/libfibosocket.so.0` | 符号链接 | 0777 | — | → `libfibosocket.so.0.0.0` |
| `/etc/initscripts/start-webs` | 文件 | 0755 | 786 | `start/stop/restart`：`start-stop-daemon -S -b -a /usr/bin/webs --home /webs` |
| `/etc/initscripts/webs-deamon` | 文件 | 0755 | 39 | systemd 的 `ExecStart` 包装，前台执行 `/usr/bin/webs --home /webs` |
| `/etc/initscripts/webs-stop` | 文件 | 0755 | 137 | 停止脚本：按进程名 `webs-deamon` 执行 kill |
| `/lib/systemd/system/webs.service` | 文件 | 0644 | 379 | 服务单元：`Type=simple`、`User/Group=root`、`Restart=always`、`Nice=-20`、`Before=persist-mount.service` |
| `/lib/systemd/system/multi-user.target.wants/webs.service` | 符号链接 | 0777 | — | → `/lib/systemd/system/webs.service`，开机自启 |

> 目录 `/webs`、`/webs/web` 及其子目录权限均为 **0755**。若目标已有 `/etc/webui_url`（内容 `fibo.web.com`，被 `/usr/bin/fibo-app-framework` 引用），本组件不包含、也不覆盖它。

### 3.4 完整逐文件清单

[`文件清单.csv`](./文件清单.csv) 共 112 行（102 文件 + 8 目录 + 2 符号链接），列为：

`原始绝对路径 | 类型 | 权限(八进制) | 大小(字节) | SHA256 | 说明`

其中 49 个 `js/<数字>.1691637850659.js`、17 个 `css/chunk.<数字>.1691637850659.css` 及带哈希的图片是 Webpack **异步分包**，`1691637850659` 为构建号，**不可改名**。

---

## 4. 运行机制

1. **启动链**：systemd → `webs.service` → `ExecStart=/bin/sh /etc/initscripts/webs-deamon` → `/usr/bin/webs --home /webs`。
2. **监听端口**：程序内固定 `http://*:80` 与 `https://*:443`；HTTPS 使用 `/webs` 下的证书材料。
3. **配置来源**：`--home /webs` 使 `webs` 读取 `/webs/route.txt`（路由）与 `/webs/auth.txt`（认证）；也可用 `--auth <file>`、`--route <file>` 覆盖。
4. **后端接口**：`route.txt` 将 `/fibo/webapi` 交给 `fiboapi` 处理器、`/fibo/fibologin` 交给 `fibologin` 处理器；这两个处理器由 `libgo.so` 导出。前端主 JS 确实请求 `/fibo/fibologin/` 与 `/fibo/webapi`。
5. **服务依赖**：`webs.service` 在 `init_sys_mss.service`、`ipacm.service`、`netmgrd.service` 之后启动，并在 `persist-mount.service` 之前启动；目标必须存在这些底座服务。
6. **运行身份**：root；对 `/webs` 及端口有完全访问权。
7. **认证**：`auth.txt` 未配置任何用户，因此不走 basic/digest；登录由 `fibologin` 接口处理，成功后前端保存会话。
8. **与 lighttpd 冲突**：本服务的 `webs` 与之前 02.02.28 版本的 `lighttpd` 都占用 80/443。若目标当前运行 lighttpd，必须先停止/禁用，否则 `webs` 无法绑定端口。

---

## 5. adb 上传移植步骤

### 5.1 前置条件

- `adb devices` 可见设备且已授权；可 `adb root` 或 `remount` 写根分区。
- 目标为 **ARM 32 位（armv7l）**，且具备依赖库（见第 9 节）。
- 记录/备份目标现有 `/webs`、`/usr/bin/webs`、`/usr/bin/libgo.so`、`/usr/lib/libfibosocket*`。

### 5.2 一键脚本（推荐）

在本目录下执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\adb_push_webui.ps1
```

也可直接**双击** `双击运行_上传WEBUI.bat`，效果相同；窗口会在结束后暂停并显示退出码。

流程：获取写权限 → 备份到 `/data` → 建目录 → 逐文件推送 → **重建 2 个符号链接** → 恢复 0755/0644 与 `root:root` → 停止 lighttpd（如有）→ `daemon-reload`、`enable`、`restart webs.service` → 打印版本/进程/端口。

### 5.3 手工命令

```bash
adb root
adb remount
adb shell "mount -o remount,rw /"

# 备份
adb shell "cp -a /webs /data/webs.bak"
adb shell "cp -a /usr/bin/webs /data/webs.bin.bak; cp -a /usr/bin/libgo.so /data/libgo.so.bak"
adb shell "cp -a /usr/lib/libfibosocket.so.0.0.0 /data/libfibosocket.so.0.0.0.bak"

# 建目录
adb shell "mkdir -p /webs/web/css /webs/web/js /webs/web/json /webs/web/static/fonts /webs/web/static/img /usr/bin /usr/lib /etc/initscripts /lib/systemd/system/multi-user.target.wants"

# 推送（推荐逐文件；也可用脚本）
adb push rootfs/webs/web/. /webs/web/
adb push rootfs/webs/auth.txt /webs/auth.txt          # 其余 /webs 文件同理
adb push rootfs/usr/bin/webs /usr/bin/webs
adb push rootfs/usr/bin/libgo.so /usr/bin/libgo.so
adb push rootfs/usr/lib/libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0.0.0
adb push rootfs/etc/initscripts/start-webs /etc/initscripts/start-webs
adb push rootfs/etc/initscripts/webs-deamon /etc/initscripts/webs-deamon
adb push rootfs/etc/initscripts/webs-stop /etc/initscripts/webs-stop
adb push rootfs/lib/systemd/system/webs.service /lib/systemd/system/webs.service

# 符号链接（必须）
adb shell "ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0"
adb shell "ln -sf /lib/systemd/system/webs.service /lib/systemd/system/multi-user.target.wants/webs.service"

# 权限/属主
adb shell "chmod -R 0755 /webs"
adb shell "chmod 0755 /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop"
adb shell "chmod 0644 /lib/systemd/system/webs.service"
adb shell "chown -R root:root /webs /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket.so.0.0.0 /etc/initscripts/start-webs /etc/initscripts/webs-deamon /etc/initscripts/webs-stop /lib/systemd/system/webs.service"

# 停止冲突服务并启动
adb shell "systemctl stop lighttpd 2>/dev/null; systemctl daemon-reload; systemctl enable webs.service; systemctl restart webs.service"
```

> `adb push 目录` 在不同 adb 版本上行为不一致，建议使用脚本的**逐文件推送**，避免多出一层目录。

---

## 6. 权限一览

| 类别 | 路径 | 权限 | 属主 |
|---|---|---|---|
| 站点文件/目录 | `/webs/web/**` | 0755 | root:root |
| 服务端配置/证书 | `/webs/*.txt`、`/webs/*.crt`、`/webs/*.key` | 0755 | root:root |
| 主程序 | `/usr/bin/webs`、`/usr/bin/libgo.so` | 0755 | root:root |
| 依赖库 | `/usr/lib/libfibosocket.so.0.0.0` | 0755 | root:root |
| 符号链接 | `/usr/lib/libfibosocket.so.0`、`.../multi-user.target.wants/webs.service` | 0777 | root:root |
| 启停脚本 | `/etc/initscripts/start-webs`、`webs-deamon`、`webs-stop` | 0755 | root:root |
| 服务单元 | `/lib/systemd/system/webs.service` | 0644 | root:root |

---

## 7. 移植后验证

```bash
adb shell "cat /webs/web/webui_version"                 # 期望 WEBUI_V_02.02.10
adb shell "pidof webs"                                  # 期望有进程
adb shell "netstat -lntp 2>/dev/null | grep -E ':80|:443'"
adb shell "ldd /usr/bin/webs"                           # 检查 libgo/libfibosocket 等依赖是否齐全
```

浏览器访问 `http://<模组IP>/`：
- 登录页正常（`index.html`、`css`、`js`、`static` 路径正确）；
- 登录与页面数据正常（说明 `/fibo/fibologin`、`/fibo/webapi` 正常，`libgo.so` + `libfibosocket` + 底座服务可用）。

本地完整性校验（PowerShell，在本目录执行）：

```powershell
$rows = Import-Csv .\文件清单.csv -Header path,type,mode,size,sha,desc | Select-Object -Skip 1
foreach ($r in $rows) {
    if ($r.sha -eq '-') { continue }          # 跳过目录/符号链接
    $local = Join-Path (Join-Path $PWD 'rootfs') ($r.path.TrimStart('/'))
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $local).Hash.ToLower() -ne $r.sha) {
        Write-Host "MISMATCH $($r.path)"
    }
}
```

---

## 8. 回滚

```bash
adb shell "rm -rf /webs && cp -a /data/webs.bak.<时间戳> /webs"
adb shell "cp -a /data/webs.bin.<时间戳> /usr/bin/webs"
adb shell "cp -a /data/libgo.so.<时间戳> /usr/bin/libgo.so"
adb shell "cp -a /data/libfibosocket.so.0.0.0.<时间戳> /usr/lib/libfibosocket.so.0.0.0"
adb shell "ln -sf libfibosocket.so.0.0.0 /usr/lib/libfibosocket.so.0"
adb shell "systemctl restart webs.service"
adb shell "chmod -R 0755 /webs"
```

若回滚到 lighttpd 方案，还需 `systemctl enable --now lighttpd`，并停止 `webs.service`。

---

## 9. 兼容性与注意事项

1. **CPU 架构**：`webs`、`libgo.so`、`libfibosocket.so.0.0.0` 均为 **ARM 32 位**（`ELF 32-bit LSB ARM EABI5`，`/lib/ld-linux-armhf.so.3`）。目标执行 `adb shell uname -m` 应为 `armv7l`。
2. **依赖库链**（缺失会导致 `webs` 无法启动）：
   - `webs` → `libgo.so`、`libc.so.6`、`ld-linux-armhf.so.3`
   - `libgo.so` → `libpthread.so.0`、`libfibosocket.so.0`、`libc`
   - `libfibosocket.so.0` → `librt.so.1`、`libfibo_utils.so.0`、`libdiag.so.1`、`libc`
   - `libfibo_utils` → `libsqlite3.so.0`、`libdiag`
   - `libdiag` → `libgthread-2.0`、`libglib-2.0`、`liblog.so.0`、`libtime_genoff.so.1`
   - 包声明还依赖 `libsystemdq`、`libxml2`、`libc6 >= 2.31`
   本组件只带 `libgo.so` 与 `libfibosocket`，其余为底座公共库；目标必须已具备（FM150AE/FM160 原厂固件通常已有）。
3. **底座服务**：`webs.service` 依赖 `init_sys_mss.service`、`ipacm.service`、`netmgrd.service`；这些服务未启动时 Web 后台不可用。
4. **端口冲突**：`webs` 固定占用 80/443，与 lighttpd 冲突。切换方案时先 `systemctl stop lighttpd; systemctl disable lighttpd`，否则 `webs` 绑定失败。
5. **版本不可混用**：本包为 `WEBUI_V_02.02.10 + webs(GoAhead)`；之前 system.img 为 `WEBUI_V_02.02.28 + lighttpd`。两套 `index.html`、分包哈希、服务端都不同，**不要只替换部分文件**，应整套替换 `/webs` 与对应服务端。
6. **符号链接不可丢**：`/usr/lib/libfibosocket.so.0` 与 `multi-user.target.wants/webs.service` 必须重建；脚本已处理。
7. **证书安全**：`/webs/*.key` 为明文私钥，原固件权限 0755（所有用户可读）。生产环境建议收紧为 0600 并替换自签/测试证书。
8. **`auth.txt` 无用户**：不要误以为需要改密码；登录由 `fibologin` 处理器与底座账户体系完成。若启用 basic/digest 路由，需按文件内 schema 自行添加用户与角色。
9. **SELinux**：该 rootfs 带 `/etc/selinux/mls` 策略；若目标为 enforcing，`webs` 及 `/webs` 需要正确上下文，可用 `getenforce` 确认，必要时 `restorecon -R /webs /usr/bin/webs /usr/bin/libgo.so /usr/lib/libfibosocket*`。
10. **只读根分区**：`/webs` 与 `/usr/bin` 通常位于只读系统分区；如无法 remount，请使用厂商 overlay/可写分区方案，并确保运行时仍映射到上述绝对路径。
11. **可选配置**：`/etc/webui_url`（内容 `fibo.web.com`）被 `/usr/bin/fibo-app-framework` 引用，本组件未包含；目标已有则保留，缺失不影响 `webs` 本身。
12. **停止脚本的行为**：`webs-stop` 按进程名 `webs-deamon` 结束，若手工直接启动 `/usr/bin/webs`，停止时可能需 `killall webs`。

---

## 10. 解包过程记录（可复现）

```bash
# UBI 结构确认
ubireader_display_info sdxlemur-sysfs.ubi
#   PEB 262144 / LEB 253952 / 303 PEB / 单卷 rootfs（301 块，autoresize）

# 保留权限解包（必须 root，否则普通文件会落成 0644）
sudo ubireader_extract_files -k -o /tmp/ubi_root sdxlemur-sysfs.ubi

# 组件定位
cat /tmp/ubi_root/*/rootfs/webs/web/webui_version        # WEBUI_V_02.02.10
cat /tmp/ubi_root/*/rootfs/var/lib/opkg/info/goahead.list # 110 条组件清单
```

核对结果：`goahead.list` 的 110 条路径全部存在；另按 `libfibosocket0.list` 补充 2 条依赖文件/链接；WEBUI 站点文件共 84 个，与清单一致，无缺失。
