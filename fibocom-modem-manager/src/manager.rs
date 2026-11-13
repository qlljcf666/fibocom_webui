use crate::at::{self, AtResponse};
use crate::config::{ConnSpec, Settings};
use crate::profiles::{self, Profile};
use crate::transport::{self, Transport};
use serde::Serialize;
use serde_json::{json, Value};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

#[derive(Clone, Debug, Default, Serialize)]
pub struct Snap {
    pub manufacturer: String,
    pub model: String,
    pub fw: String,
    pub imei: String,
    pub iccid: String,
    pub imsi: String,
    pub msisdn: String,
    pub sim_state: i32,
    pub pin_state: i32,
    pub pin_remain: i32,
    pub puk_remain: i32,
    pub csq: i32,
    pub signal_level: i32,
    pub reg_state: i32,
    pub operator: String,
    pub rat: i32,
    pub act: i32,
    pub roaming: i32,
    pub pdn_active: bool,
    pub ipv4: String,
    pub ipv6: String,
    pub rx_rate: u64,
    pub tx_rate: u64,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub cell_id: i64,
    pub rsrp: i32,
    pub rsrq: i32,
    pub sinr: i32,
    pub band: String,
    pub usb_mode: Option<u32>,
}

pub struct Engine {
    pub transport: Transport,
    pub profile: Option<&'static Profile>,
    pub desc: String,
    pub connected_at: Instant,
}

#[derive(Clone, Serialize)]
pub struct PortInfo {
    pub port: String,
    pub description: String,
    pub usb: bool,
    pub fibocom: bool,
}

pub struct Manager {
    pub engine: Mutex<Option<Engine>>,
    pub snap: Mutex<Snap>,
    pub settings: Mutex<Settings>,
    pub last_error: Mutex<String>,
}

impl Manager {
    pub fn new(settings: Settings) -> Arc<Self> {
        let mgr = Arc::new(Self {
            engine: Mutex::new(None),
            snap: Mutex::new(Snap::default()),
            settings: Mutex::new(settings),
            last_error: Mutex::new(String::new()),
        });
        let mgr2 = mgr.clone();
        std::thread::spawn(move || mgr2.poll_loop());
        mgr
    }

    fn poll_loop(&self) {
        loop {
            std::thread::sleep(Duration::from_secs(4));
            let mut guard = self.engine.lock().unwrap();
            if let Some(e) = guard.as_mut() {
                self.refresh_engine(e);
            }
        }
    }

    pub fn is_connected(&self) -> bool {
        self.engine.lock().unwrap().is_some()
    }

    pub fn status(&self) -> Value {
        let connected = self.is_connected();
        let snap = self.snap.lock().unwrap().clone();
        let profile = {
            let g = self.engine.lock().unwrap();
            g.as_ref().and_then(|e| e.profile).map(|p| p.key.to_string())
        };
        json!({
            "connected": connected,
            "model": snap.model,
            "profile": profile,
            "imei": snap.imei,
            "fw": snap.fw,
            "manufacturer": snap.manufacturer,
            "iccid": snap.iccid,
            "simState": snap.sim_state,
            "operator": snap.operator,
            "rat": at::ui_rat_name(snap.rat),
            "signal": snap.signal_level,
            "ipv4": snap.ipv4,
            "usbMode": snap.usb_mode,
            "lastError": self.last_error.lock().unwrap().clone(),
        })
    }

    pub fn connect(&self, spec: ConnSpec) -> Result<Value, String> {
        // 先断开已有连接
        self.disconnect();

        let (mut transport, desc) = match &spec {
            ConnSpec::Serial {
                port,
                baud,
                parity,
                stop_bits,
                flow,
            } => {
                let t = transport::open_serial(port, *baud, parity, *stop_bits, flow)?;
                (t, format!("{} @ {}", port, baud))
            }
            ConnSpec::Tcp { host, port } => {
                let t = transport::open_tcp(host, *port)?;
                (t, format!("TCP {}:{}", host, port))
            }
            ConnSpec::Udp { host, port } => {
                let t = transport::open_udp(host, *port)?;
                (t, format!("UDP {}:{}", host, port))
            }
        };

        // 唤醒：部分模组需要先收到一次任意字符
        let mut ok = false;
        for _ in 0..3 {
            let r = at::exchange(&mut transport, "AT", Duration::from_millis(1200));
            if r.ok {
                ok = true;
                break;
            }
        }
        if !ok {
            return Err(format!(
                "{} 无 AT 响应，请检查端口/波特率或 AT over Ethernet 地址",
                desc
            ));
        }
        let _ = at::exchange(&mut transport, "ATE0", Duration::from_millis(800));

        let cmi = at::exchange(&mut transport, "AT+CGMI", Duration::from_millis(1000));
        let cgmm = at::exchange(&mut transport, "AT+CGMM", Duration::from_millis(1000));
        let fw = at::exchange(&mut transport, "AT+CGMR", Duration::from_millis(1000));
        let model = cgmm
            .first()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "Fibocom".into());
        let profile = profiles::profile_for(&model);

        let mut snap = self.snap.lock().unwrap();
        *snap = Snap::default();
        snap.manufacturer = cmi
            .first()
            .map(|s| s.trim().to_string())
            .unwrap_or_else(|| "Fibocom".into());
        snap.model = model.clone();
        snap.fw = fw
            .first()
            .map(|s| s.trim().to_string())
            .unwrap_or_default();

        let engine = Engine {
            transport,
            profile,
            desc: desc.clone(),
            connected_at: Instant::now(),
        };
        drop(snap);
        *self.engine.lock().unwrap() = Some(engine);
        *self.last_error.lock().unwrap() = String::new();

        {
            let mut s = self.settings.lock().unwrap();
            s.last_conn = Some(spec);
            s.save();
        }

        // 首次采集
        {
            let mut guard = self.engine.lock().unwrap();
            if let Some(e) = guard.as_mut() {
                self.refresh_engine(e);
            }
        }

        Ok(json!({
            "model": model,
            "profile": profile.map(|p| p.key),
            "supported": profile.is_some(),
            "transport": desc,
        }))
    }

    pub fn disconnect(&self) {
        let mut g = self.engine.lock().unwrap();
        if let Some(e) = g.take() {
            *self.last_error.lock().unwrap() = format!("已断开 {}", e.desc);
        }
    }

    pub fn cmd(&self, command: &str, timeout_ms: u64) -> AtResponse {
        let mut g = self.engine.lock().unwrap();
        match g.as_mut() {
            Some(e) => at::exchange(&mut e.transport, command, Duration::from_millis(timeout_ms)),
            None => AtResponse {
                lines: vec![],
                ok: false,
                raw: "未连接模组".into(),
            },
        }
    }

    pub fn cmd_ok(&self, command: &str, timeout_ms: u64) -> Result<AtResponse, String> {
        let r = self.cmd(command, timeout_ms);
        if r.ok {
            Ok(r)
        } else {
            Err(extract_error(&r))
        }
    }

    pub fn snapshot(&self) -> Snap {
        self.snap.lock().unwrap().clone()
    }

    pub fn refresh_now(&self) {
        let mut g = self.engine.lock().unwrap();
        if let Some(e) = g.as_mut() {
            self.refresh_engine(e);
        }
    }

    fn refresh_engine(&self, e: &mut Engine) {
        let mut s = self.snap.lock().unwrap();
        let t = &mut e.transport;

        let r = at::exchange(t, "AT+CGSN", Duration::from_millis(900));
        if r.ok {
            if let Some(l) = r.first() {
                let v: String = l.chars().filter(|c| c.is_ascii_digit()).collect();
                if v.len() >= 15 {
                    s.imei = v;
                }
            }
        }

        let r = at::exchange(t, "AT+CPIN?", Duration::from_millis(900));
        let cpin = r.after("+CPIN").unwrap_or_default();
        let upper = cpin.to_ascii_uppercase();
        if upper.starts_with("READY") {
            s.sim_state = 7;
            s.pin_state = 2;
        } else if upper.contains("SIM PIN") {
            s.sim_state = 7;
            s.pin_state = 1;
        } else if upper.contains("SIM PUK") {
            s.sim_state = 7;
            s.pin_state = 4;
        } else {
            s.sim_state = 0;
            s.pin_state = 0;
        }

        if s.sim_state == 7 && s.pin_state == 2 {
            let r = at::exchange(t, "AT+CLCK=\"SC\",2", Duration::from_millis(900));
            if r.ok {
                if let Some(v) = r.after("+CLCK") {
                    s.pin_state = if v.trim().starts_with('1') { 2 } else { 3 };
                }
            }
        }

        let r = at::exchange(t, "AT+CPINR=\"SIM PIN\"", Duration::from_millis(900));
        if r.ok {
            if let Some(v) = r.after("+CPINR") {
                if let Some(n) = v.rsplit(',').next() {
                    s.pin_remain = n.trim().parse().unwrap_or(3);
                }
            }
        } else {
            s.pin_remain = 3;
        }

        if s.iccid.is_empty() {
            let mut r = at::exchange(t, "AT+ICCID", Duration::from_millis(900));
            if !r.ok || r.lines.is_empty() {
                r = at::exchange(t, "AT+CCID", Duration::from_millis(900));
            }
            if r.ok {
                let val = r
                    .after("+ICCID")
                    .or_else(|| r.after("+CCID"))
                    .or_else(|| r.first().map(|s| s.to_string()))
                    .unwrap_or_default();
                let v: String = val.chars().filter(|c| c.is_ascii_digit()).collect();
                if v.len() >= 8 {
                    s.iccid = v;
                }
            }
        }

        if s.imsi.is_empty() {
            let r = at::exchange(t, "AT+CIMI", Duration::from_millis(900));
            if r.ok {
                if let Some(l) = r.first() {
                    let v: String = l.chars().filter(|c| c.is_ascii_digit()).collect();
                    if v.len() >= 10 {
                        s.imsi = v;
                    }
                }
            }
        }

        if s.msisdn.is_empty() {
            let r = at::exchange(t, "AT+CNUM", Duration::from_millis(900));
            if r.ok {
                if let Some(v) = r.after("+CNUM") {
                    if let Some(q) = v.split('"').nth(1) {
                        if !q.trim().is_empty() {
                            s.msisdn = q.trim().to_string();
                        }
                    }
                }
            }
        }

        let r = at::exchange(t, "AT+CSQ", Duration::from_millis(900));
        if r.ok {
            s.csq = at::parse_csq(&r.lines);
            s.signal_level = if s.csq == 99 || s.csq <= 0 {
                0
            } else {
                ((s.csq / 5).clamp(0, 4)) as i32
            };
        }

        let r = at::exchange(t, "AT+COPS?", Duration::from_millis(1200));
        if r.ok {
            let (name, act) = at::parse_cops(&r.lines);
            if !name.is_empty() {
                s.operator = name;
            }
            if act > 0 {
                s.act = act;
                s.rat = at::act_to_ui_rat(act);
            }
        }

        let mut reg = None;
        for prefix in ["+CEREG", "+CGREG", "+CREG"] {
            let r = at::exchange(t, &format!("AT{}?", prefix), Duration::from_millis(900));
            if r.ok {
                if let Some(st) = at::parse_reg(&r.lines, prefix) {
                    reg = Some(st);
                    break;
                }
            }
        }
        if let Some(st) = reg {
            s.reg_state = st;
            s.roaming = if st == 5 { 1 } else { 0 };
        }

        let r = at::exchange(t, "AT+CGACT?", Duration::from_millis(900));
        if r.ok {
            s.pdn_active = r.lines.iter().any(|l| {
                l.trim()
                    .strip_prefix("+CGACT:")
                    .map(|rest| rest.split(',').nth(1).map(|x| x.trim() == "1").unwrap_or(false))
                    .unwrap_or(false)
            });
        }

        let r = at::exchange(t, "AT+CGCONTRDP=1", Duration::from_millis(1200));
        if r.ok {
            if let Some(v) = r.after("+CGCONTRDP") {
                for part in v.split(',') {
                    let q = part.trim().trim_matches('"');
                    if q.contains('.') && q.chars().filter(|c| *c == '.').count() == 3 {
                        if !q.starts_with("0.0.0.0") && !q.is_empty() && s.ipv4.is_empty() {
                            s.ipv4 = q.to_string();
                        }
                    } else if q.contains(':') && q.len() > 6 && s.ipv6.is_empty() {
                        if s.ipv6.is_empty() {
                            s.ipv6 = q.to_string();
                        }
                    }
                }
            }
        }
        if s.ipv4.is_empty() {
            let r = at::exchange(t, "AT+CGPADDR=1", Duration::from_millis(900));
            if r.ok {
                if let Some(v) = r.after("+CGPADDR") {
                    for part in v.split(',') {
                        let q = part.trim().trim_matches('"');
                        if q.contains('.') {
                            s.ipv4 = q.to_string();
                        }
                    }
                }
            }
        }

        let is_mediatek = e
            .profile
            .map(|p| p.family == profiles::Family::MediaTek)
            .unwrap_or(false);
        if !is_mediatek {
            let r = at::exchange(t, "AT+GTSTATIS?", Duration::from_millis(1000));
            if r.ok {
                if let Some(v) = r.after("+GTSTATIS") {
                    let f: Vec<&str> = v.split(',').map(|x| x.trim()).collect();
                    if f.len() >= 4 {
                        s.rx_rate = f[0].parse().unwrap_or(0);
                        s.tx_rate = f[1].parse().unwrap_or(0);
                        s.rx_bytes = f[2].parse().unwrap_or(0);
                        s.tx_bytes = f[3].parse().unwrap_or(0);
                    }
                }
            }
        }

        let r = at::exchange(t, "AT+GTCCINFO?", Duration::from_millis(1200));
        if r.ok {
            if let Some((cell, rsrp, rsrq, sinr, band)) = at::parse_cell(&r.lines) {
                s.cell_id = cell;
                s.rsrp = rsrp;
                s.rsrq = rsrq;
                s.sinr = sinr;
                s.band = band;
            }
        }
    }

    pub fn send_sms(&self, numbers: &[String], body: &str) -> Result<(), String> {
        self.cmd_ok("AT+CMGF=1", 2000)?;
        let mut guard = self.engine.lock().unwrap();
        let e = guard.as_mut().ok_or_else(|| "未连接模组".to_string())?;
        for n in numbers {
            let r = at::send_sms(&mut e.transport, n, body, Duration::from_secs(20));
            if !r.ok {
                return Err(extract_error(&r));
            }
        }
        Ok(())
    }

    pub fn query_usb_mode(&self) -> Option<u32> {
        let r = self.cmd("AT+GTUSBMODE?", 1000);
        if r.ok {
            if let Some(v) = r.after("+GTUSBMODE") {
                let n: String = v.chars().take_while(|c| c.is_ascii_digit()).collect();
                if let Ok(m) = n.parse() {
                    self.snap.lock().unwrap().usb_mode = Some(m);
                    return Some(m);
                }
            }
        }
        None
    }

    pub fn set_usb_mode(&self, mode: u32) -> Result<(), String> {
        self.cmd_ok(&format!("AT+GTUSBMODE={}", mode), 2000)?;
        self.snap.lock().unwrap().usb_mode = Some(mode);
        Ok(())
    }

    pub fn set_apn(&self, cid: u32, apn: &str, pdp: &str) -> Result<(), String> {
        self.cmd_ok(
            &format!("AT+CGDCONT={},\"{}\",\"{}\"", cid, pdp, apn),
            2000,
        )?;
        Ok(())
    }

    pub fn activate_pdp(&self, cid: u32) -> Result<(), String> {
        self.cmd_ok(&format!("AT+CGACT=1,{}", cid), 4000)?;
        Ok(())
    }

    pub fn enable_rndis(&self, cid: u32) -> Result<(), String> {
        self.cmd_ok(&format!("AT+GTRNDIS=1,{}", cid), 4000)?;
        Ok(())
    }

    pub fn set_autoconnect(&self) -> Result<(), String> {
        self.cmd_ok("AT+GTAUTOCONNECT=1", 2000)?;
        Ok(())
    }

    pub fn reboot(&self) -> Result<(), String> {
        let _ = self.cmd("AT+CFUN=1,1", 1500);
        Ok(())
    }

    pub fn quick_route_mode(
        &self,
        preset: &str,
        apn: &str,
        cid: u32,
        do_reboot: bool,
    ) -> Result<Value, String> {
        let profile = {
            let g = self.engine.lock().unwrap();
            match g.as_ref().and_then(|e| e.profile) {
                Some(p) => p,
                None => return Err("当前模组不在支持列表（FM650/FM652/FM350/FM150AE/FM160/FM170/FM190）".into()),
            }
        };
        let mode = match preset {
            "rndis_adb" => profile.rndis_adb,
            "ecm_adb" => profile.ecm_adb,
            "ncm_adb" => profile.ncm_adb,
            "mbim_adb" => profile.mbim_adb,
            "mbim" => profile.mbim,
            "rndis" => profile.rndis,
            _ => return Err("未知模式".into()),
        }
        .ok_or_else(|| format!("{} 不支持该 USB/网卡模式", profile.display))?;

        let mut steps: Vec<String> = Vec::new();
        if !apn.trim().is_empty() {
            self.set_apn(cid, apn.trim(), "IP")?;
            steps.push(format!("AT+CGDCONT={},\"IP\",\"{}\" OK", cid, apn.trim()));
        }
        self.set_usb_mode(mode)?;
        steps.push(format!("AT+GTUSBMODE={} OK", mode));

        if profile.gtrndis && (preset.starts_with("rndis") || preset == "ecm_adb") {
            let _ = self.activate_pdp(cid);
            if preset.starts_with("rndis") {
                if self.enable_rndis(cid).is_ok() {
                    steps.push(format!("AT+GTRNDIS=1,{} OK", cid));
                }
            }
            if profile.autoconnect && self.set_autoconnect().is_ok() {
                steps.push("AT+GTAUTOCONNECT=1 OK".into());
            }
        }

        if do_reboot {
            let _ = self.reboot();
            steps.push("AT+CFUN=1,1 重启中".into());
        }
        self.snap.lock().unwrap().usb_mode = Some(mode);

        Ok(json!({
            "mode": mode,
            "preset": preset,
            "steps": steps,
            "reboot": do_reboot,
        }))
    }
}

fn extract_error(r: &AtResponse) -> String {
    for l in &r.lines {
        let t = l.trim();
        if t.starts_with("+CME ERROR") || t.starts_with("+CMS ERROR") || t == "ERROR" {
            return t.to_string();
        }
    }
    if r.raw.trim().is_empty() {
        "模组无响应（超时）".into()
    } else {
        let raw = r.raw.replace(['\r', '\n'], " ");
        format!("执行失败: {}", raw.chars().take(160).collect::<String>())
    }
}

pub fn probe_serial(port: &str) -> Option<Value> {
    for baud in [115200u32, 9600u32] {
        let mut t = match transport::open_serial(port, baud, "none", 1, "none") {
            Ok(t) => t,
            Err(_) => return None,
        };
        let mut alive = false;
        for _ in 0..3 {
            if at::exchange(&mut t, "AT", Duration::from_millis(900)).ok {
                alive = true;
                break;
            }
        }
        if !alive {
            continue;
        }
        let _ = at::exchange(&mut t, "ATE0", Duration::from_millis(500));
        let cmi = at::exchange(&mut t, "AT+CGMI", Duration::from_millis(800));
        let cgmm = at::exchange(&mut t, "AT+CGMM", Duration::from_millis(800));
        let model = cgmm
            .first()
            .map(|s| s.trim().to_string())
            .unwrap_or_default();
        let manufacturer = cmi
            .first()
            .map(|s| s.trim().to_string())
            .unwrap_or_default();
        return Some(json!({
            "port": port,
            "baud": baud,
            "model": model,
            "manufacturer": manufacturer,
            "fibocom": manufacturer.to_ascii_uppercase().contains("FIBOCOM")
                || profiles::is_supported_model(&model)
                || !profiles::profile_for(&model).is_none(),
        }));
    }
    None
}

pub fn list_ports() -> Vec<PortInfo> {
    transport::list_ports()
        .into_iter()
        .map(|p| {
            let desc = match &p.port_type {
                serialport::SerialPortType::UsbPort(info) => {
                    let mut d = String::new();
                    if let Some(m) = &info.manufacturer {
                        d.push_str(m);
                    }
                    if let Some(prod) = &info.product {
                        if !d.is_empty() {
                            d.push(' ');
                        }
                        d.push_str(prod);
                    }
                    if d.is_empty() {
                        format!("USB 串口 (VID:{:04X} PID:{:04X})", info.vid, info.pid)
                    } else {
                        d
                    }
                }
                serialport::SerialPortType::BluetoothPort => "蓝牙串口".into(),
                serialport::SerialPortType::PciPort => "PCI 串口".into(),
                serialport::SerialPortType::Unknown => "串口".into(),
            };
            let usb = matches!(p.port_type, serialport::SerialPortType::UsbPort(_));
            let fibocom = desc.to_ascii_uppercase().contains("FIBOCOM");
            PortInfo {
                port: p.port_name,
                description: desc,
                usb,
                fibocom,
            }
        })
        .collect()
}

pub fn auto_detect() -> Option<ConnSpec> {
    let ports = list_ports();
    let mut candidates: Vec<_> = ports.iter().filter(|p| p.usb).cloned().collect();
    if candidates.is_empty() {
        candidates = ports;
    }
    for p in candidates {
        if let Some(info) = probe_serial(&p.port) {
            let baud = info.get("baud").and_then(|v| v.as_u64()).unwrap_or(115200) as u32;
            return Some(ConnSpec::Serial {
                port: p.port,
                baud,
                parity: "none".into(),
                stop_bits: 1,
                flow: "none".into(),
            });
        }
    }
    None
}
