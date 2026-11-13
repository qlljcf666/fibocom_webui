use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type")]
pub enum ConnSpec {
    Serial {
        port: String,
        #[serde(default = "default_baud")]
        baud: u32,
        #[serde(default = "default_parity")]
        parity: String,
        #[serde(default = "default_stop")]
        stop_bits: u8,
        #[serde(default = "default_flow")]
        flow: String,
    },
    Tcp {
        host: String,
        port: u16,
    },
    Udp {
        host: String,
        port: u16,
    },
}

fn default_baud() -> u32 {
    115200
}
fn default_parity() -> String {
    "none".into()
}
fn default_stop() -> u8 {
    1
}
fn default_flow() -> String {
    "none".into()
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Settings {
    #[serde(default)]
    pub last_conn: Option<ConnSpec>,
    #[serde(default)]
    pub auto_connect: bool,
    #[serde(default = "default_password")]
    pub password: String,
    #[serde(default = "default_lang")]
    pub language: String,
    #[serde(default)]
    pub timezone: i32,
    #[serde(default)]
    pub sms_center: String,
}

impl Default for Settings {
    fn default() -> Self {
        Self {
            last_conn: None,
            auto_connect: true,
            password: default_password(),
            language: default_lang(),
            timezone: 8,
            sms_center: String::new(),
        }
    }
}

fn default_password() -> String {
    "admin".into()
}
fn default_lang() -> String {
    "ch".into()
}

impl Settings {
    pub fn path() -> PathBuf {
        let base = std::env::var("APPDATA")
            .ok()
            .map(PathBuf::from)
            .or_else(|| std::env::var("HOME").ok().map(PathBuf::from))
            .unwrap_or_else(|| PathBuf::from("."));
        base.join("FibocomModemManager").join("settings.json")
    }

    pub fn load() -> Settings {
        let p = Self::path();
        std::fs::read_to_string(&p)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default()
    }

    pub fn save(&self) {
        let p = Self::path();
        if let Some(dir) = p.parent() {
            let _ = std::fs::create_dir_all(dir);
        }
        if let Ok(s) = serde_json::to_string_pretty(self) {
            let _ = std::fs::write(p, s);
        }
    }
}
