use crate::at;
use crate::config::ConnSpec;
use crate::manager::{probe_serial, Manager};
use crate::profiles::Profile;
use serde_json::{json, Value};
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub mgr: Arc<Manager>,
    pub exit: Arc<std::sync::atomic::AtomicBool>,
    pub heartbeat: Arc<std::sync::Mutex<std::time::Instant>>,
    pub heartbeat_seen: Arc<std::sync::atomic::AtomicBool>,
    pub started: std::time::Instant,
}

pub struct RpcError {
    code: i32,
    message: String,
}

impl RpcError {
    pub fn new(m: impl Into<String>) -> Self {
        Self {
            code: 1,
            message: m.into(),
        }
    }

    pub fn message(&self) -> String {
        self.message.clone()
    }

    #[allow(dead_code)]
    pub fn code(&self) -> i32 {
        self.code
    }
}

type RpcResult = Result<Value, RpcError>;

fn s(v: &Value, key: &str) -> String {
    v.get(key)
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .to_string()
}

fn i(v: &Value, key: &str) -> i64 {
    v.get(key).and_then(|x| x.as_i64()).unwrap_or(0)
}

fn b(v: &Value, key: &str) -> bool {
    match v.get(key) {
        Some(Value::Bool(x)) => *x,
        Some(Value::Number(n)) => n.as_i64().unwrap_or(0) != 0,
        Some(Value::String(s)) => s == "1" || s == "true",
        _ => false,
    }
}

fn param<'a>(params: &'a Value, keys: &[&str]) -> String {
    for k in keys {
        if let Some(v) = params.get(*k) {
            if let Some(x) = v.as_str() {
                return x.to_string();
            }
            if let Some(n) = v.as_i64() {
                return n.to_string();
            }
        }
    }
    String::new()
}

pub fn handle(mgr: Arc<Manager>, method: &str, params: &Value) -> RpcResult {
    match method {
        // ---------------- 登录 ----------------
        "Login" => Ok(json!({})),
        "Logout" => Ok(json!({})),
        "GetLoginState" => Ok(json!({
            "LoginState": 0,
            "LockedTime": 0,
            "LoginVerify": 0,
            "LoginVerifyMax": 5
        })),
        "ChangePassword" => Ok(json!({})),
        "GetCurrentLanguage" => {
            let mut lang = mgr.settings.lock().unwrap().language.clone();
            if lang == "zh" || lang.is_empty() {
                lang = "ch".into();
            }
            Ok(json!({ "Language": lang }))
        }
        "SetLanguage" => {
            let lang = param(params, &["Language", "language"]);
            if !lang.is_empty() {
                let mut s = mgr.settings.lock().unwrap();
                s.language = if lang == "zh" { "ch".into() } else { lang };
                s.save();
            }
            Ok(json!({}))
        }

        // ---------------- 设备信息 ----------------
        "GetDeviceInfo" => {
            let s = mgr.snapshot();
            Ok(json!({
                "DeviceName": s.model,
                "ModelName": s.model,
                "Manufacturer": s.manufacturer,
                "IMEI": s.imei,
                "IMSI": s.imsi,
                "ICCID": s.iccid,
                "MSISDN": s.msisdn,
                "SN": s.imei,
                "sn": s.imei,
                "SwVersion": s.fw,
                "swVersion": s.fw,
                "SoftwareVersion": s.fw,
                "HwVersion": "",
                "hwVersion": "",
                "HardwareVersion": "",
                "WebUiVersion": "Rust-PC 1.0.0",
                "webUiVersion": "Rust-PC 1.0.0",
                "ConfigVersion": "",
                "MACAddress": "",
                "macAddress": "",
                "WANIPaddress": s.ipv4,
                "WanIPAddress": s.ipv4,
                "wanIP": s.ipv4,
                "WANConnectionStatus": if s.pdn_active { 1 } else { 0 },
                "CellId": s.cell_id,
                "PLMN": s.operator,
                "RAT": s.rat,
                "CQI": 0,
                "RSRP": s.rsrp,
                "RSRQ": s.rsrq,
                "SINR": s.sinr,
                "Band": s.band
            }))
        }
        "GetDeviceState" => {
            let s = mgr.snapshot();
            let run = {
                let g = mgr.engine.lock().unwrap();
                g.as_ref()
                    .map(|e| e.connected_at.elapsed().as_secs())
                    .unwrap_or(0)
            };
            Ok(json!({
                "RunTime": run,
                "CellId": s.cell_id,
                "CQI": 0,
                "RSRP": s.rsrp,
                "RSRQ": s.rsrq,
                "SINR": s.sinr,
                "Band": s.band,
                "PLMN": s.operator,
                "RAT": s.rat,
                "CSQ": s.csq,
                "SimState": s.sim_state,
                "WANConnectionStatus": if s.pdn_active { 1 } else { 0 }
            }))
        }
        "GetCaInfo" => Ok(json!({ "LTE_CaState": 0, "NR_CaState": 0 })),

        // ---------------- SIM / PIN ----------------
        "GetSimStatus" => {
            let s = mgr.snapshot();
            Ok(json!({
                "SIMState": s.sim_state,
                "PinState": s.pin_state,
                "PinRemainingTimes": s.pin_remain,
                "PukRemainingTimes": s.puk_remain,
                "IMSI": s.imsi,
                "ICCID": s.iccid,
                "MSISDN": s.msisdn
            }))
        }
        "UnlockPin" => {
            let pin = param(params, &["Pin", "PIN", "pin"]);
            if pin.is_empty() {
                return Err(RpcError::new("请输入 PIN 码"));
            }
            mgr.cmd_ok(&format!("AT+CPIN=\"{}\"", pin), 3000)
                .map_err(RpcError::new)?;
            mgr.refresh_now();
            Ok(json!({}))
        }
        "UnlockPuk" => {
            let puk = param(params, &["Puk", "PUK", "puk"]);
            let pin = param(params, &["Pin", "PIN", "pin"]);
            if puk.is_empty() || pin.is_empty() {
                return Err(RpcError::new("请输入 PUK 和新的 PIN"));
            }
            mgr.cmd_ok(&format!("AT+CPIN=\"{}\",\"{}\"", puk, pin), 5000)
                .map_err(RpcError::new)?;
            mgr.refresh_now();
            Ok(json!({}))
        }
        "ChangePinCode" => {
            let old = param(params, &["Pin", "OldPin"]);
            let new = param(params, &["NewPin", "newPin"]);
            if old.is_empty() || new.is_empty() {
                return Err(RpcError::new("请输入原 PIN 与新 PIN"));
            }
            mgr.cmd_ok(&format!("AT+CPIN=\"{}\",\"{}\"", old, new), 5000)
                .map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "ChangePinState" => {
            let pin = param(params, &["Pin", "PIN"]);
            let state = i(params, "State");
            let enable = if state == 2 { 1 } else { 0 };
            mgr.cmd_ok(&format!("AT+CLCK=\"SC\",{},\"{}\"", enable, pin), 4000)
                .map_err(RpcError::new)?;
            mgr.refresh_now();
            Ok(json!({}))
        }

        // ---------------- 网络 ----------------
        "GetNetworkInfo" => {
            let s = mgr.snapshot();
            Ok(json!({
                "NetworkName": s.operator,
                "RAT": s.rat,
                "CSQ": s.csq,
                "ListNetworkInfo": [{
                    "Roaming": s.roaming,
                    "MCC": "",
                    "MNC": "",
                    "NetworkName": s.operator
                }],
                "PLMN": s.operator
            }))
        }
        "GetSignalStrength" => {
            let s = mgr.snapshot();
            Ok(json!({
                "SignalLevel": s.signal_level,
                "RSRP": s.rsrp,
                "RSRQ": s.rsrq,
                "SINR": s.sinr,
                "RSSI": if s.csq == 99 { -113 } else { -113 + 2 * s.csq },
                "CSQ": s.csq
            }))
        }
        "GetNetworkRegisterState" => {
            let s = mgr.snapshot();
            // QCMAP 语义：2=已注册（本地/漫游），1=搜网中，3=拒绝，0=无服务
            let regist = match s.reg_state {
                1 | 5 => 2,
                2 => 1,
                3 => 3,
                _ => 0,
            };
            Ok(json!({ "regist_state": regist }))
        }
        "GetNetworkSettings" => {
            let s = mgr.snapshot();
            Ok(json!({
                "NetselectionMode": 0,
                "Band_LIST": [],
                "RATPrefer": 0,
                "RATPriority": 0,
                "RAT": s.rat
            }))
        }
        "SetNetworkSettings" => {
            let mode = i(params, "NetselectionMode");
            if mode == 0 {
                mgr.cmd_ok("AT+COPS=0", 5000).map_err(RpcError::new)?;
                mgr.refresh_now();
                Ok(json!({}))
            } else {
                Err(RpcError::new("本工具暂不支持手动锁网，请在自动选网模式下使用"))
            }
        }
        "GetNRNetworkMode" => {
            let s = mgr.snapshot();
            Ok(json!({
                "NRState": if s.rat == 8 { 1 } else { 1 },
                "NRMode": 3
            }))
        }
        "SetNRNetworkMode" => {
            let on = b(params, "NRState") || i(params, "NRState") == 1;
            let profile = current_profile(&mgr);
            let cmd = if on { "AT+GTRAT=20" } else { "AT+GTRAT=3" };
            if let Some(p) = profile {
                if !p.gtrat {
                    let cmd = if on { "AT+GTACT=17" } else { "AT+GTACT=3" };
                    mgr.cmd_ok(cmd, 4000).map_err(RpcError::new)?;
                    return Ok(json!({}));
                }
            } else {
                return Err(RpcError::new("未连接模组"));
            }
            mgr.cmd_ok(cmd, 4000).map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "GetMobileDataStatus" => {
            let s = mgr.snapshot();
            Ok(json!({
                "MobileDateSwitch": if s.pdn_active { 1 } else { 0 },
                "RoamingSwitch": s.roaming
            }))
        }
        "SetMobileDataStatus" => {
            let on = b(params, "MobileDateSwitch") || i(params, "MobileDateSwitch") == 1;
            if on {
                let _ = mgr.cmd_ok("AT+CGATT=1", 5000);
            } else {
                let _ = mgr.cmd_ok("AT+CGATT=0", 5000);
            }
            Ok(json!({}))
        }

        // ---------------- 连接 / 流量 ----------------
        "GetWANInfo" => {
            let s = mgr.snapshot();
            Ok(json!({
                "ConnectionStatus": if s.pdn_active { 1 } else { 0 },
                "WANIPaddress": s.ipv4,
                "IPv4Adrress": s.ipv4,
                "IPv6Adrress": s.ipv6
            }))
        }
        "GetConnectionInfo" => {
            let s = mgr.snapshot();
            // QCMAP 语义：2=移动数据已开启/已连接，1=已关闭
            Ok(json!({
                "ConnectionStatus": if s.pdn_active { 2 } else { 1 },
                "DlRate": s.rx_rate.saturating_mul(8),
                "UlRate": s.tx_rate.saturating_mul(8),
                "IPv4Adrress": s.ipv4,
                "IPv6Adrress": s.ipv6,
                "DlBytes": s.rx_bytes,
                "UlBytes": s.tx_bytes,
                "ConnectionTime": 0
            }))
        }
        "GetConnectionDevices" => Ok(json!({ "DeviceList": [] })),
        "GetDiffConnectionDevices" => Ok(json!({ "DeviceList": [] })),
        "EditConnectionDevices" | "DeleteConnectionDevices" => Ok(json!({})),
        "GetConnectionSettings" => Ok(json!({ "ConnectMode": 0, "PdpType": 2 })),
        "SetConnectionSettings" => Ok(json!({})),
        "GetProfileList" => {
            let r = mgr.cmd("AT+CGDCONT?", 1200);
            let mut list = Vec::new();
            if r.ok {
                for (cid, apn) in at::parse_pdp(&r.lines) {
                    list.push(json!({
                        "ProfileIndex": cid,
                        "ProfileName": if cid == 1 { "默认配置".to_string() } else { format!("Profile {}", cid) },
                        "APN": apn,
                        "UserName": "",
                        "Password": "",
                        "PdpType": 0,
                        "AuthType": 0,
                        "IsPredefine": cid == 1,
                        "AlwaysSaved": 1
                    }));
                }
            }
            if list.is_empty() {
                list.push(json!({
                    "ProfileIndex": 1,
                    "ProfileName": "默认配置",
                    "APN": "",
                    "UserName": "",
                    "Password": "",
                    "PdpType": 0,
                    "AuthType": 0,
                    "IsPredefine": true,
                    "AlwaysSaved": 1
                }));
            }
            Ok(json!({ "Profile_count": list.len(), "ProfileList": list }))
        }
        "AddProfile" => {
            let cid = i(params, "ProfileIndex");
            let apn = s(params, "APN");
            let pdp = match i(params, "PdpType") {
                1 => "IPV6",
                2 => "IPV4V6",
                _ => "IP",
            };
            if cid <= 0 {
                return Err(RpcError::new("无效的配置索引"));
            }
            mgr.cmd_ok(
                &format!("AT+CGDCONT={},\"{}\",\"{}\"", cid, pdp, apn),
                2000,
            )
            .map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "EditProfile" => {
            let cid = i(params, "ProfileIndex");
            let apn = s(params, "APN");
            let pdp = match i(params, "PdpType") {
                1 => "IPV6",
                2 => "IPV4V6",
                _ => "IP",
            };
            if cid <= 0 {
                return Err(RpcError::new("无效的配置索引"));
            }
            mgr.cmd_ok(
                &format!("AT+CGDCONT={},\"{}\",\"{}\"", cid, pdp, apn),
                2000,
            )
            .map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "DeleteProfile" => {
            let cid = i(params, "ProfileIndex");
            if cid <= 0 {
                return Err(RpcError::new("无效的配置索引"));
            }
            let _ = mgr.cmd_ok(&format!("AT+CGDCONT={}", cid), 2000);
            Ok(json!({}))
        }
        "SetDefaultProfile" => {
            let cid = i(params, "ProfileIndex");
            let _ = mgr.cmd_ok(&format!("AT+CGACT=1,{}", cid), 4000);
            let _ = mgr.cmd_ok(&format!("AT+GTRNDIS=1,{}", cid), 4000);
            Ok(json!({}))
        }

        // ---------------- 短信 ----------------
        "GetSMS" => sms_list(&mgr, params),
        "GetSMSStatisticsInfo" => {
            let r = mgr.cmd("AT+CPMS?", 1200);
            let mut used = 0;
            let mut max = 0;
            if r.ok {
                if let Some(v) = r.after("+CPMS") {
                    let parts: Vec<&str> = v.split(',').collect();
                    if parts.len() >= 3 {
                        used = parts[1].trim().parse().unwrap_or(0);
                        max = parts[2].trim().parse().unwrap_or(0);
                    }
                }
            }
            let unread = sms_unread(&mgr);
            Ok(json!({
                "Unread": unread,
                "Used": used,
                "Max": max
            }))
        }
        "SendSMS" => {
            let numbers_val = params.get("PhoneNumber");
            let mut numbers: Vec<String> = Vec::new();
            match numbers_val {
                Some(Value::Array(a)) => {
                    for v in a {
                        if let Some(x) = v.as_str() {
                            numbers.push(x.to_string());
                        }
                    }
                }
                Some(Value::String(x)) => numbers.push(x.clone()),
                _ => {}
            }
            let content = s(params, "SMSContent");
            if numbers.is_empty() || content.is_empty() {
                return Err(RpcError::new("请输入号码和短信内容"));
            }
            mgr.send_sms(&numbers, &content).map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "DeleteSMS" => {
            let idx = params
                .get("SMSIdArry")
                .and_then(|v| v.as_array())
                .cloned()
                .or_else(|| params.get("SMSId").and_then(|v| v.as_array()).cloned());
            if let Some(arr) = idx {
                for v in arr {
                    let id = v.as_i64().or_else(|| v.as_str().and_then(|x| x.parse().ok())).unwrap_or(0);
                    if id > 0 {
                        let _ = mgr.cmd_ok(&format!("AT+CMGD={}", id), 2000);
                    }
                }
            } else {
                let id = i(params, "SMSId");
                if id > 0 {
                    let _ = mgr.cmd_ok(&format!("AT+CMGD={}", id), 2000);
                } else {
                    let _ = mgr.cmd_ok("AT+CMGD=1,4", 4000);
                }
            }
            Ok(json!({}))
        }
        "ReadSMS" => Ok(json!({})),
        "SaveSMS" | "ImportFromSim" => Ok(json!({})),
        "GetSMSSettings" => {
            let r = mgr.cmd("AT+CSCA?", 1200);
            let center = r
                .after("+CSCA")
                .map(|v| v.split(',').next().unwrap_or("").trim().trim_matches('"').to_string())
                .unwrap_or_default();
            let store = {
                let st = mgr.settings.lock().unwrap().sms_center.clone();
                if st.is_empty() { 0 } else { 1 }
            };
            Ok(json!({ "SMSCenter": center, "StoreFlag": store }))
        }
        "SetSMSSettings" => {
            let center = s(params, "SMSCenter");
            if !center.is_empty() {
                mgr.cmd_ok(&format!("AT+CSCA=\"{}\"", center), 2000)
                    .map_err(RpcError::new)?;
                mgr.settings.lock().unwrap().sms_center = center;
                mgr.settings.lock().unwrap().save();
            }
            Ok(json!({}))
        }
        "GetSMSReportState" => Ok(json!({ "ReportFlag": 0 })),
        "SetSMSReportState" => Ok(json!({})),
        "SetAPSMSFULLSwitch" | "GetAPSMSFULLSwitch" => Ok(json!({ "GetAPSMSFULLSwitch": 0 })),

        // ---------------- 流量统计 ----------------
        "GetUsageRecord" => {
            let s = mgr.snapshot();
            let total = s.rx_bytes.saturating_add(s.tx_bytes);
            let dur = mgr
                .engine
                .lock()
                .unwrap()
                .as_ref()
                .map(|e| e.connected_at.elapsed().as_secs())
                .unwrap_or(0);
            Ok(json!({
                "UsageRecordList": [],
                "Total": total,
                "Used": total,
                "Unit": 0,
                "Duration": dur,
                "clear_time": "",
                "LastClearTime": ""
            }))
        }
        "GetDailyUsageRecord" => {
            let s = mgr.snapshot();
            Ok(json!({ "Total": s.rx_bytes.saturating_add(s.tx_bytes), "Unit": 0 }))
        }
        "GetMonthlyUsageRecord" => {
            let s = mgr.snapshot();
            Ok(json!({ "Total": s.rx_bytes.saturating_add(s.tx_bytes), "Unit": 0 }))
        }
        "GetDailyUsageSettings" => Ok(json!({
            "LimitSwitch": 0,
            "Total": 0,
            "Unit": 0,
            "AutoDisconnect": 0
        })),
        "GetMonthlyUsageSettings" => Ok(json!({
            "LimitSwitch": 0,
            "Total": 0,
            "Unit": 1,
            "AutoDisconnect": 0
        })),
        "SetDailyUsageSettings" | "SetMonthlyUsageSettings" | "SetUsageRecordClear" => {
            Ok(json!({}))
        }

        // ---------------- 系统 ----------------
        "SetDeviceReset" => {
            mgr.cmd("AT+CFUN=1,1", 1500);
            Ok(json!({}))
        }
        "Restart" | "Reboot" => {
            mgr.cmd("AT+CFUN=1,1", 1500);
            Ok(json!({}))
        }
        "GetFactoryResetState" => Ok(json!({ "FactoryResetState": 0 })),
        "FactoryReset" => {
            let _ = mgr.cmd("AT+GTFACTORYRESET=1", 2000);
            Ok(json!({}))
        }
        "GetTimeZone" => {
            let tz = mgr.settings.lock().unwrap().timezone;
            Ok(json!({ "TimeZone": tz }))
        }
        "SetTimeZone" => {
            let tz = i(params, "TimeZone") as i32;
            mgr.settings.lock().unwrap().timezone = tz;
            mgr.settings.lock().unwrap().save();
            Ok(json!({}))
        }
        "GetSNTPInfo" => Ok(json!({ "SNTPState": 0, "SNTPServer": "", "SNTPInterval": 0 })),
        "SetSNTPInfo" => Ok(json!({})),
        "GetLogLvl" => Ok(json!({ "LogLvl": 3 })),
        "SetLogLvl" => Ok(json!({})),
        "GetDeviceNewVersion" | "HostGetDeviceNewVersion" => Ok(json!({
            "NewVersion": 0,
            "CurrentVersion": mgr.snapshot().fw,
            "Version": mgr.snapshot().fw,
            "Description": "",
            "ReleaseNote": ""
        })),
        "GetDownloadState" => Ok(json!({ "State": 0, "Progress": 0 })),
        "GetUpgradeState" => Ok(json!({ "State": 0, "Progress": 0 })),
        "SetDownloadConfig" | "StartDownload" | "StartUpgrade" => {
            Err(RpcError::new("当前版本不支持在线升级，请使用 AT+FWUPD 或厂商工具"))
        }
        "StartDiagnosis" => {
            let host = param(params, &["DiagnosisIP", "IP", "Ip"]);
            if host.is_empty() {
                return Err(RpcError::new("请输入诊断目标地址"));
            }
            let r = mgr.cmd(&format!("AT+GTPING=\"{}\",4", host), 15000);
            Ok(json!({ "DiagnosisResult": if r.ok { 1 } else { 0 }, "Raw": r.raw }))
        }
        "DiagnosisSet" => Ok(json!({})),
        "OneClickCheck" => {
            let s = mgr.snapshot();
            Ok(json!({
                "SIMState": s.sim_state,
                "regState": s.reg_state,
                "ConnectionStatus": if s.pdn_active { 1 } else { 0 },
                "ServiceStatus": s.reg_state == 1 || s.reg_state == 5
            }))
        }

        // ---------------- 以太网 / WLAN / 路由（模组不支持，返回空） ----------------
        "GetEthnetInfo" => Ok(json!({
            "EthernetMode": 2,
            "ConnectionStatus": 0,
            "LinkSpeed": 0,
            "IPv4Adrress": "",
            "IPv6Adrress": ""
        })),
        "GetEthnetStatus" => Ok(json!({ "ConnectionStatus": 0 })),
        "SetEthnetInfo" => Ok(json!({})),
        "GetDHCPSettings" => Ok(json!({})),
        "GetStaticConfig" => Ok(json!({})),
        "SetStaticConfig" => Ok(json!({})),
        "GetWifiState" | "Get6GWifiState" => Ok(json!({
            "W2P4GStatus": 0,
            "W5GStatus": 0,
            "W2P4GSSID": "",
            "W5GSSID": ""
        })),
        "SetWifiState" | "Set6GWifiState" => Ok(json!({})),
        "GetWifiMacFilter" | "GetMacFilterSettings_V1" | "GetMacFilterSettings" => {
            Ok(json!({ "MacFilterList": [] }))
        }
        "GetGuestTimer" => Ok(json!({})),

        // ---------------- 本地工具 ----------------
        "LocalStatus" => {
            let mut v = mgr.status();
            v["settings"] = serde_json::json!({
                "autoConnect": mgr.settings.lock().unwrap().auto_connect,
                "lastConn": mgr.settings.lock().unwrap().last_conn,
            });
            let supported = current_profile(&mgr)
                .map(|p| serde_json::to_value(p).unwrap_or(json!({})))
                .unwrap_or(json!(null));
            v["profileInfo"] = supported;
            v["uptime"] = json!(mgr
                .engine
                .lock()
                .unwrap()
                .as_ref()
                .map(|e| e.connected_at.elapsed().as_secs())
                .unwrap_or(0));
            Ok(v)
        }
        "LocalListPorts" => {
            let ports = crate::manager::list_ports();
            let ports: Vec<Value> = ports
                .into_iter()
                .map(|p| {
                    json!({
                        "port": p.port,
                        "description": p.description,
                        "usb": p.usb,
                        "fibocom": p.fibocom
                    })
                })
                .collect();
            Ok(json!({ "ports": ports }))
        }
        "LocalProbe" => {
            let port = param(params, &["port", "Port"]);
            if port.is_empty() {
                return Err(RpcError::new("请选择串口"));
            }
            match probe_serial(&port) {
                Some(v) => Ok(v),
                None => Err(RpcError::new(format!("{} 无 AT 响应", port))),
            }
        }
        "LocalAutoDetect" => match crate::manager::auto_detect() {
            Some(spec) => Ok(serde_json::to_value(&spec).unwrap_or(json!({}))),
            None => Err(RpcError::new("未发现可用的 Fibocom 串口")),
        },
        "LocalConnect" => {
            let spec: ConnSpec = serde_json::from_value(params.clone())
                .map_err(|e| RpcError::new(format!("连接参数无效: {}", e)))?;
            mgr.connect(spec).map_err(RpcError::new)
        }
        "LocalDisconnect" => {
            mgr.disconnect();
            Ok(json!({}))
        }
        "LocalGetUsbMode" => {
            let mode = mgr.query_usb_mode();
            let info = current_profile(&mgr);
            Ok(json!({
                "mode": mode,
                "profile": info.map(|p| p.key),
                "options": info.map(usb_options).unwrap_or_else(|| json!({}))
            }))
        }
        "LocalSetUsbMode" => {
            let mode = i(params, "mode") as u32;
            if mode == 0 {
                return Err(RpcError::new("无效的 USB 模式"));
            }
            mgr.set_usb_mode(mode).map_err(RpcError::new)?;
            Ok(json!({ "mode": mode }))
        }
        "LocalQuickMode" => {
            let preset = param(params, &["preset"]);
            let apn = s(params, "apn");
            let cid = i(params, "cid").max(1) as u32;
            let reboot = b(params, "reboot");
            mgr.quick_route_mode(&preset, &apn, cid, reboot)
                .map_err(RpcError::new)
        }
        "LocalSetApn" => {
            let apn = s(params, "apn");
            let cid = i(params, "cid").max(1) as u32;
            mgr.set_apn(cid, &apn, "IP").map_err(RpcError::new)?;
            Ok(json!({}))
        }
        "LocalReboot" => {
            let _ = mgr.reboot();
            Ok(json!({}))
        }
        "LocalRefresh" => {
            mgr.refresh_now();
            Ok(json!({}))
        }
        "LocalExit" => {
            Ok(json!({}))
        }

        // 其余功能（WLAN、路由器、VPN、防火墙等）模块不具备，统一返回空对象
        _ => Ok(json!({})),
    }
}

fn current_profile(mgr: &Arc<Manager>) -> Option<&'static Profile> {
    mgr.engine.lock().unwrap().as_ref().and_then(|e| e.profile)
}

fn usb_options(p: &Profile) -> Value {
    json!({
        "rndis_adb": p.rndis_adb,
        "ecm_adb": p.ecm_adb,
        "ncm_adb": p.ncm_adb,
        "mbim_adb": p.mbim_adb,
        "mbim": p.mbim,
        "rndis": p.rndis,
        "family": p.family.label()
    })
}

fn sms_unread(mgr: &Arc<Manager>) -> i64 {
    let r = mgr.cmd("AT+CMGF=1", 1200);
    if !r.ok {
        return 0;
    }
    let r = mgr.cmd("AT+CMGL=\"REC UNREAD\"", 3000);
    r.lines
        .iter()
        .filter(|l| l.trim_start().starts_with("+CMGL:"))
        .count() as i64
}

fn sms_list(mgr: &Arc<Manager>, _params: &Value) -> RpcResult {
    let _ = mgr.cmd_ok("AT+CMGF=1", 2000);
    let r = mgr.cmd("AT+CMGL=\"ALL\"", 5000);
    let mut items: Vec<Value> = Vec::new();
    let mut idx: i64 = 0;
    let mut phone = String::new();
    let mut status = String::new();
    let mut time = String::new();
    let mut body_lines: Vec<String> = Vec::new();

    let flush = |items: &mut Vec<Value>,
                 idx: i64,
                 phone: &str,
                 status: &str,
                 time: &str,
                 body: &[String]| {
        if idx <= 0 {
            return;
        }
        let text = body.join("\n");
        let unread = status.to_ascii_uppercase().contains("UNREAD");
        items.push(json!({
            "SMSId": idx,
            "PhoneNumber": phone,
            "SMSContent": text,
            "SMSTime": time,
            "StorageLocation": if unread { 1 } else { 2 },
            "SMSCount": 1,
            "Unread": unread,
            "SMSStatus": if unread { 0 } else { 1 }
        }));
    };

    for line in &r.lines {
        let t = line.trim();
        if let Some(rest) = t.strip_prefix("+CMGL:") {
            flush(&mut items, idx, &phone, &status, &time, &body_lines);
            body_lines.clear();
            let parts = csv_fields(rest);
            idx = parts
                .first()
                .map(|x| x.trim().parse().unwrap_or(0))
                .unwrap_or(0);
            status = parts.get(1).cloned().unwrap_or_default();
            phone = parts.get(2).cloned().unwrap_or_default();
            let raw_time = parts.get(4).cloned().unwrap_or_default();
            // UI 会自行把 24/05/01,10:00:00+32 格式化为 2024-05-01 10:00:00+32
            time = raw_time;
        } else if idx > 0 && !t.starts_with("+CMGL") {
            body_lines.push(t.to_string());
        }
    }
    flush(&mut items, idx, &phone, &status, &time, &body_lines);

    Ok(json!({
        "SMSList": items,
        "SMSCount": items.len(),
        "SIMState": mgr.snapshot().sim_state
    }))
}

/// 解析 AT 响应中以逗号分隔、字段可能带引号的行（时间字段本身含逗号）
fn csv_fields(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut cur = String::new();
    let mut in_quotes = false;
    for c in s.chars() {
        match c {
            '"' => in_quotes = !in_quotes,
            ',' if !in_quotes => {
                out.push(cur.trim().to_string());
                cur.clear();
            }
            _ => cur.push(c),
        }
    }
    out.push(cur.trim().to_string());
    out
}
