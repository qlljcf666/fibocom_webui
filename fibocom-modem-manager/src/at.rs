use crate::transport::Transport;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Default)]
pub struct AtResponse {
    pub lines: Vec<String>,
    pub ok: bool,
    pub raw: String,
}

impl AtResponse {
    pub fn first(&self) -> Option<&str> {
        self.lines
            .iter()
            .find(|l| !l.trim().is_empty())
            .map(|s| s.as_str())
    }

    /// 返回形如 `+CPIN: READY` 中冒号后的内容
    pub fn after(&self, prefix: &str) -> Option<String> {
        for l in &self.lines {
            let t = l.trim();
            if let Some(rest) = t.strip_prefix(prefix) {
                return Some(rest.trim().trim_start_matches(':').trim().to_string());
            }
        }
        None
    }
}

fn process_line(
    line: &str,
    lines: &mut Vec<String>,
    raw: &str,
    cmd: &str,
) -> Option<AtResponse> {
    let t = line.trim().trim_start_matches('\u{feff}');
    if t.is_empty() || t.eq_ignore_ascii_case(cmd.trim()) {
        return None;
    }
    if t == "OK" {
        return Some(AtResponse {
            lines: lines.clone(),
            ok: true,
            raw: raw.to_string(),
        });
    }
    if t == "ERROR" || t.starts_with("+CME ERROR") || t.starts_with("+CMS ERROR") {
        return Some(AtResponse {
            lines: lines.clone(),
            ok: false,
            raw: raw.to_string(),
        });
    }
    lines.push(t.to_string());
    None
}

pub fn exchange(t: &mut Transport, cmd: &str, timeout: Duration) -> AtResponse {
    t.drain();
    let payload = format!("{}\r\n", cmd);
    if t.write_all(payload.as_bytes()).is_err() {
        return AtResponse::default();
    }
    let _ = t.flush();

    let deadline = Instant::now() + timeout;
    let mut pending = String::new();
    let mut lines: Vec<String> = Vec::new();
    let mut raw = String::new();
    let mut buf = [0u8; 4096];

    while Instant::now() < deadline {
        match t.read(&mut buf) {
            Ok(0) => {
                std::thread::sleep(Duration::from_millis(15));
            }
            Ok(n) => {
                let chunk = String::from_utf8_lossy(&buf[..n]).to_string();
                raw.push_str(&chunk);
                pending.push_str(&chunk);
                while let Some(pos) = pending.find('\n') {
                    let line = pending[..pos].trim_end_matches('\r').to_string();
                    pending = pending[pos + 1..].to_string();
                    if let Some(mut r) = process_line(&line, &mut lines, &raw, cmd) {
                        r.raw = raw.clone();
                        return r;
                    }
                }
            }
            Err(_) => break,
        }
    }
    if !pending.trim().is_empty() {
        let _ = process_line(&pending, &mut lines, &raw, cmd);
    }
    AtResponse {
        lines,
        ok: false,
        raw,
    }
}

pub fn send_sms(t: &mut Transport, number: &str, body: &str, timeout: Duration) -> AtResponse {
    t.drain();
    if t.write_all(format!("AT+CMGS=\"{}\"\r\n", number).as_bytes()).is_err() {
        return AtResponse::default();
    }
    let deadline = Instant::now() + timeout;
    let mut buf = [0u8; 1024];
    let mut prompt_seen = false;
    let mut raw = String::new();
    while Instant::now() < deadline {
        match t.read(&mut buf) {
            Ok(0) => std::thread::sleep(Duration::from_millis(20)),
            Ok(n) => {
                let chunk = String::from_utf8_lossy(&buf[..n]).to_string();
                raw.push_str(&chunk);
                if chunk.contains('>') {
                    prompt_seen = true;
                    break;
                }
                if chunk.contains("ERROR") {
                    return AtResponse {
                        lines: vec![],
                        ok: false,
                        raw,
                    };
                }
            }
            Err(_) => break,
        }
    }
    if !prompt_seen {
        return AtResponse {
            lines: vec![],
            ok: false,
            raw,
        };
    }
    let _ = t.write_all(format!("{}\u{1a}", body).as_bytes());
    let _ = t.flush();
    let deadline = Instant::now() + timeout;
    let mut pending = String::new();
    let mut lines = Vec::new();
    let mut raw2 = String::new();
    while Instant::now() < deadline {
        match t.read(&mut buf) {
            Ok(0) => std::thread::sleep(Duration::from_millis(20)),
            Ok(n) => {
                let chunk = String::from_utf8_lossy(&buf[..n]).to_string();
                raw2.push_str(&chunk);
                pending.push_str(&chunk);
                while let Some(pos) = pending.find('\n') {
                    let line = pending[..pos].trim_end_matches('\r').to_string();
                    pending = pending[pos + 1..].to_string();
                    if let Some(mut r) = process_line(&line, &mut lines, &raw2, "") {
                        r.raw = raw2.clone();
                        return r;
                    }
                }
            }
            Err(_) => break,
        }
    }
    AtResponse {
        lines,
        ok: false,
        raw: raw2,
    }
}

/// 解析 `+CGDCONT: 1,"IP","apn",...` 之类的列表，返回 (cid, apn)
pub fn parse_pdp(lines: &[String]) -> Vec<(i32, String)> {
    let mut out = Vec::new();
    for l in lines {
        let t = l.trim();
        if let Some(rest) = t.strip_prefix("+CGDCONT:") {
            let parts: Vec<&str> = rest.split(',').collect();
            if parts.len() >= 3 {
                if let (Ok(cid), Some(apn)) = (
                    parts[0].trim().parse::<i32>(),
                    parts.get(2).map(|s| s.trim().trim_matches('"').to_string()),
                ) {
                    out.push((cid, apn));
                }
            }
        }
    }
    out
}

pub fn parse_cops(lines: &[String]) -> (String, i32) {
    for l in lines {
        let t = l.trim();
        if let Some(rest) = t.strip_prefix("+COPS:") {
            let parts: Vec<&str> = rest.split(',').collect();
            let mut name = String::new();
            let mut act = 0;
            for p in &parts {
                let trim = p.trim();
                if trim.starts_with('"') {
                    name = trim.trim_matches('"').to_string();
                }
            }
            if let Some(last) = parts.last() {
                // 最后一个字段通常是 AcT（后面可能跟 URC 数据）
                let clean: String = last
                    .trim()
                    .chars()
                    .take_while(|c| c.is_ascii_digit())
                    .collect();
                act = clean.parse().unwrap_or(0);
            }
            return (name, act);
        }
    }
    (String::new(), 0)
}

pub fn parse_csq(lines: &[String]) -> i32 {
    for l in lines {
        let t = l.trim();
        if let Some(rest) = t.strip_prefix("+CSQ:") {
            return rest
                .split(',')
                .next()
                .unwrap_or("99")
                .trim()
                .parse()
                .unwrap_or(99);
        }
    }
    99
}

pub fn parse_reg(lines: &[String], prefix: &str) -> Option<i32> {
    for l in lines {
        let t = l.trim();
        if let Some(rest) = t.strip_prefix(prefix) {
            let parts: Vec<&str> = rest.split(',').collect();
            if let Some(stat) = parts.get(1) {
                return stat.trim().parse().ok();
            }
        }
    }
    None
}

/// AcT -> WebUI RAT 编号 (2G=1, 3G=4, 4G=6, 5G=8)
pub fn act_to_ui_rat(act: i32) -> i32 {
    match act {
        0 | 3 => 1,
        1 | 2 | 4 | 5 | 6 => 4,
        7 => 6,
        10 | 11 | 12 | 13 | 14 => 8,
        _ => 0,
    }
}

pub fn ui_rat_name(rat: i32) -> &'static str {
    match rat {
        1 => "2G",
        4 => "3G",
        6 => "4G",
        8 => "5G",
        _ => "",
    }
}

/// 将 `+GTCCINFO:` 服务小区数据粗略解析成 (cell_id, rsrp, rsrq, sinr, band)
pub fn parse_cell(lines: &[String]) -> Option<(i64, i32, i32, i32, String)> {
    for l in lines {
        let t = l.trim();
        if let Some(rest) = t.strip_prefix("+GTCCINFO:") {
            let f: Vec<&str> = rest.split(',').map(|s| s.trim()).collect();
            if f.len() < 8 {
                continue;
            }
            let is_serving = f[0].parse::<i32>().unwrap_or(0);
            if is_serving != 1 {
                continue;
            }
            let rat = f[1].parse::<i32>().unwrap_or(0);
            let (cell_idx, rsrp_idx, rsrq_idx, sinr_idx, band_idx) = match rat {
                4 | 5 | 6 => (5, 12, 13, 10, 8),
                9 => (5, 12, 13, 10, 8),
                2 => (5, 10, 9, 9, 8),
                _ => (5, 12, 13, 10, 8),
            };
            let cell = f.get(cell_idx).and_then(|s| s.parse().ok()).unwrap_or(0);
            let rsrp = f.get(rsrp_idx).and_then(|s| s.parse().ok()).unwrap_or(0);
            let rsrq = f.get(rsrq_idx).and_then(|s| s.parse().ok()).unwrap_or(0);
            let sinr = f.get(sinr_idx).and_then(|s| s.parse().ok()).unwrap_or(0);
            let band = f.get(band_idx).map(|s| s.to_string()).unwrap_or_default();
            return Some((cell, rsrp, rsrq, sinr, band));
        }
    }
    None
}
