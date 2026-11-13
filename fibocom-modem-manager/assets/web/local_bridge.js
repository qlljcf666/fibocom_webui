// 注入到原厂 WebUI：心跳 + 本地工具入口
(function () {
  // 已取消登录功能，隐藏顶栏“注销”图标
  try {
    var style = document.createElement("style");
    style.textContent = ".el-icon-switch-button{display:none !important;}";
    document.head.appendChild(style);
  } catch (e) {}

  function heartbeat() {
    fetch("/local/api/heartbeat", { cache: "no-store" }).catch(function () {});
  }
  heartbeat();
  setInterval(heartbeat, 10000);

  function addEntry() {
    if (document.getElementById("fibocom-local-entry")) return;
    var btn = document.createElement("div");
    btn.id = "fibocom-local-entry";
    btn.title = "连接设置 / 快捷工具";
    btn.style.cssText = [
      "position:fixed",
      "right:22px",
      "bottom:22px",
      "z-index:9999",
      "display:flex",
      "align-items:center",
      "gap:6px",
      "padding:10px 16px",
      "border-radius:24px",
      "background:linear-gradient(135deg,#409eff,#2383e6)",
      "color:#fff",
      "font-size:13px",
      "font-family:Arial,Microsoft YaHei,sans-serif",
      "box-shadow:0 6px 18px rgba(35,131,230,.35)",
      "cursor:pointer",
      "user-select:none"
    ].join(";");
    btn.innerHTML = '<span style="font-size:15px">&#9881;</span><span>连接与工具</span>';
    btn.onclick = function () {
      window.open(
        "/local/",
        "fibocomLocalTools",
        "width=1080,height=820,menubar=no,toolbar=no,location=no,status=no"
      );
    };
    document.body.appendChild(btn);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", addEntry);
  } else {
    addEntry();
  }
})();
