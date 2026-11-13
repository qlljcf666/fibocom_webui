#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod api;
mod at;
mod config;
mod manager;
mod profiles;
mod transport;
mod web;

use api::AppState;
use config::Settings;
use manager::Manager;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

#[tokio::main]
async fn main() {
    let settings = Settings::load();
    let auto = settings.auto_connect;
    let last = settings.last_conn.clone();
    let mgr = Manager::new(settings);

    let state = AppState {
        mgr: mgr.clone(),
        exit: Arc::new(AtomicBool::new(false)),
        heartbeat: Arc::new(Mutex::new(Instant::now())),
        heartbeat_seen: Arc::new(AtomicBool::new(false)),
        started: Instant::now(),
    };

    if auto {
        if let Some(spec) = last {
            let m = mgr.clone();
            tokio::task::spawn_blocking(move || {
                if let Err(e) = m.connect(spec) {
                    eprintln!("自动连接失败: {}", e);
                }
            });
        }
    }

    // 无窗口心跳后自动退出（关闭浏览器窗口即结束后台服务）
    let exit = state.exit.clone();
    let seen = state.heartbeat_seen.clone();
    let hb = state.heartbeat.clone();
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(2)).await;
            if exit.load(Ordering::SeqCst) {
                break;
            }
            if seen.load(Ordering::SeqCst) {
                let last = *hb.lock().unwrap();
                if last.elapsed() > Duration::from_secs(60) {
                    std::process::exit(0);
                }
            }
        }
        std::process::exit(0);
    });

    if let Err(e) = web::run(state).await {
        eprintln!("启动失败: {}", e);
        let _ = std::fs::write("fibocom-manager-error.log", &e);
        std::thread::sleep(Duration::from_secs(3));
    }
}
