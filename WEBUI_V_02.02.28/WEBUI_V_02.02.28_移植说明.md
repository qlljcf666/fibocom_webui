# WEBUI_V_02.02.28 组件解包与 adb 移植说明

适用模组：Fibocom **FM150AE / FM160**
组件版本：**WEBUI_V_02.02.28**
整理日期：2026-11-12

---

## 1. 概述

| 项目 | 内容 |
|---|---|
| 源镜像 | `160/system.img`（268,435,456 字节，即 256 MB） |
| 镜像格式 | 裸 ext4 文件系统镜像（无分区表；超级块在 0x400，magic `0xEF53`，block size 4096，共 65536 块） |
| 平台标识 | `ro.product.name=sdxlemur-qti-distro-nogplv3-debug`，`/target=sdxlemur-ab` |
| 版本串 | `ro.build.version.release=r00196.1-602-g11754168df` |
| 组件归属 | opkg 包 `lighttpd`（`/var/lib/opkg/info/lighttpd.list` 内含全部 WEBUI 文件） |
| 包版本/架构 | `lighttpd 1.4.76-r3`，`armv7vet2hf-neon`（ARM 32 位） |
| WebUI 版本文件 | `/webs/web/webui_version` → `WEBUI_V_02.02.28` |
| 提取结果 | 114 个组件文件 + 4 个包元数据文件；组件文件合计 4,827,466 字节 |

组件本质是 **lighttpd + 一套 Vue/Element 单页应用（SPA）**：`/webs/web` 为静态站点根目录，`/data/lighttpd.conf` 为服务配置，`/etc/initscripts/lighttpd` 与 systemd unit 负责启动和证书生成。

---

## 2. 交付目录

所有提取文件按**设备原始绝对路径**还原在 `rootfs/` 下，可直接对照设备路径推送：

```text
WEBUI_V_02.02.28/
├─ rootfs/                              # 设备根文件系统镜像目录
│  ├─ webs/web/                         # WEBUI 站点（104 个文件，4,519,797 B）
│  │  ├─ index.html                     # 入口页面
│  │  ├─ config.js                      # 登录页品牌配置
│  │  ├─ webui_version                  # 版本标识 WEBUI_V_02.02.28
│  │  ├─ favicon.ico
│  │  ├─ css/                           # 20 个样式文件
│  │  ├─ js/                            # 60 个脚本文件
│  │  ├─ images/                        # 9 个品牌/背景图片
│  │  ├─ json/permission.json           # 菜单与权限配置
│  │  └─ static/{fonts,img}/            # 10 个字体/静态图片
│  ├─ etc/
│  │  ├─ initscripts/lighttpd           # 启停脚本（证书生成/会话清理）
│  │  ├─ backup_restore.sh              # 配置备份恢复脚本
│  │  ├─ lighttpd/                      # 空目录（预留）
│  │  └─ lighttpd.d/                    # 空目录（预留 conf.d）
│  ├─ data/
│  │  ├─ lighttpd.conf                  # lighttpd 主配置
│  │  ├─ firewall.fibocom               # 防火墙默认开关
│  │  ├─ openssl.cnf                    # 自签证书参数
│  │  └─ backup.list                    # 备份清单（仅 /data）
│  ├─ usr/sbin/
│  │  ├─ lighttpd                       # ARM32 主程序
│  │  └─ lighttpd-angel                 # 守护/拉起程序
│  └─ lib/
│     ├─ systemd/system/lighttpd.service
│     └─ systemd/system-preset/98-lighttpd.preset
├─ package-meta/                        # opkg 元数据，仅作参考，不推送
│  ├─ lighttpd.control
│  ├─ lighttpd.list                     # 组件权威文件清单（路径 + 权限）
│  ├─ lighttpd.postinst
│  └─ lighttpd.prerm
├─ 文件清单.csv                          # 逐文件路径/权限/大小/SHA256/说明
├─ adb_push_webui.ps1                   # 一键推送脚本（Windows PowerShell）
└─ WEBUI_V_02.02.28_移植说明.md          # 本文档
```

---

## 3. 文件清单与权限

### 3.1 站点根目录 `/webs/web` 汇总

| 路径 | 文件数 | 大小(字节) | 权限 | 说明 |
|---|---:|---:|---|---|
| `/webs/web`（根级文件） | 4 | 5,178 | 0755 | `index.html`、`config.js`、`webui_version`、`favicon.ico` |
| `/webs/web/css` | 20 | 278,188 | 0755（目录） | 第三方样式 + 主样式 + 各页面分包样式 |
| `/webs/web/js` | 60 | 1,674,748 | 0755（目录） | `index` 主逻辑、`chunk-vendors` 依赖、58 个页面异步分包 |
| `/webs/web/images` | 9 | 2,189,295 | 0755（目录） | 登录页、背景、Logo 等品牌图 |
| `/webs/web/json` | 1 | 37,441 | 0755（目录） | `permission.json` 菜单/权限定义 |
| `/webs/web/static/fonts` | 5 | 123,036 | 0755（目录） | iconfont / Element 图标字体 |
| `/webs/web/static/img` | 5 | 211,911 | 0755（目录） | 构建后的背景与 WLAN 状态图标 |
| **合计** | **104** | **4,519,797** | — | — |

> 注意：该固件打包时把 `/webs/web` 下**所有普通文件统一设为 0755**（非常规 0644），目录为 0755，属主均为 `root:root`。这是 `lighttpd.list` 中的原始记录，移植时保持一致即可。

### 3.2 关键文件明细

| 设备绝对路径 | 类型 | 权限 | 大小(字节) | 说明 |
|---|---|---|---:|---|
| `/webs/web/index.html` | 文件 | 0755 | 750 | SPA 入口。引用 `/js/chunk-vendors.1732844826120.js`、`/js/index.1732844826120.js`、`/css/...` 等**绝对路径**，因此站点必须部署在 `/webs/web` |
| `/webs/web/config.js` | 文件 | 0755 | 126 | 登录页配置：标题 `Fibocom`、登录插图 `./images/login_pic.png`、Logo `./images/logo1.png` |
| `/webs/web/webui_version` | 文件 | 0755 | 16 | 内容 `WEBUI_V_02.02.28`，固件/AT 查询 WebUI 版本时读取此文件 |
| `/webs/web/favicon.ico` | 文件 | 0755 | 4,286 | 浏览器站点图标 |
| `/webs/web/json/permission.json` | 文件 | 0755 | 37,441 | 二级菜单与页面权限：`navs`、`children`、`title`、`icon`、`index`、`show`；决定首页、网络、WLAN、设备、工具等菜单显隐 |
| `/data/lighttpd.conf` | 文件 | 0755 | 13,477 | 主配置：监听 80，`server.document-root=/webs/web/`，`server.chroot="/"`，上传目录 `/tmp`，443 端口启用 TLS（`/data/lighttpd.key`、`/data/lighttpd.pem`） |
| `/data/firewall.fibocom` | 文件 | 0755 | 81 | 防火墙默认值：`fibo_firewall_switch 1`、`fibo_firewall_ping_wan=1`、`fibo_firewall_urlfilter_policy=0` |
| `/data/openssl.cnf` | 文件 | 0755 | 9,771 | 自签证书的 `openssl req -config` 参数（主体、扩展等） |
| `/data/backup.list` | 文件 | 0644 | 6 | 内容仅 `/data`，供 `backup_restore.sh` 打包/恢复配置 |
| `/etc/initscripts/lighttpd` | 文件 | 0755 | 4,813 | 启停脚本：启动/重启时若 `/data/lighttpd.key`、`/data/lighttpd.pem` 不存在则调用 openssl 生成；清理 `/data/www` 下会话文件；在“证书不存在、需要生成”的分支里对 `/webs/web` 执行 `chmod -R 775` |
| `/etc/backup_restore.sh` | 文件 | 0755 | 2,649 | 配置备份/恢复：`backup` 打包 `/etc/backup.list` 列出的路径到 `/tmp/cfg.tar.gz`，`restore` 反向覆盖 |
| `/usr/sbin/lighttpd` | 文件 | 0755 | 270,980 | lighttpd 主程序，ELF 32-bit ARM EABI5，动态链接 `/lib/ld-linux-armhf.so.3` |
| `/usr/sbin/lighttpd-angel` | 文件 | 0755 | 5,520 | lighttpd 守护进程（崩溃后拉起），同为 ARM32 ELF |
| `/lib/systemd/system/lighttpd.service` | 文件 | 0644 | 348 | systemd 单元：`ExecStartPre=/etc/initscripts/lighttpd start`，`ExecStart=/usr/sbin/lighttpd -f /data/lighttpd.conf`，`Restart=always`，`Nice=-20` |
| `/lib/systemd/system-preset/98-lighttpd.preset` | 文件 | 0644 | 24 | 内容 `enable lighttpd.service`，首次安装默认开机自启 |
| `/webs` | 目录 | 0755 | — | 前端父目录 |
| `/webs/web` | 目录 | 0755 | — | 站点根目录（document-root） |
| `/etc/lighttpd`、`/etc/lighttpd.d` | 目录 | 0755 | — | 空目录，配置中未加载 `conf.d`，保留结构 |

### 3.3 包元数据（不推送，仅供核对）

| 文件 | 权限 | 大小(字节) | 说明 |
|---|---|---:|---|
| `package-meta/lighttpd.control` | 0644 | 881 | 包名/版本/依赖/架构 |
| `package-meta/lighttpd.list` | 0644 | 5,180 | **权威清单**：125 条“路径 + 模式”，本次全部提取成功，无缺失 |
| `package-meta/lighttpd.postinst` | 0755 | 424 | 安装后 `systemctl enable/restart lighttpd` |
| `package-meta/lighttpd.prerm` | 0755 | 165 | 卸载前停止并禁用服务 |

### 3.4 完整逐文件清单

[`文件清单.csv`](./文件清单.csv) 含 125 行（114 文件 + 11 目录），列为：

`原始绝对路径 | 类型 | 权限(八进制) | 大小(字节) | SHA256 | 说明`

其中 58 个 `js/<数字>.<构建号>.js` 与 18 个 `css/chunk.<数字>.<构建号>.css` 是 Webpack **异步分包**（按路由/组件按需加载），文件名中的数字和 `1732844826120` 为构建哈希，**不可改名**，否则入口 JS 无法加载对应页面。图片目录中带哈希的文件同理。

---

## 4. 运行机制

1. **开机启动**：systemd 读取 `/lib/systemd/system/lighttpd.service`（preset 默认 enable）→ 先执行 `/etc/initscripts/lighttpd start` 生成/复用自签证书 → 再执行 `/usr/sbin/lighttpd -f /data/lighttpd.conf`。
2. **监听端口**：HTTP `80`；HTTPS `443`（`mod_openssl`，TLS 最低 1.2），证书 `/data/lighttpd.pem` + 私钥 `/data/lighttpd.key`。
3. **站点根**：`server.document-root = "/webs/web/"`，`server.chroot = "/"`；`index.html` 使用 `/js`、`/css` 绝对路径，部署位置不能改。
4. **运行身份**：配置中 `server.username` / `server.groupname` 均被注释，因此 lighttpd 以 root 运行；`/etc/initscripts/lighttpd` 会把 `/webs/web` 设为 775。
5. **证书生成**：缺少证书时，initscript 用 `/data/openssl.cnf` 调用 `openssl genrsa/req` 生成 `/data/lighttpd.key`、`/data/lighttpd.pem`（1024 位 RSA，仅适合调试；生产环境建议替换）。
6. **前端结构**：Vue + Vuex + Element UI；登录成功后把 `Authorization` 写入 `sessionStorage`；页面通过 Vuex action（如 `GetDeviceInfo`）调用**同源后端接口**。
7. **后端依赖（重要）**：本组件只包含静态前端与 lighttpd，不包含业务后端。原镜像中由底座固件提供 `/usr/bin/QCMAP_Web_CLIENT` 及 fibo 应用框架，并通过 `/data/www` 下的 `session_token.txt`、`login_attempt`、`qcmap_session`、`/dev/socket/data/www/qcmap_cgi_webclient_file` 维护登录会话。**目标模组必须已有这套 QCMAP Web 后端，否则页面能打开但 API 全部失败。**

---

## 5. adb 上传移植步骤

### 5.1 前置条件

- 模组已启动 Linux，`adb devices` 能看到设备且已授权。
- 具备 root（`adb root`）或可 `remount` 写根分区；若根分区只读，先按目标固件方式切到可写（本镜像存在 `/systemrw`、`/overlay` 目录，说明使用 overlay/可写机制）。
- 记录目标现有 `/webs/web`、`/data/lighttpd.conf` 以便回滚。

### 5.2 一键脚本（推荐）

在 Windows PowerShell 中、于 `WEBUI_V_02.02.28` 目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\adb_push_webui.ps1
```

脚本流程：`adb root/remount` → 备份到 `/data/webs_web.bak.<时间>` → 建目录 → **逐文件**按原始绝对路径推送 → 恢复 0755/0644 权限与 `root:root` 属主 → `systemctl daemon-reload/enable/restart`（无 systemd 时回退 `/etc/initscripts/lighttpd restart`）→ 打印 `webui_version` 与进程。

### 5.3 手工命令

```bash
# 1) 获取写权限
adb root
adb remount
adb shell "mount -o remount,rw /"

# 2) 备份
adb shell "cp -a /webs/web /data/webs_web.bak"
adb shell "cp -a /data/lighttpd.conf /data/lighttpd.conf.bak"

# 3) 建目录
adb shell "mkdir -p /webs/web /etc/initscripts /etc/lighttpd /etc/lighttpd.d /data /usr/sbin /lib/systemd/system /lib/systemd/system-preset"

# 4) 推送（在解包目录执行；逐文件推送最稳妥）
adb push rootfs/webs/web/index.html /webs/web/index.html
adb push rootfs/webs/web/config.js /webs/web/config.js
adb push rootfs/webs/web/webui_version /webs/web/webui_version
adb push rootfs/webs/web/favicon.ico /webs/web/favicon.ico
adb push rootfs/webs/web/json/permission.json /webs/web/json/permission.json
adb push rootfs/webs/web/css /webs/web/
adb push rootfs/webs/web/js /webs/web/
adb push rootfs/webs/web/images /webs/web/
adb push rootfs/webs/web/static /webs/web/

adb push rootfs/etc/initscripts/lighttpd /etc/initscripts/lighttpd
adb push rootfs/etc/backup_restore.sh /etc/backup_restore.sh
adb push rootfs/data/lighttpd.conf /data/lighttpd.conf
adb push rootfs/data/firewall.fibocom /data/firewall.fibocom
adb push rootfs/data/openssl.cnf /data/openssl.cnf
adb push rootfs/data/backup.list /data/backup.list
adb push rootfs/usr/sbin/lighttpd /usr/sbin/lighttpd
adb push rootfs/usr/sbin/lighttpd-angel /usr/sbin/lighttpd-angel
adb push rootfs/lib/systemd/system/lighttpd.service /lib/systemd/system/lighttpd.service
adb push rootfs/lib/systemd/system-preset/98-lighttpd.preset /lib/systemd/system-preset/98-lighttpd.preset

# 5) 权限与属主（与包清单一致）
adb shell "chmod -R 0755 /webs/web"
adb shell "chmod 0755 /etc/initscripts/lighttpd /etc/backup_restore.sh /data/lighttpd.conf /data/firewall.fibocom /data/openssl.cnf /usr/sbin/lighttpd /usr/sbin/lighttpd-angel"
adb shell "chmod 0644 /data/backup.list /lib/systemd/system/lighttpd.service /lib/systemd/system-preset/98-lighttpd.preset"
adb shell "chown -R root:root /webs/web /etc/initscripts/lighttpd /etc/backup_restore.sh /data/lighttpd.conf /data/firewall.fibocom /data/openssl.cnf /data/backup.list /usr/sbin/lighttpd /usr/sbin/lighttpd-angel /lib/systemd/system/lighttpd.service /lib/systemd/system-preset/98-lighttpd.preset"

# 6) 重启
adb shell "systemctl daemon-reload; systemctl enable lighttpd.service; systemctl restart lighttpd.service"
# 无 systemd 时：
# adb shell "/etc/initscripts/lighttpd restart"
```

> `adb push 目录` 在不同 adb 版本上对“目录合并/创建同名子目录”的行为不一致，因此脚本采用**逐文件推送**，路径确定、不会多出一层 `web/web`。

---

## 6. 权限一览

| 类别 | 路径 | 权限 | 属主 |
|---|---|---|---|
| 站点文件 | `/webs/web/**`（104 个文件） | 0755 | root:root |
| 站点目录 | `/webs`、`/webs/web`、`css`、`js`、`images`、`json`、`static`、`static/fonts`、`static/img` | 0755 | root:root |
| 服务端配置/脚本 | `/data/lighttpd.conf`、`/data/firewall.fibocom`、`/data/openssl.cnf`、`/etc/initscripts/lighttpd`、`/etc/backup_restore.sh` | 0755 | root:root |
| 可执行程序 | `/usr/sbin/lighttpd`、`/usr/sbin/lighttpd-angel` | 0755 | root:root |
| 只读数据/单元文件 | `/data/backup.list`、`/lib/systemd/system/lighttpd.service`、`/lib/systemd/system-preset/98-lighttpd.preset` | 0644 | root:root |

---

## 7. 移植后验证

```bash
adb shell "cat /webs/web/webui_version"          # 期望输出 WEBUI_V_02.02.28
adb shell "pidof lighttpd || ps | grep lighttpd" # 期望有进程
adb shell "ss -lntp | grep -E ':80|:443'"        # 期望 80/443 监听
```

浏览器访问 `http://<模组IP>/`：

- 能打开 Fibocom 登录页（说明 `index.html`、`css`、`js`、`images` 路径正确）；
- 登录页 Logo/背景正常（`config.js` 与 `images/logo1.png`、`login_pic.png` 正常）；
- 登录后能获取设备信息（说明 QCMAP Web 后端正常）。

本地完整性校验（PowerShell，在解包目录执行）：

```powershell
# 用自定义表头读取，避免中文列名在 PowerShell 5.1 下的编码问题
$rows = Import-Csv .\文件清单.csv -Header path,type,mode,size,sha,desc | Select-Object -Skip 1
foreach ($r in $rows) {
    if ($r.sha -eq '-') { continue }          # 跳过目录
    $local = Join-Path (Join-Path $PWD 'rootfs') ($r.path.TrimStart('/'))
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $local).Hash.ToLower() -ne $r.sha) {
        Write-Host "MISMATCH $($r.path)"
    }
}
```

---

## 8. 回滚

```bash
adb shell "rm -rf /webs/web && cp -a /data/webs_web.bak.<时间戳> /webs/web"
adb shell "cp -a /data/lighttpd.conf.bak.<时间戳> /data/lighttpd.conf"
adb shell "systemctl restart lighttpd.service"
adb shell "chmod -R 0755 /webs/web"
```

> 替换整个 `/webs/web` 前建议先删除旧目录：旧版本残留的哈希分包不会被覆盖，会额外占用空间，也可能造成排障干扰。

---

## 9. 兼容性与注意事项

1. **CPU 架构**：本次提取的 `lighttpd`、`lighttpd-angel` 是 **ARM 32 位**（`ELF 32-bit LSB ARM EABI5`，解释器 `/lib/ld-linux-armhf.so.3`）。先在目标执行 `adb shell uname -m`：
   - `armv7l`：可直接使用；
   - `aarch64`：仅当系统带 32 位运行库（`ld-linux-armhf.so.3`、`libc6`）时可用；否则**只推送 `/webs/web` 与配置，保留目标自带的 lighttpd 程序**。
2. **动态库依赖**：`libc6 >= 2.31`、`libcrypto3 >= 3.0.10`、`libpcre2 >= 10.34`，以及 `lighttpd-module-*`（auth、authn-file、cgi、dirlisting、openssl、webdav）。配置实际只启用了 `mod_openssl`。
3. **不要覆盖产品专属配置**：若目标已有 `/data/lighttpd.conf` 且端口、document-root、证书路径不同，请先比对再合并；脚本已自动备份。
4. **端口冲突**：默认占用 80/443，若目标已有 Web 服务，修改 `/data/lighttpd.conf` 的 `server.port` 与 443 socket 段。
5. **`/data` 必须可写**：证书（`lighttpd.key`/`lighttpd.pem`）、会话文件、备份都写入 `/data`；若 `/data` 只读或未挂载，HTTPS 与登录会失败。
6. **根分区只读**：来源镜像含 `/systemrw`、`/overlay`，说明固件通过 overlay 实现可写。目标若为只读 UBIFS/squashfs，请使用厂商的 overlay/remount 机制，或把组件放到可写分区后绑定到 `/webs/web`。
7. **界面功能差异**：`permission.json` 是 Fibocom 多产品共用菜单，包含 Ethernet、WLAN、GuestWlan、W5G/W6G 等条目。FM150AE/FM160 若无对应硬件能力，可通过 `show` 字段隐藏，避免出现点不动的菜单。
8. **硬编码测试地址**：主逻辑 `js/index.1732844826120.js` 中存在 FOTA 测试地址 `http://47.110.234.36:8000/httpfota/test1/FG360-EAU-00/bxy/`（FG360 测试产物）。移植到 FM150AE/FM160 时，若启用 FOTA，需要替换为目标产品的升级地址。
9. **签名/权限安全**：initscript 生成的 1024 位自签证书仅用于调试；正式产品应替换证书。Web 文件全部 0755 且服务以 root 运行，属于原厂打包方式，移植时保持一致以确保可启动。
10. **版本识别**：固件通过 `/webs/web/webui_version` 获取 WebUI 版本（原镜像中存在 `oem_device_get_web_ui_version` 读取逻辑），请勿删除该文件。

---

## 10. 解包过程记录（可复现）

镜像头部前 512 字节为 0，偏移 `0x400` 处为 ext4 超级块：

- `s_inodes_count=16384`，`s_blocks_count_lo=65536`，`s_log_block_size=2`（4096 字节/块），`s_magic=0xEF53` → 裸 ext4 镜像，无 MBR/GPT 分区表。

在 WSL Ubuntu 中使用 `debugfs`（只读方式，未修改原镜像）：

```bash
IMG=system.img
debugfs -R "ls -l /" "$IMG"                       # 确认 /webs/web
debugfs -R "cat /webs/web/webui_version" "$IMG"   # WEBUI_V_02.02.28
debugfs -R "cat /var/lib/opkg/info/lighttpd.list" "$IMG"  # 125 条组件文件清单
debugfs -R "rdump /webs/web /tmp/out" "$IMG"      # 导出整站
debugfs -R "dump /data/lighttpd.conf /tmp/out" "$IMG"     # 导出单文件
```

组件边界判定依据：`/webs/web/webui_version` 给出组件版本；`/var/lib/opkg/info/lighttpd.list` 给出该组件全部 125 个路径及模式，按清单逐项导出后核对，**无缺失、无多余**（104 个站点文件 + 10 个系统文件 + 11 个目录）。
