use crate::api::{self, AppState};
use axum::body::Body;
use axum::extract::State;
use axum::http::{header, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::Router;
use include_dir::{include_dir, Dir};
use serde_json::{json, Value};
use std::sync::atomic::Ordering;
use std::time::Instant;

static WEB: Dir<'_> = include_dir!("$CARGO_MANIFEST_DIR/assets/web");
static LOCAL: Dir<'_> = include_dir!("$CARGO_MANIFEST_DIR/assets/local");

pub fn router(state: AppState) -> Router {
    Router::new()
        .route("/fibo/webapi", post(webapi))
        .route("/fibo/fibologin/", get(login).post(login))
        .route("/local/api/heartbeat", get(heartbeat))
        .route("/local/", get(local_index))
        .route("/local", get(local_index))
        .fallback(get(static_handler))
        .with_state(state)
}

async fn webapi(State(st): State<AppState>, body: String) -> Response {
    let req: Value = match serde_json::from_str(&body) {
        Ok(v) => v,
        Err(_) => {
            return JsonRpc::err(Value::Null, "请求格式错误".into()).into_response();
        }
    };
    let id = req.get("id").cloned().unwrap_or(Value::Null);
    let method = req
        .get("method")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();
    let params = req.get("params").cloned().unwrap_or(json!({}));

    if method == "LocalExit" {
        st.exit.store(true, Ordering::SeqCst);
        return JsonRpc::ok(id, json!({})).into_response();
    }

    let mgr = st.mgr.clone();
    let m = method.clone();
    let p = params.clone();
    let result = tokio::task::spawn_blocking(move || api::handle(mgr, &m, &p))
        .await
        .unwrap_or_else(|e| Err(api::RpcError::new(format!("内部错误: {}", e))));
    // 心跳：任何来自 UI 的请求都视为窗口存活
    *st.heartbeat.lock().unwrap() = Instant::now();

    match result {
        Ok(v) => JsonRpc::ok(id, v).into_response(),
        Err(e) => JsonRpc::err(id, e.message()).into_response(),
    }
}

struct JsonRpc;

impl JsonRpc {
    fn ok(id: Value, result: Value) -> Response {
        let body = json!({ "jsonrpc": "2.0", "id": id, "result": result });
        json_response(body)
    }

    fn err(id: Value, message: String) -> Response {
        let body = json!({
            "jsonrpc": "2.0",
            "id": id,
            "error": { "code": 1, "message": message }
        });
        json_response(body)
    }
}

fn json_response(body: Value) -> Response {
    let mut res = Response::new(Body::from(body.to_string()));
    res.headers_mut()
        .insert(header::CONTENT_TYPE, HeaderValue::from_static("application/json; charset=utf-8"));
    res.headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
    res
}

async fn login() -> Response {
    // 部分 UI 版本会调用 /fibo/fibologin/，返回本机地址即可
    let body = json!({
        "code": 0,
        "result": { "ip": "127.0.0.1", "loginState": 0 }
    });
    json_response(body)
}

async fn heartbeat(State(st): State<AppState>) -> Response {
    *st.heartbeat.lock().unwrap() = Instant::now();
    st.heartbeat_seen.store(true, Ordering::SeqCst);
    let elapsed = st.started.elapsed().as_secs();
    json_response(json!({ "ok": true, "uptime": elapsed }))
}

async fn local_index() -> Response {
    serve_dir(&LOCAL, "index.html")
}

async fn static_handler(
    uri: axum::http::Uri,
    State(_st): State<AppState>,
) -> Response {
    let path = percent_encoding::percent_decode_str(uri.path())
        .decode_utf8_lossy()
        .to_string();
    let path = path.trim_start_matches('/');
    if path.contains("..") {
        return StatusCode::NOT_FOUND.into_response();
    }
    if path.is_empty() {
        return serve_dir(&WEB, "index.html");
    }
    if path.starts_with("local/") {
        return serve_dir(&LOCAL, path.trim_start_matches("local/"));
    }
    if let Some(f) = WEB.get_file(path) {
        let mut res = Response::new(Body::from(f.contents().to_vec()));
        let mime = mime_for(path);
        res.headers_mut()
            .insert(header::CONTENT_TYPE, HeaderValue::from_static(mime));
        res.headers_mut()
            .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
        return res;
    }
    // SPA 路由回退
    let has_ext = path.rsplit('/').next().map(|s| s.contains('.')).unwrap_or(false);
    if !has_ext {
        return serve_dir(&WEB, "index.html");
    }
    StatusCode::NOT_FOUND.into_response()
}

fn serve_dir(dir: &Dir<'static>, path: &str) -> Response {
    match dir.get_file(path.trim_start_matches('/')) {
        Some(f) => {
            let mut res = Response::new(Body::from(f.contents().to_vec()));
            res.headers_mut()
                .insert(header::CONTENT_TYPE, HeaderValue::from_static(mime_for(path)));
            res.headers_mut()
                .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
            res
        }
        None => StatusCode::NOT_FOUND.into_response(),
    }
}

fn mime_for(path: &str) -> &'static str {
    let p = path.to_ascii_lowercase();
    if p.ends_with(".html") {
        "text/html; charset=utf-8"
    } else if p.ends_with(".js") {
        "application/javascript; charset=utf-8"
    } else if p.ends_with(".css") {
        "text/css; charset=utf-8"
    } else if p.ends_with(".json") {
        "application/json; charset=utf-8"
    } else if p.ends_with(".png") {
        "image/png"
    } else if p.ends_with(".jpg") || p.ends_with(".jpeg") {
        "image/jpeg"
    } else if p.ends_with(".gif") {
        "image/gif"
    } else if p.ends_with(".svg") {
        "image/svg+xml"
    } else if p.ends_with(".ico") {
        "image/x-icon"
    } else if p.ends_with(".woff2") {
        "font/woff2"
    } else if p.ends_with(".woff") {
        "font/woff"
    } else if p.ends_with(".ttf") {
        "font/ttf"
    } else if p.ends_with(".eot") {
        "application/vnd.ms-fontobject"
    } else {
        "application/octet-stream"
    }
}

pub async fn run(state: AppState) -> Result<(), String> {
    let app = router(state);
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .map_err(|e| format!("监听端口失败: {}", e))?;
    let port = listener.local_addr().map(|a| a.port()).unwrap_or(0);
    println!("Fibocom 模组管理工具已启动: http://127.0.0.1:{}", port);
    open_window(port);
    axum::serve(listener, app)
        .await
        .map_err(|e| format!("服务运行失败: {}", e))
}

fn open_window(port: u16) {
    if std::env::var("FIBOCOM_NO_WINDOW").is_ok() {
        return;
    }
    let url = format!("http://127.0.0.1:{}/", port);
    let edge = format!("start msedge --app={} --window-size=1360,860", url);
    let _ = std::process::Command::new("cmd")
        .arg("/C")
        .arg(edge)
        .spawn();
    // 若 Edge 启动失败，2 秒后使用默认浏览器
    let url2 = url.clone();
    std::thread::spawn(move || {
        std::thread::sleep(std::time::Duration::from_secs(3));
        if !msedge_running() {
            let _ = std::process::Command::new("cmd")
                .arg("/C")
                .arg(format!("start {}", url2))
                .spawn();
        }
    });
}

fn msedge_running() -> bool {
    let out = std::process::Command::new("tasklist")
        .arg("/FI")
        .arg("IMAGENAME eq msedge.exe")
        .output();
    match out {
        Ok(o) => String::from_utf8_lossy(&o.stdout).contains("msedge.exe"),
        Err(_) => false,
    }
}
