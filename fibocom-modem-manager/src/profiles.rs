use serde::Serialize;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub enum Family {
    Unisoc,
    MediaTek,
    Qualcomm,
}

impl Family {
    pub fn label(&self) -> &'static str {
        match self {
            Family::Unisoc => "Unisoc",
            Family::MediaTek => "MediaTek",
            Family::Qualcomm => "Qualcomm",
        }
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct Profile {
    pub key: &'static str,
    pub display: &'static str,
    pub family: Family,
    /// RNDIS + ADB
    pub rndis_adb: Option<u32>,
    /// ECM + ADB
    pub ecm_adb: Option<u32>,
    /// NCM + ADB
    pub ncm_adb: Option<u32>,
    /// MBIM + ADB（不是所有平台都有）
    pub mbim_adb: Option<u32>,
    /// MBIM（无 ADB）
    pub mbim: Option<u32>,
    /// RNDIS（无 ADB）
    pub rndis: Option<u32>,
    /// 是否支持 AT+GTRNDIS
    pub gtrndis: bool,
    /// 是否支持 AT+GTAUTOCONNECT
    pub autoconnect: bool,
    /// 是否支持 AT+GTRAT（高通/紫光），否则使用 AT+GTACT（MTK）
    pub gtrat: bool,
}

pub fn profile_for(model: &str) -> Option<&'static Profile> {
    let m = model.to_ascii_uppercase().replace(' ', "");
    if m.contains("FM652") {
        return Some(&FM652);
    }
    if m.contains("FM650") {
        return Some(&FM650);
    }
    if m.contains("FM350") {
        return Some(&FM350);
    }
    if m.contains("FM150") {
        return Some(&FM150);
    }
    if m.contains("FM160") {
        return Some(&FM160);
    }
    if m.contains("FM170") {
        return Some(&FM170);
    }
    if m.contains("FM190") {
        return Some(&FM190);
    }
    None
}

pub fn is_supported_model(model: &str) -> bool {
    profile_for(model).is_some()
}

pub static FM650: Profile = Profile {
    key: "FM650",
    display: "FM650",
    family: Family::Unisoc,
    rndis_adb: Some(39),
    ecm_adb: Some(35),
    ncm_adb: Some(37),
    mbim_adb: Some(41),
    mbim: Some(40),
    rndis: Some(38),
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};

pub static FM652: Profile = Profile {
    key: "FM652",
    display: "FM652",
    family: Family::Unisoc,
    rndis_adb: Some(39),
    ecm_adb: Some(35),
    ncm_adb: Some(37),
    mbim_adb: Some(41),
    mbim: Some(40),
    rndis: Some(38),
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};

pub static FM350: Profile = Profile {
    key: "FM350",
    display: "FM350",
    family: Family::MediaTek,
    rndis_adb: Some(41),
    ecm_adb: None,
    ncm_adb: None,
    mbim_adb: None,
    mbim: None,
    rndis: Some(40),
    gtrndis: false,
    autoconnect: false,
    gtrat: false,
};

pub static FM150: Profile = Profile {
    key: "FM150AE",
    display: "FM150AE",
    family: Family::Qualcomm,
    rndis_adb: Some(24),
    ecm_adb: Some(18),
    ncm_adb: None,
    mbim_adb: None,
    mbim: Some(29),
    rndis: None,
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};

pub static FM160: Profile = Profile {
    key: "FM160",
    display: "FM160",
    family: Family::Qualcomm,
    rndis_adb: Some(24),
    ecm_adb: Some(18),
    ncm_adb: None,
    mbim_adb: None,
    mbim: Some(29),
    rndis: None,
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};

pub static FM170: Profile = Profile {
    key: "FM170",
    display: "FM170",
    family: Family::Qualcomm,
    rndis_adb: Some(24),
    ecm_adb: Some(18),
    ncm_adb: None,
    mbim_adb: None,
    mbim: Some(29),
    rndis: None,
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};

pub static FM190: Profile = Profile {
    key: "FM190",
    display: "FM190",
    family: Family::Qualcomm,
    rndis_adb: Some(24),
    ecm_adb: Some(18),
    ncm_adb: None,
    mbim_adb: None,
    mbim: Some(29),
    rndis: None,
    gtrndis: true,
    autoconnect: true,
    gtrat: true,
};
