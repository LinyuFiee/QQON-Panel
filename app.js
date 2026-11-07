(() => {
  "use strict";

  const API = window.PanelAPI;
  const main = document.getElementById("main-content");
  const gate = document.getElementById("auth-gate");
  const gateContent = document.getElementById("gate-content");
  const app = document.getElementById("panel-app");
  const dialog = document.getElementById("detail-dialog");
  const sidebar = document.getElementById("sidebar");
  const overlay = document.getElementById("sidebar-overlay");
  const menuButton = document.getElementById("menu-toggle");
  const mobileQuery = window.matchMedia("(max-width: 760px)");

  const pageNames = {
    overview: "概览",
    apps: "软件",
    websites: "网站",
    jobs: "任务",
    files: "文件",
    services: "服务",
    sources: "应用与源",
    docker: "Docker",
    updates: "面板更新",
    account: "个人与安全",
  };
  const dockerTabs = { containers: "容器", images: "镜像", volumes: "卷", networks: "网络" };
  const actionNames = { start: "启动", stop: "停止", restart: "重启" };
  const taskNames = { queued: "排队中", running: "执行中", succeeded: "已完成", failed: "失败", interrupted: "已中断" };
  const siteTypes = { static: "静态网站", proxy: "反向代理", php: "PHP 网站" };
  const appIDs = ["nginx", "php-fpm", "mariadb", "redis"];
  const pendingJob = (job) => ["queued", "running"].includes(job?.status);
  const validSiteID = (id) => typeof id === "string" && /^[a-f0-9]{32}$/.test(id);
  const utf8Bytes = (value) => new TextEncoder().encode(value).length;
  const dailySentences = [
    "先记录事实，再判断原因。",
    "把复杂的问题拆成可以验证的小步骤。",
    "重要的改变，值得留下一份记录。",
    "看见未知，是理解的开始。",
    "读懂一条错误，比忽略十条错误更有用。",
    "在改变之前，先确认当前的状态。",
    "留出思考的时间，也是一种进展。",
    "清楚的边界，让协作更简单。",
    "耐心观察，答案常常在细节里。",
    "好的问题，往往比匆忙的答案更重要。",
    "每一次验证，都让判断更可靠。",
    "不确定时，先求证。",
  ];

  const state = {
    authenticated: false,
    initialized: null,
    user: null,
    page: "overview",
    route: null,
    authController: null,
    authChanging: false,
    authLost: false,
    recoveryVisible: false,
    pollTimer: null,
    modal: null,
    jobSelection: "",
    dockerTab: "containers",
    dockerStatus: null,
    accountData: null,
    accountDirty: false,
    totpPending: false,
    sourceRevision: null,
    sourceDirty: false,
    files: { path: "", offset: 0, previous: [], query: "", data: null },
  };
  const htmlCache = new WeakMap();
  let toastTimer;

  // HTML templates contain only local markup; every API string goes through h().
  const h = (value) => String(value ?? "").replace(/[&<>"']/g, (character) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[character]);
  const icon = (name) => `<svg class="icon" aria-hidden="true"><use href="#icon-${name}"/></svg>`;
  const number = (value) => typeof value === "number" && Number.isFinite(value);
  const text = (value, fallback = "未提供") => typeof value === "string" && value.length ? value : fallback;
  const list = (value, limit) => Array.isArray(value) ? value.slice(0, limit) : [];
  const aborted = (error) => error?.name === "AbortError";
  const current = (context) => Boolean(context && state.authenticated && state.route === context && !context.signal.aborted);
  const invalid = (message = "后端响应格式不符合接口契约。") => new API.Error(message, "invalid_response");

  function requireList(payload, key, limit) {
    if (!Array.isArray(payload?.[key])) throw invalid();
    return payload[key].slice(0, limit);
  }

  function requireOK(payload) {
    if (payload?.ok !== true) throw invalid("后端没有确认操作成功，请重新读取状态后再操作。");
    return payload;
  }

  function formatBytes(value) {
    if (!number(value) || value < 0) return "不可用";
    if (value === 0) return "0 B";
    const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
    const index = Math.max(0, Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1));
    return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
  }

  function formatPercent(value) {
    return number(value) && value >= 0 && value <= 100 ? `${value.toFixed(1)}%` : "不可用";
  }

  function formatRate(value) {
    return number(value) && value >= 0 ? `${formatBytes(value)}/s` : "不可用";
  }

  function formatDate(value) {
    if (typeof value !== "string" || !value) return "未提供";
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "时间未知" : date.toLocaleString("zh-CN", { hour12: false });
  }

  function formatDuration(value) {
    if (!number(value) || value < 0) return "不可用";
    const seconds = Math.floor(value);
    if (seconds < 60) return `${seconds} 秒`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes} 分钟`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours} 小时 ${minutes % 60} 分钟`;
    return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
  }

  function dailySentence() {
    const now = new Date();
    const localDay = Math.floor(Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()) / 86400000);
    return dailySentences[((localDay % dailySentences.length) + dailySentences.length) % dailySentences.length];
  }

  function setHTML(target, markup) {
    const node = typeof target === "string" ? document.getElementById(target) : target;
    if (!node) return;
    // A form's live input values can differ from its original HTML. Explicit reads
    // must rebuild those forms, even when the server returned identical markup.
    const rebuild = node === main || node === gateContent || ["account-main", "sources-content", "totp-section"].includes(node.id);
    if (!rebuild && htmlCache.get(node) === markup) return;
    const focusedKey = node.contains(document.activeElement) ? document.activeElement?.dataset.focusKey : null;
    node.innerHTML = markup;
    htmlCache.set(node, markup);
    if (focusedKey) {
      Array.from(node.querySelectorAll("[data-focus-key]")).find((item) => item.dataset.focusKey === focusedKey)?.focus({ preventScroll: true });
    }
  }

  function notice(message, kind = "info") {
    const style = ["info", "warning", "success", "error"].includes(kind) ? kind : "info";
    return `<div class="notice ${style}"${style === "error" ? ' role="alert"' : ""}>${icon("info")}<span>${h(message)}</span></div>`;
  }

  function errorMarkup(error) {
    const message = error instanceof API.Error ? error.message : "处理响应时发生错误，请重新读取并检查后端接口。";
    const code = error instanceof API.Error ? error.code : "client_error";
    return `<div class="notice error" role="alert">${icon("info")}<div><strong>${h(message)}</strong><small>错误代码：${h(code)}${error?.status ? ` · HTTP ${h(error.status)}` : ""}</small></div></div>`;
  }

  function empty(message) {
    return `<div class="empty-state">${h(message)}</div>`;
  }

  function loading(message = "正在读取…") {
    return `<div class="loading-state" role="status">${h(message)}</div>`;
  }

  function pageHeading(title, description = "", actions = "") {
    return `<div class="page-heading"><div><h1>${h(title)}</h1>${description ? `<p id="page-description">${h(description)}</p>` : ""}</div>${actions ? `<div class="page-actions">${actions}</div>` : ""}</div>`;
  }

  function refreshButton(label = "刷新") {
    return `<button class="button" type="button" data-action="refresh">${icon("refresh")}${h(label)}</button>`;
  }

  function facts(rows) {
    return `<dl class="system-facts">${rows.map(([label, value]) => `<div class="fact-row"><dt>${h(label)}</dt><dd>${h(value)}</dd></div>`).join("")}</dl>`;
  }

  function table(headers, rows, label, emptyMessage = "没有记录。") {
    return `<div class="table-scroll"><table class="data-table" aria-label="${h(label)}"><thead><tr>${headers.map((header) => `<th scope="col">${h(header)}</th>`).join("")}</tr></thead><tbody>${rows || `<tr><td colspan="${headers.length}" class="table-empty">${h(emptyMessage)}</td></tr>`}</tbody></table></div>`;
  }

  function badge(label, tone = "neutral") {
    const safeTone = ["neutral", "success", "warning", "error"].includes(tone) ? tone : "neutral";
    return `<span class="status-pill ${safeTone}">${h(label)}</span>`;
  }

  function serviceBadge(value) {
    const known = { active: ["运行中", "success"], inactive: ["未运行", "neutral"], failed: ["失败", "error"], unknown: ["未知", "warning"] };
    return Object.hasOwn(known, value) ? badge(...known[value]) : badge(`未知${value ? `（${value}）` : ""}`, "warning");
  }

  function containerBadge(value) {
    const known = { running: ["运行中", "success"], exited: ["已退出", "neutral"], created: ["已创建", "neutral"], paused: ["已暂停", "warning"], restarting: ["重启中", "warning"], removing: ["移除中", "warning"], dead: ["异常", "error"] };
    return Object.hasOwn(known, value) ? badge(...known[value]) : badge(`未知${value ? `（${value}）` : ""}`, "warning");
  }

  function showToast(message) {
    clearTimeout(toastTimer);
    const toast = document.getElementById("toast");
    toast.textContent = message;
    toast.hidden = false;
    toastTimer = setTimeout(() => { toast.hidden = true; }, 6000);
  }

  // One route owns its reads and one auto-refresh timer. Navigation cancels both.
  function stopRoute() {
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
    if (state.route) {
      state.route.controller.abort();
      state.route.fileController?.abort();
      state.route.jobDetailController?.abort();
    }
    state.route = null;
    closeDialog(true);
  }

  function updateRefreshLabel() {
    const context = state.route;
    let label = "此页面仅按需读取";
    if (context?.interval) {
      label = document.hidden ? "页面不可见，自动刷新已暂停" : dialog.open ? "对话框打开时暂停自动刷新" : `每 ${context.interval / 1000} 秒刷新 · 切页或隐藏时暂停`;
    }
    document.getElementById("refresh-status").textContent = label;
  }

  function schedulePoll(context) {
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
    updateRefreshLabel();
    if (!current(context) || !context.interval || document.hidden || dialog.open || context.refreshing) return;
    state.pollTimer = setTimeout(() => {
      state.pollTimer = null;
      void refreshRoute(context, true);
    }, context.interval);
  }

  async function refreshRoute(context, automatic = false) {
    if (!current(context) || context.refreshing || !context.refresh || (automatic && (document.hidden || dialog.open))) return;
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
    context.refreshing = true;
    context.automatic = automatic;
    const controller = new AbortController();
    context.readController = controller;
    const abort = () => controller.abort();
    context.signal.addEventListener("abort", abort, { once: true });
    main.querySelectorAll('[data-action="refresh"]').forEach((button) => { button.disabled = true; });
    main.setAttribute("aria-busy", "true");
    try {
      await context.refresh(controller.signal, automatic);
    } catch (error) {
      if (!aborted(error) && current(context)) setHTML("route-message", errorMarkup(error));
    } finally {
      context.signal.removeEventListener("abort", abort);
      context.refreshing = false;
      context.readController = null;
      if (current(context)) {
        main.removeAttribute("aria-busy");
        main.querySelectorAll('[data-action="refresh"]').forEach((button) => { button.disabled = false; });
        if (context.refreshRequested) {
          context.refreshRequested = false;
          if (!document.hidden && !dialog.open) void refreshRoute(context);
          else { context.interval ||= 5000; schedulePoll(context); }
        } else schedulePoll(context);
      }
    }
  }

  async function renderSection(context, target, request, renderer) {
    try {
      const payload = await request();
      if (current(context)) setHTML(target, renderer(payload));
    } catch (error) {
      if (!aborted(error) && current(context)) setHTML(target, errorMarkup(error));
    }
  }

  function renderPage(moveFocus = false) {
    if (!state.authenticated || state.recoveryVisible) return;
    stopRoute();
    const requested = location.hash.slice(1);
    state.page = Object.hasOwn(pageNames, requested) ? requested : "overview";
    const controller = new AbortController();
    const context = { controller, signal: controller.signal, interval: 0, refreshing: false, refresh: null };
    state.route = context;
    state.accountDirty = false;
    state.sourceDirty = false;
    state.totpPending = false;
    state.files.data = null;
    state.dockerStatus = null;
    const renderers = {
      overview: renderOverviewPage,
      apps: renderAppsPage,
      websites: renderWebsitesPage,
      jobs: renderJobsPage,
      files: renderFilesPage,
      services: renderServicesPage,
      sources: renderSourcesPage,
      docker: renderDockerPage,
      updates: renderUpdatesPage,
      account: renderAccountPage,
    };
    renderers[state.page](context);
    document.title = `服务器面板 · ${pageNames[state.page]}`;
    document.getElementById("breadcrumb-current").textContent = pageNames[state.page];
    document.querySelectorAll("[data-page]").forEach((link) => {
      const active = link.dataset.page === state.page;
      link.classList.toggle("active", active);
      if (active) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
    closeSidebar(false);
    updateRefreshLabel();
    if (moveFocus) {
      main.focus({ preventScroll: true });
      window.scrollTo({ top: 0, behavior: "instant" });
    }
    void refreshRoute(context);
  }

  // Authentication gates never display server statistics or invented login results.
  function leavePanel() {
    stopRoute();
    state.authenticated = false;
    state.user = null;
    state.accountData = null;
    state.sourceRevision = null;
    state.sourceDirty = false;
    state.accountDirty = false;
    state.jobSelection = "";
    state.files = { path: "", offset: 0, previous: [], query: "", data: null };
    API.clearSession();
    app.hidden = true;
    gate.hidden = false;
    main.replaceChildren();
    closeSidebar(false);
    clearTimeout(toastTimer);
    document.getElementById("toast").hidden = true;
  }

  function validateStatus(status) {
    if (typeof status?.initialized !== "boolean" || typeof status?.authenticated !== "boolean") throw invalid();
    if (status.authenticated && (!status.initialized || typeof status.user?.username !== "string" || typeof status.user?.twoFactorEnabled !== "boolean")) throw invalid();
  }

  function enterPanel(status) {
    validateStatus(status);
    if (!status.authenticated) throw invalid("后端尚未确认登录成功。");
    API.setSession(status);
    state.authenticated = true;
    state.initialized = true;
    state.user = status.user;
    state.authLost = false;
    state.recoveryVisible = false;
    gate.hidden = true;
    gateContent.replaceChildren();
    app.hidden = false;
    document.getElementById("account-username").textContent = status.user.username;
    document.getElementById("header-username").textContent = status.user.username;
    document.getElementById("connection-label").textContent = "已登录";
    renderPage(true);
  }

  function renderAuthForm(mode, message = "") {
    const setup = mode === "setup";
    document.title = `服务器面板 · ${setup ? "初始化" : "登录"}`;
    setHTML(gateContent, `<h1 id="gate-title">${setup ? "初始化面板" : "登录"}</h1>
      <p class="auth-description">${setup ? "创建唯一管理员。初始化令牌保存在后端启动时输出路径对应的文件中。" : "使用管理员账户登录此面板。"}</p>
      ${message ? notice(message, "warning") : ""}
      <form data-form="${setup ? "setup" : "login"}" class="form-stack">
        ${setup ? '<label class="field"><span>初始化令牌</span><input name="token" type="password" required maxlength="512" autocomplete="off" spellcheck="false" /></label>' : ""}
        <label class="field"><span>用户名</span><input name="username" type="text" required minlength="3" maxlength="32" autocomplete="username" autocapitalize="none" spellcheck="false" />${setup ? '<small>3–32 位 ASCII 字符，允许的具体字符由后端校验。</small>' : ""}</label>
        <label class="field"><span>密码</span><input name="password" type="password" required autocomplete="${setup ? "new-password" : "current-password"}" />${setup ? '<small>12–128 个 UTF-8 字节，不截断密码。</small>' : ""}</label>
        ${setup ? '<label class="field"><span>确认密码</span><input name="confirmPassword" type="password" required autocomplete="new-password" /></label>' : '<label class="field"><span>验证码或一次性恢复码 <small>启用双重验证时填写</small></span><input name="code" type="text" maxlength="128" autocomplete="one-time-code" autocapitalize="none" spellcheck="false" /></label>'}
        <div data-form-error></div>
        <button class="button button-primary" type="submit">${setup ? "创建管理员" : "登录"}</button>
      </form>
      <button class="text-link auth-recheck" type="button" data-action="retry-auth">重新检查后端与登录状态</button>`);
  }

  function forceLogin(message = "登录已失效，请重新登录。") {
    if (state.recoveryVisible) return;
    state.authController?.abort();
    state.authController = null;
    leavePanel();
    state.initialized = true;
    renderAuthForm("login", message);
    gateContent.focus({ preventScroll: true });
  }

  async function checkAuth() {
    if (state.recoveryVisible || state.authChanging) return;
    state.authController?.abort();
    leavePanel();
    const controller = new AbortController();
    state.authController = controller;
    document.title = "服务器面板 · 连接后端";
    setHTML(gateContent, `<h1 id="gate-title">连接面板后端</h1>${loading("正在检查初始化与登录状态…")}`);
    try {
      const status = await API.authStatus(controller.signal);
      if (controller.signal.aborted || state.authController !== controller) return;
      validateStatus(status);
      state.initialized = status.initialized;
      if (status.authenticated) enterPanel(status);
      else renderAuthForm(status.initialized ? "login" : "setup");
    } catch (error) {
      if (aborted(error) || state.authController !== controller) return;
      document.title = "服务器面板 · 后端不可用";
      setHTML(gateContent, `<h1 id="gate-title">无法读取后端状态</h1>${errorMarkup(error)}
        <p class="muted">没有连接后端时，初始化、登录和服务器数据均不可用。</p>
        <button class="button button-primary" type="button" data-action="retry-auth">重新连接</button>`);
    }
  }

  function validatePassword(password, confirmation) {
    const bytes = new TextEncoder().encode(password).length;
    if (bytes < 12 || bytes > 128) throw new API.Error("密码需要 12–128 个 UTF-8 字节。", "invalid_password_length");
    if (password !== confirmation) throw new API.Error("两次输入的密码不一致。", "password_mismatch");
  }

  async function securityChange(work) {
    state.authChanging = true;
    state.authLost = false;
    try {
      return await work();
    } finally {
      state.authChanging = false;
      if (state.authLost && state.authenticated && !state.recoveryVisible) forceLogin();
    }
  }

  // Overview: only the server's finite sample history is plotted; null remains unavailable.
  function renderOverviewPage(context) {
    setHTML(main, `${pageHeading("每日一言", dailySentence(), refreshButton())}<div id="route-message"></div>
      <div id="management-status"></div><div id="overview-system">${loading("正在读取系统采样…")}</div>
      <section class="panel section-card"><h2>功能可用性</h2><div id="overview-capabilities">${loading()}</div></section>`);
    context.interval = 5000;
    context.refresh = async (signal, automatic) => {
      document.getElementById("page-description").textContent = dailySentence();
      const requests = [renderSection(context, "overview-system", () => API.system(signal), renderSystem)];
      if (!automatic || !context.capabilitiesLoaded) {
        requests.push(loadManagement(context, signal));
        requests.push(renderSection(context, "overview-capabilities", () => API.capabilities(signal), (data) => {
          const content = renderCapabilities(data);
          context.capabilitiesLoaded = true;
          return content;
        }));
      }
      await Promise.all(requests);
    };
  }

  function metric(label, value, description, iconName) {
    return `<article class="panel metric-card"><div class="metric-top"><span>${h(label)}</span>${icon(iconName)}</div><strong class="metric-number">${h(value)}</strong><p>${h(description)}</p></article>`;
  }

  function renderSystem(data) {
    if (typeof data.available !== "boolean" || !data.panel || !Array.isArray(data.history)) throw invalid();
    const memory = data.memory ?? {};
    const disk = data.disk ?? {};
    const network = data.network ?? {};
    return `${data.available ? "" : notice(text(data.reason, "当前平台不支持主机资源采样；仅显示后端实际提供的信息。"), "warning")}
      <section class="panel host-summary">
        <div><h2>${h(text(data.hostname, "主机信息"))}</h2><p>${h(text(data.os))} · ${h(text(data.architecture))} · ${h(text(data.platform))}</p></div>
        <div class="host-sample"><span>系统运行 ${h(formatDuration(data.uptimeSeconds))}</span><small>采样时间 ${h(formatDate(data.sampledAt))}</small></div>
      </section>
      <section class="metrics-grid" aria-label="实际资源指标">
        ${metric("CPU 使用率", formatPercent(data.cpuPercent), "不可用的数据不会填充为零", "cpu")}
        ${metric("内存使用率", formatPercent(memory.percent), `${formatBytes(memory.used)} / ${formatBytes(memory.total)}`, "memory")}
        ${metric("磁盘使用率", formatPercent(disk.percent), `${formatBytes(disk.used)} / ${formatBytes(disk.total)}`, "disk")}
        ${metric("网络接收", formatRate(network.rxBytesPerSecond), `发送 ${formatRate(network.txBytesPerSecond)}`, "network")}
      </section>
      <div class="dashboard-grid">
        <section class="panel section-card"><h2>资源趋势</h2><p class="panel-subtitle">后端最近最多 300 条真实采样</p>${renderHistory(data.history)}</section>
        <section class="panel section-card"><h2>面板进程</h2>${facts([
          ["Go 堆内存", formatBytes(data.panel.heapBytes)],
          ["进程驻留内存（RSS）", formatBytes(data.panel.rssBytes)],
          ["Goroutine 数量", number(data.panel.goroutines) ? data.panel.goroutines : "不可用"],
          ["面板运行时间", formatDuration(data.panel.uptimeSeconds)],
        ])}<p class="panel-subtitle">进程内存与主机总内存是不同指标。</p></section>
      </div>`;
  }

  function renderHistory(history) {
    const points = list(history, 300).filter((point) => point && typeof point.time === "string" && Number.isFinite(new Date(point.time).getTime()));
    if (!points.length) return empty("尚无可用的历史采样。");
    points.sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime());
    const firstTime = new Date(points[0].time).getTime();
    const lastTime = new Date(points[points.length - 1].time).getTime();
    const validValue = (value) => number(value) && value >= 0 && value <= 100;
    const pathFor = (key) => {
      let connected = false;
      return points.map((point) => {
        if (!validValue(point[key])) { connected = false; return ""; }
        const x = lastTime === firstTime ? 290 : 40 + ((new Date(point.time).getTime() - firstTime) / (lastTime - firstTime)) * 520;
        const y = 160 - point[key] * 1.4;
        const command = `${connected ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
        connected = true;
        return command;
      }).join(" ");
    };
    const cpuCount = points.filter((point) => validValue(point.cpuPercent)).length;
    const memoryCount = points.filter((point) => validValue(point.memoryPercent)).length;
    if (cpuCount === 0 && memoryCount === 0) return empty("当前环境没有可用的 CPU 或内存采样，暂不绘制趋势。");
    if (cpuCount < 2 && memoryCount < 2) return empty("有效采样不足两条，等待下一次采样。");
    return `<div class="chart-legend"><span><i class="legend-dot"></i>CPU</span><span><i class="legend-dot blue"></i>内存</span><span>${points.length} 条采样</span></div>
      <svg class="resource-chart" viewBox="0 0 580 180" role="img" aria-label="后端记录的 CPU 与内存使用率，缺失采样不连线">
        ${[0, 25, 50, 75, 100].map((value) => `<line class="chart-grid" x1="40" x2="560" y1="${160 - value * 1.4}" y2="${160 - value * 1.4}"/><text x="2" y="${164 - value * 1.4}">${value}%</text>`).join("")}
        ${memoryCount >= 2 ? `<path class="chart-line memory-line" d="${pathFor("memoryPercent")}"/>` : ""}
        ${cpuCount >= 2 ? `<path class="chart-line cpu-line" d="${pathFor("cpuPercent")}"/>` : ""}
      </svg><div class="chart-times"><span>${h(formatDate(points[0].time))}</span><span>${h(formatDate(points[points.length - 1].time))}</span></div>`;
  }

  function renderCapabilities(data) {
    if (!data.metrics || !data.files || !data.services || !data.docker) throw invalid();
    const items = [
      ["主机指标", data.metrics.available, data.metrics.reason],
      ["文件浏览", data.files.available, data.files.reason],
      ["服务读取", data.services.available, data.services.reason],
      ["Docker 接口配置", data.docker.configured, data.docker.reason],
    ];
    return `<div class="capability-grid">${items.map(([name, available, reason]) => `<div><strong>${h(name)}</strong>${badge(available === true ? "已提供" : available === false ? "不可用" : "未知", available === true ? "success" : "neutral")}${reason ? `<p>${h(reason)}</p>` : ""}</div>`).join("")}</div>`;
  }

  // Hosting privileges are reported by the server, never granted by a web control.
  function renderManagement(data) {
    if (!["restricted", "system"].includes(data?.mode) || typeof data.authorized !== "boolean" || typeof data.identity !== "string" || typeof data.platform !== "string") throw invalid("最高权限状态响应不符合契约；管理操作保持禁用。");
    if (data.authorized && (data.mode !== "system" || !["root", "LocalSystem"].includes(data.identity))) throw invalid("最高权限状态不一致；管理操作保持禁用。");
    return `<section class="notice ${data.authorized ? "success" : "warning"} management-notice">${icon("shield")}<div><strong>${data.authorized ? "系统最高权限已授权" : "未获得系统最高权限授权"}</strong><small>模式 ${h(data.mode)} · 身份 ${h(text(data.identity))} · 平台 ${h(text(data.platform))}${data.acceptedAt ? ` · 授权记录 ${h(formatDate(data.acceptedAt))}` : ""}</small>
      <span>${h(data.authorized ? "管理任务以系统最高权限执行，可修改软件、服务和站点配置。每项变更都需要单独确认。" : text(data.reason, "当前仅允许受限操作。实际身份与主机本地授权记录必须同时有效；root 身份本身不代表已同意。"))}</span>
      ${data.authorized && data.reason ? `<small>${h(data.reason)}</small>` : ""}<small>网页不能授予最高权限；需要主机管理员通过本地安装器明确授权。</small></div></section>
      ${data.platform.toLowerCase() === "windows" ? notice("Windows 暂不支持软件安装和网站管理（unsupported）。LocalSystem 授权不等于这些 Linux 功能可用。", "warning") : ""}`;
  }

  async function loadManagement(context, signal) {
    context.management = null;
    try {
      const data = await API.management(signal);
      if (!current(context) || signal.aborted) return;
      const markup = renderManagement(data);
      context.management = data;
      setHTML("management-status", markup);
    } catch (error) {
      if (!aborted(error) && current(context)) setHTML("management-status", errorMarkup(error));
    }
  }

  function hostingWritable(context, data) {
    return current(context) && context.management?.authorized === true && context.management.platform === "linux" && data?.available === true;
  }

  function requireJob(job) {
    if (!job || typeof job.id !== "string" || !job.id || job.id.length > 128 || !Object.hasOwn(taskNames, job.status) || !Array.isArray(job.logs)) throw invalid("后端未返回有效任务。请先读取任务列表确认结果，不要直接重复提交。");
    return job;
  }

  function jobBadge(job) {
    return badge(taskNames[job.status], job.status === "succeeded" ? "success" : ["failed", "interrupted"].includes(job.status) ? "error" : "warning");
  }

  function jobProgress(job) {
    const valid = number(job.progress) && job.progress >= 0 && job.progress <= 100;
    return `<div class="job-progress"><progress max="100" ${valid ? `value="${job.progress}"` : ""} aria-label="${h(text(job.stage, "任务进度"))}"></progress><span>${valid ? `${h(job.progress)}%` : "未知"}</span></div><small>${h(text(job.stage, "等待后端报告阶段"))}</small>`;
  }

  function renderJobRows(jobs, compact = false) {
    return table(["任务 / 目标", "状态", "真实进度", "结果 / 时间", "详情"], jobs.map((job) => `<tr><td class="wrap-cell"><strong>${h(text(job.action))}</strong><small class="mono">${h(text(job.target))}</small><small class="mono">${h(job.id)}</small></td>
      <td>${jobBadge(job)}</td><td>${jobProgress(job)}</td><td class="wrap-cell">${h(text(job.error, text(job.message, pendingJob(job) ? "尚未完成" : "后端未提供结果说明")))}<small>${h(formatDate(job.finishedAt || job.createdAt))}</small></td>
      <td><button class="button button-small" type="button" data-action="show-job" data-job-id="${h(job.id)}" data-focus-key="job-${h(job.id)}">${compact ? "查看" : "进度与日志"}</button></td></tr>`).join(""), compact ? "近期管理任务" : "软件与网站任务", "没有保留的任务。不会显示模拟进度。");
  }

  function renderJobSummary(jobs) {
    const selected = [...jobs.filter(pendingJob), ...jobs.filter((job) => !pendingJob(job))].slice(0, 6);
    return `<section class="panel hosting-task-panel"><div class="section-toolbar"><div><h2>近期管理任务</h2><p class="panel-subtitle">受理不等于成功；完成后重新读取当前页面。</p></div><a class="text-link" href="#jobs">全部任务与日志</a></div>${renderJobRows(selected, true)}</section>`;
  }

  async function loadHostingJobs(context, signal, full = false) {
    try {
      const data = await API.jobs(signal);
      if (!current(context) || signal.aborted) return false;
      const jobs = requireList(data, "jobs", 32).map(requireJob);
      const completed = jobs.some((job) => pendingJob(context.jobStates?.get(job.id)) && !pendingJob(job));
      context.jobs = jobs;
      context.jobStates = new Map(jobs.map((job) => [job.id, { status: job.status }]));
      context.interval = jobs.some(pendingJob) ? 5000 : 0;
      setHTML(full ? "jobs-content" : "hosting-jobs", full ? renderJobRows(jobs) : renderJobSummary(jobs));
      return completed;
    } catch (error) {
      if (!aborted(error) && current(context)) setHTML(full ? "jobs-content" : "hosting-jobs", errorMarkup(error));
      return false;
    }
  }

  async function acceptedHostingJob(context, result) {
    const job = requireJob(result?.job);
    if (!current(context)) return;
    context.jobStates ??= new Map();
    context.jobStates.set(job.id, { status: job.status });
    context.reloadResources = true;
    context.interval = pendingJob(job) ? 5000 : context.interval;
    closeDialog(true);
    setHTML("route-message", notice(`任务已受理：${job.id}（${taskNames[job.status]}）。提交请求不代表执行成功，请查看最终状态和日志。`));
    showToast("后台任务已受理，请等待实际结果；关闭页面不会取消已受理的任务。");
    if (context.refreshing) context.refreshRequested = true;
    else await refreshRoute(context);
  }

  function renderAppsPage(context) {
    setHTML(main, `${pageHeading("软件", "管理主机上的 Nginx、PHP-FPM、MariaDB 和 Redis，不是安装面板自身。", refreshButton())}<div id="route-message"></div><div id="management-status">${loading("正在读取最高权限状态…")}</div>
      <div id="apps-content">${loading("正在读取已安装的软件…")}</div><div id="hosting-jobs"></div>`);
    context.refresh = async (signal, automatic) => {
      const completed = await loadHostingJobs(context, signal);
      if (!current(context) || signal.aborted) return;
      if (!automatic || completed || context.reloadResources) {
        context.reloadResources = false;
        await loadApps(context, signal);
      }
    };
  }

  async function loadApps(context, signal) {
    try {
      const [, data] = await Promise.all([loadManagement(context, signal), API.apps(signal)]);
      if (!current(context) || signal.aborted) return;
      if (typeof data.available !== "boolean" || !["apt", "dnf", ""].includes(data.manager) || !Array.isArray(data.phpRuntimes)) throw invalid();
      const items = requireList(data, "items", 4);
      if (items.some((item) => !appIDs.includes(item?.id) || typeof item.installed !== "boolean")) throw invalid();
      context.apps = { ...data, items };
      const writable = hostingWritable(context, data);
      setHTML("apps-content", `${!data.available ? notice(text(data.reason, "软件管理不可用。"), "warning") : data.reason ? notice(data.reason) : ""}
        <section class="panel section-card hosting-intro"><h2>系统现有可信软件源</h2><p class="body-copy">${h(data.manager ? `包管理器：${data.manager.toUpperCase()}。` : "包管理器未就绪。")}安装只使用主机已经配置的可信源，不切换仓库、不应用“应用与源”中的模板，不升级整个系统、不删除已有应用数据。安装、启动或重启可能占用端口和内存。安装完成不等于服务已运行，实际状态以软件卡片为准。</p><a class="text-link" href="#sources">查看源配置模板（不会应用）</a></section>
        <div class="apps-grid">${items.map((item) => `<article class="panel app-card"><div class="app-card-heading"><span class="app-symbol">${icon(item.id === "nginx" ? "globe" : item.id === "php-fpm" ? "file" : "server")}</span><div><h2>${h(text(item.name, item.id))}</h2><span class="mono muted">${h(item.id)}</span></div>${item.installed ? badge("已安装", "success") : badge("未安装")}</div>
          <p class="body-copy">${h(text(item.description, "由系统包管理器维护的软件服务。"))}</p>${facts([["版本", text(item.version, item.installed ? "后端未提供版本" : "未安装")]])}<div class="app-service-state">${serviceBadge(item.state)}${item.installed && item.state !== "active" ? `<p class="panel-subtitle">${h(item.state === "inactive" ? "已安装，服务尚未启动；需要使用时请点“启动”。" : "已安装，但服务未报告运行中。请检查状态与任务日志，确认后再启动。")}</p>` : ""}</div>
          <div class="form-actions"><button class="button button-primary button-small" type="button" data-action="app-operation" data-app-id="${h(item.id)}" data-operation="install" ${writable && !item.installed ? "" : "disabled"}>${item.installed ? "已安装" : "安装"}</button>
          ${Object.entries(actionNames).map(([action, label]) => `<button class="button button-small ${action === "stop" ? "button-danger" : ""}" type="button" data-action="app-operation" data-app-id="${h(item.id)}" data-operation="${action}" ${writable && item.installed ? "" : "disabled"}>${label}</button>`).join("")}</div></article>`).join("") || empty("后端没有返回可管理的软件。")}</div>
        <section class="panel section-card"><h2>已检测到的 PHP-FPM 运行时</h2><p class="panel-subtitle">网站只能选择主机实际提供的运行时；Nginx / PHP 等服务的内存属于主机应用，不是面板进程内存。</p>${data.phpRuntimes.length ? facts(list(data.phpRuntimes, 32).map((runtime) => [text(runtime.id), `${text(runtime.version)} · ${text(runtime.unit)} · ${text(runtime.socket, "Socket 未提供")}`])) : empty("没有可用的 PHP-FPM 运行时。安装后请检查任务结果并重新读取。")}</section>`);
    } catch (error) {
      if (!aborted(error) && current(context)) {
        context.apps = null;
        setHTML("apps-content", errorMarkup(error));
      }
    }
  }

  function confirmAppAction(id, action) {
    const context = state.route;
    const item = context?.apps?.items.find((entry) => entry.id === id);
    if (!item || !hostingWritable(context, context.apps) || !["install", "start", "stop", "restart"].includes(action) || (action === "install" ? item.installed : !item.installed)) return showToast("当前不可执行该软件操作，请重新读取权限和软件状态。");
    const label = action === "install" ? "安装" : actionNames[action];
    const effect = action === "install" ? "将使用系统现有可信源安装软件及所需依赖，可能启动服务、占用端口和内存；不会应用面板中的软件源模板。" : action === "start" ? "将启动此软件服务，可能开放其已有配置中的监听端口。" : "将中断此软件提供的服务，可能影响依赖它的网站或数据库连接。";
    openConfirm(`${label}${text(item.name, id)}`, `<p>软件：<strong>${h(text(item.name, id))}</strong> <span class="mono">${h(id)}</span></p>${notice(`${effect}以系统最高权限执行，不会自动重试。`, "warning")}`, `确认${label}`, async (signal) => {
      await acceptedHostingJob(context, await API.appAction(id, action, signal));
    });
  }

  function renderJobsPage(context) {
    context.jobId = state.jobSelection || "";
    setHTML(main, `${pageHeading("任务", "软件与网站共享有界任务队列；最多保留 32 项。进度、阶段和日志均来自后端。", refreshButton())}<div id="route-message"></div><div id="management-status"></div>
      <section class="panel section-card hosting-intro"><p class="body-copy">仅本页可见且存在排队 / 执行中任务时每 5 秒读取。切页、隐藏或打开对话框会暂停；关闭页面不等于取消服务器任务。失败或进程重启后的中断任务不会自动重试。</p><div class="form-actions"><a class="text-link" href="#apps">软件实际状态</a><a class="text-link" href="#websites">网站实际状态</a></div></section>
      <section class="panel hosting-task-panel" id="jobs-content">${loading()}</section><div id="job-detail"></div>`);
    context.refresh = async (signal, automatic) => {
      const requests = [loadHostingJobs(context, signal, true)];
      if (!automatic) requests.push(loadManagement(context, signal));
      await Promise.all(requests);
      if (current(context) && !signal.aborted && context.jobId) await loadJobDetail(context, signal);
    };
  }

  async function loadJobDetail(context, signal) {
    const id = context.jobId;
    const sequence = context.jobDetailSequence = (context.jobDetailSequence || 0) + 1;
    context.jobDetailController?.abort();
    const controller = new AbortController();
    context.jobDetailController = controller;
    const abort = () => controller.abort();
    if (signal.aborted) controller.abort();
    else signal.addEventListener("abort", abort, { once: true });
    try {
      const data = await API.job(id, controller.signal);
      if (!current(context) || controller.signal.aborted || context.jobDetailSequence !== sequence) return;
      const job = requireJob(data.job);
      if (job.id !== id) throw invalid();
      // Only one task's bounded tail is rendered, not 32 expanded log streams.
      const lines = job.logs.slice(-2048).map((line) => typeof line === "string" ? line : "[无效日志行]");
      const joined = lines.join("\n");
      const logs = joined.slice(-65536);
      setHTML("job-detail", `<section class="panel section-card job-detail"><div class="section-toolbar"><h2>任务详情</h2><button class="button button-small" type="button" data-action="close-job-detail">收起</button></div>${facts([["任务", job.id], ["操作 / 目标", `${text(job.action)} / ${text(job.target)}`], ["创建", formatDate(job.createdAt)], ["开始", formatDate(job.startedAt)], ["结束", formatDate(job.finishedAt)]])}<div class="job-detail-state">${jobBadge(job)}${jobProgress(job)}</div>
        ${job.message ? notice(job.message) : ""}${job.error ? notice(job.error, "error") : ""}${job.status === "interrupted" ? notice("任务已中断，可能有部分操作已经执行。请先检查实际状态，不要盲目重复提交。", "warning") : ""}
        <h3>有界日志尾部</h3><p class="panel-subtitle">仅显示后端保留日志，最多 2,048 行 / 64 Ki 字符；不执行 HTML 或终端控制代码。</p>${joined.length > logs.length || job.logs.length > lines.length ? notice("显示内容已截断，仅保留日志尾部。", "warning") : ""}${logs ? `<pre class="code-preview log-preview">${h(logs)}</pre>` : empty("后端尚未报告日志。")}</section>`);
    } catch (error) {
      if (!aborted(error) && current(context) && context.jobDetailSequence === sequence) setHTML("job-detail", errorMarkup(error));
    } finally {
      signal.removeEventListener("abort", abort);
      if (context.jobDetailController === controller) context.jobDetailController = null;
    }
  }

  function showJob(id) {
    if (typeof id !== "string" || !id || id.length > 128) return;
    state.jobSelection = id;
    if (state.page !== "jobs") { location.hash = "#jobs"; return; }
    const context = state.route;
    context.jobId = id;
    setHTML("job-detail", loading("正在读取任务详情…"));
    void loadJobDetail(context, context.signal);
  }

  function validateSite(site) {
    if (!validSiteID(site?.id) || typeof site.name !== "string" || !Array.isArray(site.domains) || !site.domains.length || site.domains.length > 10 || site.domains.some((domain) => typeof domain !== "string") || !Object.hasOwn(siteTypes, site.type) || typeof site.enabled !== "boolean" || typeof site.tlsEnabled !== "boolean" || !Number.isInteger(site.port) || !Number.isSafeInteger(site.revision) || site.revision < 1) throw invalid("网站响应缺少有效标识、类型或配置版本。请重新读取。");
    return site;
  }

  function siteURL(site) {
    try {
      const url = new URL(site.url);
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.origin === location.origin) return "";
      return url.href;
    } catch { return ""; }
  }

  function siteFields(site) {
    return { name: site.name, domains: site.domains, type: site.type, port: site.port, enabled: site.enabled, ...(site.type === "proxy" ? { upstream: site.upstream } : {}), ...(site.type === "php" ? { phpRuntime: site.phpRuntime } : {}) };
  }

  function renderWebsitesPage(context) {
    setHTML(main, `${pageHeading("网站", "管理真实 Nginx 静态站点、反向代理和 PHP 网站。每次配置变更先校验、再重载；失败请查看任务诊断。", `${refreshButton()}<button class="button button-primary" type="button" data-action="create-site" id="create-site" disabled>创建网站</button>`)}<div id="route-message"></div><div id="management-status">${loading("正在读取最高权限状态…")}</div>
      <div id="sites-content">${loading("正在读取受管网站…")}</div><div id="hosting-jobs"></div><div id="site-files"></div>`);
    context.refresh = async (signal, automatic) => {
      const completed = await loadHostingJobs(context, signal);
      if (!current(context) || signal.aborted) return;
      if (!automatic || completed || context.reloadResources) {
        context.reloadResources = false;
        await loadSites(context, signal);
        if (current(context) && !signal.aborted && context.siteFiles) await loadSiteFiles(context, signal);
      }
    };
  }

  async function loadSites(context, signal) {
    const create = document.getElementById("create-site");
    if (create) create.disabled = true;
    try {
      const [, data] = await Promise.all([loadManagement(context, signal), API.sites(signal)]);
      if (!current(context) || signal.aborted) return;
      if (typeof data.available !== "boolean" || !Array.isArray(data.phpRuntimes) || !number(data.uploadLimitBytes) || data.uploadLimitBytes <= 0 || !number(data.editLimitBytes) || data.editLimitBytes <= 0) throw invalid();
      const sites = requireList(data, "sites", 128).map(validateSite);
      context.sites = { ...data, sites, uploadLimitBytes: Math.min(data.uploadLimitBytes, 2097152), editLimitBytes: Math.min(data.editLimitBytes, 262144) };
      const writable = hostingWritable(context, data);
      if (create) create.disabled = !writable || sites.length >= 128;
      setHTML("sites-content", `${!data.available ? notice(text(data.reason, "网站管理不可用，请先检查权限与 Nginx。"), "warning") : data.reason ? notice(data.reason) : ""}
        <section class="panel section-card hosting-intro"><div class="section-toolbar"><div><h2>受管网站 <span class="muted">${sites.length} / 128</span></h2><p class="panel-subtitle">仅管理面板创建的配置；不会接管系统已有的未知 Nginx 配置。</p></div><a class="text-link" href="#apps">安装 / 检查 Nginx 和 PHP</a></div><p class="body-copy">首次使用请到软件页安装并启动 Nginx；PHP 网站还需启动实际 PHP-FPM 运行时。已安装但未运行不是安装失败。访问地址是配置值，不代表 DNS、证书信任或公网可达性已经验证。删除只移除受管配置，始终保留网站目录与文件。</p></section>
        <section class="panel sites-panel">${table(["网站 / 域名", "类型 / 端口", "配置状态", "操作"], sites.map((site) => {
          const url = siteURL(site);
          return `<tr><td class="wrap-cell"><button class="text-link" type="button" data-action="site-details" data-site-id="${h(site.id)}"><strong>${h(site.name)}</strong></button><small class="mono">${h(site.domains.join("\n"))}</small>${url ? `<a class="text-link" href="${h(url)}" target="_blank" rel="noopener noreferrer">访问配置地址 ${icon("chevron")}</a>` : ""}</td>
            <td>${h(siteTypes[site.type])}<small class="mono">端口 ${h(site.port)}${site.phpRuntime ? ` · ${h(site.phpRuntime)}` : ""}</small>${site.upstream ? `<small class="mono wrap-cell">${h(site.upstream)}</small>` : ""}</td>
            <td>${badge(site.enabled ? "配置启用" : "已停用", site.enabled ? "success" : "neutral")} ${badge(site.tlsEnabled ? "TLS 已配置" : "未配置 TLS", site.tlsEnabled ? "success" : "neutral")}<small>${h(text(site.configStatus, "配置状态未提供"))}</small></td>
            <td><div class="row-actions site-actions"><button class="button button-small" type="button" data-action="edit-site" data-site-id="${h(site.id)}" ${writable ? "" : "disabled"}>编辑</button><button class="button button-small" type="button" data-action="toggle-site" data-site-id="${h(site.id)}" ${writable ? "" : "disabled"}>${site.enabled ? "停用" : "启用"}</button><button class="button button-small" type="button" data-action="site-files" data-site-id="${h(site.id)}" ${data.available ? "" : "disabled"}>文件</button><button class="button button-small" type="button" data-action="site-tls" data-site-id="${h(site.id)}" ${writable ? "" : "disabled"}>导入 TLS</button><button class="button button-small button-danger" type="button" data-action="delete-site" data-site-id="${h(site.id)}" ${writable ? "" : "disabled"}>删除配置</button></div></td></tr>`;
        }).join(""), "受管网站列表", "没有受管网站。具备权限并安装 Nginx 后，可创建第一个网站。")}</section>`);
    } catch (error) {
      if (!aborted(error) && current(context)) {
        context.sites = null;
        setHTML("sites-content", errorMarkup(error));
      }
    }
  }

  function findSite(context, id, write = false) {
    if (!current(context) || !validSiteID(id) || !context.sites || (write && !hostingWritable(context, context.sites))) throw new API.Error("网站状态或管理权限不可用，请重新读取。", "site_unavailable");
    const site = context.sites.sites.find((item) => item.id === id);
    if (!site) throw new API.Error("该网站不在当前列表中，请重新读取。", "site_missing");
    return site;
  }

  function openSiteDetails(id) {
    findSite(state.route, id);
    const modal = openDialog("网站详情", loading());
    modal.load = async (signal) => {
      const site = validateSite((await API.site(id, signal)).site);
      if (site.id !== id) throw invalid();
      return `${facts([["名称", site.name], ["稳定标识", site.id], ["域名", site.domains.join(", ")], ["类型", siteTypes[site.type]], ["端口", site.port], ["启用", site.enabled ? "是" : "否"], ["TLS", site.tlsEnabled ? "已配置" : "未配置"], ["配置状态", text(site.configStatus)], ["配置地址（未验证公网）", text(site.url)], ["受管根目录", text(site.root)], ["上游", text(site.upstream, "不适用")], ["PHP 运行时", text(site.phpRuntime, "不适用")], ["版本", site.revision], ["创建", formatDate(site.createdAt)], ["更新", formatDate(site.updatedAt)]])}${notice("删除只移除受管配置，文件始终保留。TLS 私钥不会通过查询接口返回。")}`;
    };
    void loadDialog(modal);
  }

  function confirmationField(message) {
    return `<label class="checkbox-field operation-confirm"><input type="checkbox" name="confirmed" required /><span>${h(message)}</span></label>`;
  }

  function hostingFormFooter(form, label) {
    return `<button class="button" type="button" data-action="close-dialog">取消</button><button class="button button-primary" type="submit" form="${h(form)}">${h(label)}</button>`;
  }

  function renderSiteForm(site, data) {
    const type = site?.type || "static";
    const runtimes = list(data.phpRuntimes, 32);
    return `<form id="site-config-form" data-form="site-config" class="form-stack"><p class="body-copy">仅接受域名、端口和所选类型的配置字段，不接受系统路径或 Nginx 指令。站点域名不能等于面板主机名。</p>
      <div class="form-grid"><label class="field"><span>名称</span><input name="name" required maxlength="64" value="${h(site?.name)}" autocomplete="off" /></label><label class="field"><span>网站类型</span><select name="type">${options(Object.entries(siteTypes), type)}</select></label>
      <label class="field full-width"><span>域名 / IP <small>最多 10 个；逗号、空格或换行分隔</small></span><textarea name="domains" rows="3" required maxlength="2600" placeholder="site.example.com" spellcheck="false">${h(site?.domains.join("\n"))}</textarea><small>使用 ASCII 域名（国际化域名请用 Punycode），不含协议、路径或通配符。</small></label>
      <label class="field"><span>监听端口</span><input name="port" type="number" required min="1" max="65535" step="1" value="${h(site?.port ?? 80)}" /><small>80、443 或 1024–65535；不能占用面板端口。TLS 导入前请选择兼容端口。</small></label>
      <label class="checkbox-field"><input name="enabled" type="checkbox" ${site?.enabled === false ? "" : "checked"} /><span>启用网站配置（停用时返回 503）</span></label>
      <label class="field full-width" data-site-field="proxy" ${type === "proxy" ? "" : "hidden"}><span>反向代理上游</span><input name="upstream" type="url" maxlength="2048" value="${h(site?.upstream)}" placeholder="http://127.0.0.1:3000" autocomplete="off" spellcheck="false" ${type === "proxy" ? "required" : "disabled"} /><small>HTTP(S) 地址，不含凭据、查询或片段；不能代理面板管理入口。确认后将把网站请求转发给此上游。</small></label>
      <label class="field full-width" data-site-field="php" ${type === "php" ? "" : "hidden"}><span>实际 PHP-FPM 运行时</span><select name="phpRuntime" ${type === "php" ? "required" : "disabled"}>${options([["", runtimes.length ? "请选择真实运行时" : "没有可用运行时，请先到软件页安装 / 启动 PHP-FPM"], ...runtimes.map((runtime) => [runtime.id, `${text(runtime.id)} · ${text(runtime.version)}`])], site?.phpRuntime || "")}</select><small>只使用后端检测到的运行时，不提供虚构的 PHP 版本。</small></label></div>
      ${site ? `<p class="panel-subtitle">配置版本 ${h(site.revision)}；并发变更会被拒绝，不覆盖其他会话的修改。${site.tlsEnabled ? "此站点已配置 TLS，修改域名后请检查证书匹配。" : ""}</p>` : ""}
      ${confirmationField(`我确认${site ? "修改" : "创建"}此网站，并允许以系统最高权限写入受管配置、校验及重载 Nginx；可能影响对外服务。`)}<div data-form-error></div></form>`;
  }

  function openSiteForm(id = "") {
    const context = state.route;
    if (!hostingWritable(context, context?.sites)) throw new API.Error("当前不允许管理网站，请重新读取权限。", "site_unavailable");
    if (id) findSite(context, id, true);
    const modal = openDialog(id ? "编辑网站" : "创建网站", id ? loading("正在读取最新配置版本…") : renderSiteForm(null, context.sites), hostingFormFooter("site-config-form", id ? "确认修改并提交任务" : "确认创建并提交任务"));
    modal.context = context;
    modal.site = null;
    if (!id) return;
    modal.load = async (signal) => {
      const site = validateSite((await API.site(id, signal)).site);
      if (site.id !== id) throw invalid();
      modal.site = site;
      return renderSiteForm(site, context.sites);
    };
    void loadDialog(modal);
  }

  function updateSiteTypeFields(form) {
    const type = form.elements.namedItem("type").value;
    for (const field of form.querySelectorAll("[data-site-field]")) {
      const enabled = field.dataset.siteField === type;
      field.hidden = !enabled;
      for (const control of field.querySelectorAll("input, select")) { control.disabled = !enabled; control.required = enabled; }
    }
  }

  function readSiteConfig(data, context) {
    const read = (key) => String(data.get(key) ?? "").trim();
    const domains = read("domains").split(/[\s,，]+/).filter(Boolean);
    const type = read("type");
    const port = Number(read("port"));
    if (!read("name") || read("name").length > 64 || !Object.hasOwn(siteTypes, type)) throw new API.Error("请填写有效网站名称和类型。", "invalid_site");
    if (!domains.length || domains.length > 10 || domains.some((value) => value.length > 253 || !/^[A-Za-z0-9.:[\]-]+$/.test(value))) throw new API.Error("请填写最多 10 个有效 ASCII 域名或 IP，不含协议、路径及通配符。", "invalid_domains");
    if (!Number.isInteger(port) || !([80, 443].includes(port) || (port >= 1024 && port <= 65535))) throw new API.Error("网站端口必须是 80、443 或 1024–65535。", "invalid_port");
    const site = { name: read("name"), domains, type, port, enabled: data.get("enabled") === "on" };
    if (type === "proxy") {
      const upstream = read("upstream");
      let url;
      try { url = new URL(upstream); } catch { throw new API.Error("请输入有效反向代理上游。", "invalid_upstream"); }
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash || /\s/.test(upstream)) throw new API.Error("上游必须是无凭据、查询和片段的 HTTP(S) 地址。", "invalid_upstream");
      site.upstream = upstream;
    }
    if (type === "php") {
      if (!context.sites.phpRuntimes.some((runtime) => runtime.id === read("phpRuntime"))) throw new API.Error("请先安装 / 启动 PHP-FPM，并选择后端实际提供的运行时。", "php_runtime_missing");
      site.phpRuntime = read("phpRuntime");
    }
    return site;
  }

  function confirmSiteChange(id, remove = false) {
    const context = state.route;
    const site = findSite(context, id, true);
    const label = remove ? "删除网站配置" : site.enabled ? "停用网站" : "启用网站";
    const detail = remove ? `仅移除受管配置，网站目录 ${text(site.root)} 及其中全部文件始终保留。网站将停止由此配置提供服务；不会删除文件。` : site.enabled ? "停用会使此网站返回 503，中断对外服务；保留所有配置和文件。" : "启用会重新提供网站内容或代理上游请求。";
    openConfirm(label, `<p>网站：<strong>${h(site.name)}</strong> · ${h(site.domains.join(", "))}</p>${notice(`${detail}操作以版本 ${site.revision} 为依据，Nginx 校验或重载失败时请查看任务日志。`, "warning")}`, `确认${label}`, async (signal) => {
      const result = remove ? await API.deleteSite(id, site.revision, signal) : await API.updateSite(id, site.revision, { ...siteFields(site), enabled: !site.enabled }, signal);
      await acceptedHostingJob(context, result);
    });
  }

  function relativeSitePath(value, allowEmpty = false) {
    if (typeof value !== "string" || value.length > 1024 || (!value && !allowEmpty) || /[\\\u0000-\u001f]/.test(value) || (value && value.split("/").some((part) => !part || part === "." || part === ".."))) throw new API.Error("只允许网站根目录内的相对路径，不接受绝对路径、空路径段、反斜杠或上级目录。", "invalid_path");
    return value;
  }

  function selectSiteFiles(id) {
    const context = state.route;
    findSite(context, id);
    context.siteFiles = { id, path: "", offset: 0, previous: [], data: null };
    setHTML("site-files", loading("正在读取网站目录…"));
    void loadSiteFiles(context, context.signal);
  }

  async function loadSiteFiles(context, signal) {
    const nav = context.siteFiles;
    if (!nav) return;
    context.fileController?.abort();
    const controller = new AbortController();
    context.fileController = controller;
    const abort = () => controller.abort();
    if (signal.aborted) controller.abort();
    else signal.addEventListener("abort", abort, { once: true });
    try {
      const site = findSite(context, nav.id);
      const data = await API.siteFiles(nav.id, nav.path, nav.offset, controller.signal);
      if (!current(context) || controller.signal.aborted || context.siteFiles !== nav) return;
      if (relativeSitePath(data.path, true) !== nav.path || (data.nextOffset !== null && (!Number.isSafeInteger(data.nextOffset) || data.nextOffset <= nav.offset))) throw invalid("网站目录响应或分页游标无效。");
      nav.data = { ...data, entries: requireList(data, "entries", 200) };
      setHTML("site-files", renderSiteFileListing(context, site));
    } catch (error) {
      if (!aborted(error) && current(context) && context.siteFiles === nav) {
        nav.data = null;
        setHTML("site-files", `${errorMarkup(error)}<button class="button" type="button" data-action="close-site-files">关闭网站文件</button>`);
      }
    } finally {
      signal.removeEventListener("abort", abort);
      if (context.fileController === controller) context.fileController = null;
    }
  }

  function renderSiteFileListing(context, site) {
    const nav = context.siteFiles;
    const data = nav.data;
    const writable = hostingWritable(context, context.sites);
    const parts = nav.path.split("/").filter(Boolean);
    const entries = data.entries.map((entry) => {
      const safeName = typeof entry.name === "string" && entry.name && !/[\/\\\u0000-\u001f]/.test(entry.name) && ![".", ".."].includes(entry.name);
      const actionable = safeName && ["directory", "file"].includes(entry.type);
      const path = nav.path ? `${nav.path}/${entry.name}` : entry.name;
      const editable = entry.type === "file" && number(entry.size) && entry.size <= context.sites.editLimitBytes;
      const button = actionable && (entry.type === "directory" || editable) ? `<button class="file-name-button" type="button" data-action="${entry.type === "directory" ? "site-directory" : "edit-site-text"}" data-path="${h(path)}">${icon(entry.type === "directory" ? "folder" : "file")}${h(entry.name)}</button>` : `<span class="file-name-static">${icon("file")}${h(text(entry.name))}</span>`;
      return `<tr><td>${button}</td><td>${h(entry.type === "directory" ? "目录" : entry.type === "file" ? "普通文件" : "不可操作类型")}${entry.type === "file" && !editable ? "<small>超过文本编辑上限或大小未知</small>" : ""}</td><td>${entry.type === "file" ? h(formatBytes(entry.size)) : "—"}</td><td>${h(formatDate(entry.modified))}</td></tr>`;
    }).join("");
    return `<section class="panel site-files-panel"><div class="section-toolbar"><div><h2>${h(site.name)} · 网站文件</h2><p class="panel-subtitle">根目录 ${h(text(site.root))}；不跟随链接，不执行预览内容。${site.type === "proxy" ? "代理请求不会使用本地静态文件。" : ""}</p></div><button class="button button-small" type="button" data-action="close-site-files">收起</button></div>
      <div class="file-toolbar"><nav class="file-breadcrumb" aria-label="网站文件路径"><button type="button" data-action="site-directory" data-path="">根目录</button>${parts.map((part, index) => `${icon("chevron")}<button type="button" data-action="site-directory" data-path="${h(parts.slice(0, index + 1).join("/"))}">${h(part)}</button>`).join("")}</nav><div class="form-actions"><button class="button button-small" type="button" data-action="new-site-text" ${writable ? "" : "disabled"}>新建文本</button><button class="button button-small" type="button" data-action="upload-site-file" ${writable ? "" : "disabled"}>上传文件</button></div></div>
      <p class="path-caption">单文件上传 ≤ ${h(formatBytes(context.sites.uploadLimitBytes))}（不覆盖）；UTF-8 文本编辑 ≤ ${h(formatBytes(context.sites.editLimitBytes))}，覆盖必须匹配原 SHA-256。没有文件删除接口。</p>
      ${table(["名称", "类型", "大小", "修改时间"], entries, "网站目录", "本页没有文件或目录。")}
      <div class="file-bottom"><span>本页 ${data.entries.length} 项 · 偏移 ${nav.offset}</span><div class="form-actions"><button class="button button-small" type="button" data-action="site-files-previous" ${nav.previous.length ? "" : "disabled"}>上一页</button><button class="button button-small" type="button" data-action="site-files-next" ${data.nextOffset === null ? "disabled" : ""}>下一页</button></div></div></section>`;
  }

  function moveSiteDirectory(path) {
    const context = state.route;
    if (!context?.siteFiles) return;
    context.siteFiles = { id: context.siteFiles.id, path: relativeSitePath(path, true), offset: 0, previous: [], data: null };
    void loadSiteFiles(context, context.signal);
  }

  function renderSiteTextForm(context, editor) {
    const writable = hostingWritable(context, context.sites);
    return `<form id="site-content-form" data-form="site-content" class="form-stack"><label class="field"><span>相对路径</span><input name="path" required maxlength="1024" value="${h(editor.path)}" ${editor.sha256 ? "readonly" : ""} autocomplete="off" spellcheck="false" /></label>
      <label class="field"><span>UTF-8 文本 <small>最多 ${h(formatBytes(context.sites.editLimitBytes))}</small></span><textarea class="code-editor" name="content" rows="16" maxlength="262144" spellcheck="false" autocomplete="off" ${writable ? "" : "readonly"}>${h(editor.content)}</textarea></label>
      <p class="panel-subtitle">${editor.sha256 ? `原 SHA-256：${h(editor.sha256)}；不匹配将拒绝覆盖。` : "新建文件不覆盖任何既有文件。"} 编辑器不会执行内容；保存的 PHP 或网页可能由网站对外运行 / 提供。</p>
      ${writable ? confirmationField("我确认将此文本写入上述网站文件；它会改变网站实际内容，覆盖仅在原 SHA-256 仍匹配时进行。") : notice("当前没有写入权限，仅显示只读文本。", "warning")}<div data-form-error></div></form>`;
  }

  function openSiteText(path, create = false) {
    const context = state.route;
    const nav = context?.siteFiles;
    if (!nav) return;
    const site = findSite(context, nav.id, create);
    const writable = hostingWritable(context, context.sites);
    const modal = openDialog(create ? "新建网站文本" : "网站文本编辑", loading(), writable ? hostingFormFooter("site-content-form", "确认写入并提交任务") : undefined);
    modal.context = context;
    modal.site = site;
    if (create) {
      modal.editor = { path: nav.path ? `${nav.path}/` : "", content: "" };
      setHTML("dialog-content", renderSiteTextForm(context, modal.editor));
      return;
    }
    path = relativeSitePath(path);
    modal.load = async (signal) => {
      const data = await API.siteContent(site.id, path, signal);
      if (data.path !== path || typeof data.content !== "string" || !/^[a-f0-9]{64}$/.test(data.sha256) || utf8Bytes(data.content) > context.sites.editLimitBytes) throw invalid("文本响应必须完整且有界，并携带 SHA-256；不会覆盖未知版本。");
      modal.editor = { path: data.path, content: data.content, sha256: data.sha256 };
      return renderSiteTextForm(context, modal.editor);
    };
    void loadDialog(modal);
  }

  function openSiteUpload() {
    const context = state.route;
    const nav = context?.siteFiles;
    if (!nav) return;
    const site = findSite(context, nav.id, true);
    const modal = openDialog("上传网站文件", `<form id="site-upload-form" data-form="site-upload" class="form-stack"><p class="body-copy">网站 <strong>${h(site.name)}</strong> · 目标目录 <span class="mono">${h(nav.path || "/（网站根目录）")}</span></p>
      <label class="field"><span>单个文件 <small>最大 ${h(formatBytes(context.sites.uploadLimitBytes))}</small></span><input name="file" type="file" required /></label>
      <label class="field"><span>目标文件名 <small>可选，留空使用所选文件原名</small></span><input name="filename" maxlength="255" autocomplete="off" spellcheck="false" /><small>只允许本目录中的文件名，不含路径。既有同名文件将返回冲突；文本覆盖请用编辑器。</small></label>
      ${confirmationField("我确认上传此文件并使其成为网站真实文件。不会解压归档，不覆盖同名文件；上传的内容可能被网站对外提供或运行。")}<div data-form-error></div></form>`, hostingFormFooter("site-upload-form", "确认上传并提交任务"));
    modal.context = context;
    modal.site = site;
    modal.directory = nav.path;
  }

  function openSiteTLS(id) {
    const context = state.route;
    findSite(context, id, true);
    const modal = openDialog("导入网站 TLS", loading("正在读取最新网站版本…"), hostingFormFooter("site-tls-form", "确认导入并提交任务"));
    modal.context = context;
    modal.load = async (signal) => {
      const site = validateSite((await API.site(id, signal)).site);
      if (site.id !== id) throw invalid();
      modal.site = site;
      return `<form id="site-tls-form" data-form="site-tls" class="form-stack"><p class="body-copy">网站 <strong>${h(site.name)}</strong> · 首域名 ${h(site.domains[0])} · 端口 ${h(site.port)} · 配置版本 ${h(site.revision)}</p>
        ${notice(site.port === 80 ? "当前端口为 80，请先编辑网站端口为 443 或受支持的高位 TLS 端口，再导入证书。" : "仅导入已有 PEM 证书和匹配私钥；由后端验证证书链、首域名 SAN 与密钥。没有自动签发或续期。", "warning")}
        <label class="field"><span>PEM 证书链 <small>最大 64 KiB</small></span><textarea class="code-editor" name="certificate" required rows="7" maxlength="65536" spellcheck="false" autocomplete="off" placeholder="-----BEGIN CERTIFICATE-----"></textarea></label>
        <label class="field"><span>PEM 私钥 <small>最大 16 KiB</small></span><textarea class="code-editor secret-input" name="privateKey" required rows="6" maxlength="16384" spellcheck="false" autocomplete="off" placeholder="-----BEGIN PRIVATE KEY-----"></textarea><small>只提交给同源后端，不写入浏览器存储；关闭或提交后清空。后端不返回、不记录私钥。</small></label>
        ${confirmationField("我确认导入上述证书和私钥，修改此网站 TLS 配置并校验 / 重载 Nginx；更换证书可能影响现有 HTTPS 连接。")}<div data-form-error></div></form>`;
    };
    void loadDialog(modal);
  }

  async function submitHostingForm(form, data) {
    const modal = state.modal;
    const context = state.route;
    if (!modal || modal.busy || modal.context !== context || !hostingWritable(context, context?.sites)) return;
    const controls = Array.from(dialog.querySelectorAll("input, select, textarea, button"));
    const disabled = controls.map((control) => control.disabled);
    modal.busy = true;
    form.dataset.busy = "true";
    form.setAttribute("aria-busy", "true");
    controls.forEach((control) => { control.disabled = true; });
    const errorTarget = form.querySelector("[data-form-error]");
    setHTML(errorTarget, "");
    try {
      if (data.get("confirmed") !== "on") throw new API.Error("请明确确认本次真实变更。", "confirmation_required");
      let result;
      switch (form.dataset.form) {
        case "site-config": {
          const site = readSiteConfig(data, context);
          result = modal.site ? await API.updateSite(modal.site.id, modal.site.revision, site, modal.signal) : await API.createSite(site, modal.signal);
          break;
        }
        case "site-content": {
          const path = relativeSitePath(String(data.get("path") ?? ""));
          const content = String(data.get("content") ?? "");
          if (!modal.editor || (modal.editor.sha256 && modal.editor.path !== path)) throw invalid();
          if (utf8Bytes(content) > context.sites.editLimitBytes) throw new API.Error("UTF-8 文本超过编辑上限，请缩小内容。", "content_too_large");
          result = await API.saveSiteContent(modal.site.id, path, content, modal.editor.sha256, modal.signal);
          break;
        }
        case "site-upload": {
          const file = data.get("file");
          if (!(file instanceof File) || !file.name || file.size > context.sites.uploadLimitBytes) throw new API.Error("请选择一个不超过上传上限的文件。", "invalid_upload");
          const name = String(data.get("filename") ?? "").trim() || file.name;
          if (name.includes("/") || name.length > 255) throw new API.Error("目标文件名不能含路径，且不得超过 255 字符。", "invalid_path");
          const path = relativeSitePath(modal.directory ? `${modal.directory}/${name}` : name);
          result = await API.uploadSiteFile(modal.site.id, path, file, modal.signal);
          break;
        }
        case "site-tls": {
          const certificate = String(data.get("certificate") ?? "").trim();
          const privateKey = String(data.get("privateKey") ?? "").trim();
          if (!certificate || !privateKey || utf8Bytes(certificate) > 65536 || utf8Bytes(privateKey) > 16384) throw new API.Error("证书链不得超过 64 KiB，私钥不得超过 16 KiB；两项均不能为空。", "invalid_pem_size");
          if (modal.site.port === 80) throw new API.Error("请先编辑网站端口，再导入 TLS。当前端口 80 不兼容。", "tls_port_conflict");
          result = await API.siteTLS(modal.site.id, modal.site.revision, certificate, privateKey, modal.signal);
          break;
        }
        default: throw invalid();
      }
      await acceptedHostingJob(context, result);
    } catch (error) {
      if (!aborted(error) && state.modal === modal) setHTML(errorTarget, `${errorMarkup(error)}${error?.status === 409 ? notice("发生版本或路径冲突。未自动覆盖或重试；请先保存需要的草稿，再关闭并重新读取最新状态。", "warning") : ""}`);
    } finally {
      data.delete("privateKey");
      if (form.dataset.form === "site-tls") form.elements.namedItem("privateKey").value = "";
      if (state.modal === modal) {
        modal.busy = false;
        controls.forEach((control, index) => { control.disabled = disabled[index]; });
      }
      delete form.dataset.busy;
      form.removeAttribute("aria-busy");
    }
  }

  function renderServicesPage(context) {
    setHTML(main, `${pageHeading("服务", "固定服务列表，仅查看状态，不提供安装或启停操作。", refreshButton())}<div id="route-message"></div><div id="services-content">${loading()}</div>`);
    context.interval = 15000;
    context.refresh = (signal) => renderSection(context, "services-content", () => API.services(signal), (data) => {
      if (typeof data.available !== "boolean") throw invalid();
      if (!data.available) return notice(text(data.reason, "当前平台无法读取服务状态。"), "warning");
      const services = requireList(data, "services", 6);
      return `${data.reason ? notice(data.reason) : ""}<section class="panel">${table(["服务", "标识", "状态"], services.map((service) => `<tr><td>${h(text(service.name))}</td><td class="mono">${h(text(service.id))}</td><td>${serviceBadge(service.state)}</td></tr>`).join(""), "服务状态", "后端没有返回服务记录。")}</section>`;
    });
  }

  // Files are page-replaced, never accumulated. Only regular files can be previewed.
  function renderFilesPage(context) {
    setHTML(main, `${pageHeading("文件", "仅浏览已配置的根目录；每页最多 200 项，文本预览不超过 256 KiB。", refreshButton())}<div id="route-message"></div><div id="files-content">${loading()}</div>`);
    context.refresh = async (signal) => {
      try {
        const capabilities = await API.capabilities(signal);
        if (!current(context)) return;
        if (capabilities.files?.available !== true) {
          state.files.data = null;
          setHTML("files-content", notice(text(capabilities.files?.reason, "文件浏览未启用，请在后端显式配置文件根目录。"), "warning"));
          return;
        }
        const data = await API.files(state.files.path, state.files.offset, signal);
        if (!current(context)) return;
        if (typeof data.path !== "string" || typeof data.root !== "string") throw invalid();
        const entries = requireList(data, "entries", 200);
        if (data.nextOffset !== null && (!Number.isSafeInteger(data.nextOffset) || data.nextOffset <= state.files.offset)) throw invalid("后端返回了无效的分页游标。");
        state.files.path = data.path;
        state.files.data = { ...data, entries };
        setHTML("files-content", renderFileListing());
      } catch (error) {
        if (!aborted(error) && current(context)) {
          state.files.data = null;
          setHTML("files-content", `${errorMarkup(error)}<button class="button" type="button" data-directory="">返回配置根目录</button>`);
        }
      }
    };
  }

  function fileBreadcrumb() {
    const parts = state.files.path.split("/").filter(Boolean);
    return `<nav class="file-breadcrumb" aria-label="文件路径"><button type="button" data-directory="">根目录</button>${parts.map((part, index) => `${icon("chevron")}<button type="button" data-directory="${h(parts.slice(0, index + 1).join("/"))}"${index === parts.length - 1 ? ' aria-current="location"' : ""}>${h(part)}</button>`).join("")}</nav>`;
  }

  function renderFileListing() {
    const data = state.files.data;
    return `<section class="panel"><div class="file-toolbar">${fileBreadcrumb()}<label class="search-field">${icon("search")}<input id="file-search" type="search" placeholder="筛选本页文件" aria-label="筛选本页文件" value="${h(state.files.query)}" maxlength="256" /></label></div>
      <p class="path-caption">配置根目录：<span class="mono">${h(data.root)}</span></p>
      <div id="file-table">${renderFileRows()}</div>
      <div class="file-bottom"><span>本页 ${data.entries.length} 项 · 偏移 ${state.files.offset}</span><div class="page-actions">
        <button class="button" type="button" data-action="files-previous" ${state.files.previous.length ? "" : "disabled"}>上一页</button>
        <button class="button" type="button" data-action="files-next" ${data.nextOffset === null ? "disabled" : ""}>下一页</button>
      </div></div></section>`;
  }

  function renderFileRows() {
    const data = state.files.data;
    if (!data) return "";
    const query = state.files.query.toLocaleLowerCase();
    const entries = data.entries.filter((entry) => String(entry.name ?? "").toLocaleLowerCase().includes(query));
    const names = { directory: "目录", file: "普通文件", symlink: "符号链接（不跟随）", other: "其他（不可预览）" };
    const rows = entries.map((entry) => {
      const path = state.files.path ? `${state.files.path}/${entry.name}` : String(entry.name ?? "");
      const actionable = entry.type === "directory" || entry.type === "file";
      const name = actionable
        ? `<button class="file-name-button" type="button" ${entry.type === "directory" ? "data-directory" : "data-preview-file"}="${h(path)}">${icon(entry.type === "directory" ? "folder" : "file")}<span>${h(text(entry.name))}</span></button>`
        : `<span class="file-name-static">${icon("file")}<span>${h(text(entry.name))}</span></span>`;
      return `<tr><td>${name}</td><td>${h(Object.hasOwn(names, entry.type) ? names[entry.type] : "未知类型")}</td><td class="mono">${entry.type === "file" ? h(formatBytes(entry.size)) : "—"}</td><td>${h(formatDate(entry.modified))}</td></tr>`;
    }).join("");
    return table(["名称", "类型", "大小", "修改时间"], rows, "当前页文件", query ? "本页没有匹配项；筛选不会搜索其他页或子目录。" : "当前页没有目录项。");
  }

  function openDirectory(path) {
    state.files = { path, offset: 0, previous: [], query: "", data: null };
    renderPage();
  }

  function openFilePreview(path) {
    const modal = openDialog("文件预览", loading("正在读取文本文件…"));
    modal.load = async (signal) => {
      const data = await API.previewFile(path, signal);
      if (data.encoding !== "utf-8" || typeof data.content !== "string" || data.truncated !== false || new TextEncoder().encode(data.content).length > 262144) throw invalid("预览响应不是完整的有界 UTF-8 文本。");
      return `<p class="path-caption mono">${h(text(data.path))}</p><p class="muted">只读 UTF-8 文本，不执行其中的代码或 HTML。</p><pre class="code-preview">${h(data.content)}</pre>`;
    };
    void loadDialog(modal);
  }

  // Opening this route reads only cached status; remote checks require the button.
  function renderUpdatesPage(context) {
    setHTML(main, `${pageHeading("面板更新", "仅显示后端缓存；只有点击“检查更新”才请求 GitHub，不会自动检查或应用更新。", refreshButton("重新读取缓存"))}<div id="route-message"></div><div id="updates-content">${loading("正在读取更新缓存…")}</div>`);
    context.refresh = (signal) => renderSection(context, "updates-content", () => API.updateStatus(signal), renderUpdateStatus);
  }

  function safeReleaseURL(latest, repository) {
    const repoPattern = /^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,98}[A-Za-z0-9])?\/[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$/;
    const tagPattern = /^v(0|[1-9]\d{0,9})\.(0|[1-9]\d{0,9})\.(0|[1-9]\d{0,9})$/;
    if (!repoPattern.test(repository) || !tagPattern.test(latest.tag) || latest.tag.slice(1).split(".").some((part) => Number(part) > 2147483647)) return "";
    // Exact canonical equality also rejects credentials, ports, queries and fragments.
    const expected = `https://github.com/${repository}/releases/tag/${latest.tag}`;
    return latest.url === expected ? expected : "";
  }

  function renderUpdateStatus(data) {
    const states = {
      unconfigured: ["未配置仓库", "warning"],
      not_checked: ["尚未检查", "info"],
      up_to_date: ["已是最新版本", "success"],
      available: ["发现新版本", "success"],
      ahead: ["当前版本高于最新发布", "info"],
      incompatible: ["版本或数据结构不兼容", "warning"],
      no_release: ["暂无稳定 Release", "info"],
      rate_limited: ["检查频率受限", "warning"],
      error: ["检查失败", "error"],
    };
    if (!Object.hasOwn(states, data.state) || typeof data.configured !== "boolean" || typeof data.repository !== "string" || !data.current || typeof data.current.version !== "string" || typeof data.current.os !== "string" || data.canApply !== false || typeof data.command !== "string" || (data.checkedAt !== null && typeof data.checkedAt !== "string")) throw invalid("更新状态响应不符合契约；网页不提供应用更新操作。");
    const latest = data.latest;
    if (latest !== null && (!latest || typeof latest.version !== "string" || typeof latest.tag !== "string" || typeof latest.url !== "string" || typeof latest.notes !== "string" || typeof latest.assetsReady !== "boolean")) throw invalid("Release 信息不符合接口契约。");
    const [label, tone] = states[data.state];
    const url = latest && data.configured ? safeReleaseURL(latest, data.repository) : "";
    const rows = [
      ["当前版本", text(data.current.version)],
      ["运行平台", `${text(data.current.os)} / ${text(data.current.arch)}`],
      ["GitHub 仓库", text(data.repository, "未配置")],
      ["检查状态", label],
      ["最近检查", data.checkedAt === null ? "尚未检查" : formatDate(data.checkedAt)],
    ];
    if (data.nextCheckAt) rows.push(["下次可检查", formatDate(data.nextCheckAt)]);
    return `${notice(text(data.message, label), tone)}
      ${!data.configured ? notice("本地构建尚未配置 GitHub 仓库，无法检查更新。待真实公开仓库建立后，由管理员在后端启动时设置 -github-repo OWNER/REPO，或在构建时注入仓库，再重新读取缓存。此页面不能修改仓库。", "warning") : ""}
      <section class="panel section-card"><h2>版本状态</h2>${facts(rows)}
        <div class="form-actions"><button class="button" type="button" data-action="check-update" data-focus-key="check-update" ${data.configured ? "" : "disabled"}>检查更新</button><span class="muted">检查受后端缓存与频率限制；不会自动重试。</span></div>
      </section>
      ${latest ? `<section class="panel section-card"><h2>最近获取的 Release</h2>${facts([["版本", latest.version], ["标签", latest.tag], ["发布时间", formatDate(latest.publishedAt)], ["发布资源", latest.assetsReady ? "已就绪" : "未就绪"]])}
        ${latest.assetMessage || !latest.assetsReady ? notice(text(latest.assetMessage, "发布资源不完整，暂不能通过脚本更新。"), latest.assetsReady ? "info" : "warning") : ""}
        ${url ? `<a class="text-link" href="${h(url)}" target="_blank" rel="noopener noreferrer">查看 GitHub Release</a>` : notice("Release 链接未通过仓库与地址校验，不提供跳转。", "warning")}
        <pre class="code-preview release-notes">${h(text(latest.notes, "此 Release 未提供更新说明。"))}</pre>
      </section>` : `<section class="panel section-card"><h2>Release</h2>${empty("缓存中暂无 Release 信息。")}</section>`}
      <section class="panel section-card"><h2>在${data.current.os === "windows" ? " Windows " : " Linux "}主机上更新</h2>
        <p class="body-copy">网页仅提供更新通知，不执行安装、提权或服务重启。请在通过官方安装器部署的主机终端执行对应命令；开发预览不能代替正式服务安装。</p>
        ${data.current.os === "windows" ? notice("请使用管理员 PowerShell。服务会停止、更新并重新启动，未保存的会话将失效。", "info") : ""}
        ${data.command ? `<pre class="code-preview"><code>${h(data.command)}</code></pre>` : notice("后端未提供更新命令，请查看对应系统的安装与升级文档。", "warning")}
        <p class="panel-subtitle">执行前请确认仓库、目标版本与备份。普通更新不支持降级或不同数据结构版本。</p>
        <a class="text-link" href="https://linyufiee.github.io/QQON-Panel/" target="_blank" rel="noopener noreferrer">安装与升级文档</a>
      </section>`;
  }

  async function checkUpdates(target) {
    const context = state.route;
    if (!current(context) || state.page !== "updates" || context.refreshing || target.disabled) return;
    context.refreshing = true;
    target.disabled = true;
    target.textContent = "正在检查…";
    main.setAttribute("aria-busy", "true");
    main.querySelectorAll('[data-action="refresh"]').forEach((button) => { button.disabled = true; });
    setHTML("route-message", "");
    try {
      await renderSection(context, "updates-content", () => API.checkUpdate(context.signal), renderUpdateStatus);
    } finally {
      context.refreshing = false;
      if (current(context)) {
        main.removeAttribute("aria-busy");
        main.querySelectorAll('[data-action="refresh"]').forEach((button) => { button.disabled = false; });
        if (target.isConnected) {
          target.disabled = false;
          target.textContent = "检查更新";
        }
      }
    }
  }

  // Source settings are configuration-only: three distinct provider groups, exact revision on PUT.
  function renderSourcesPage(context) {
    setHTML(main, `${pageHeading("应用与源", "仅保存 APT、Docker CE 仓库和镜像加速的配置模板；不会切换系统仓库。", refreshButton("重新读取"))}<div id="route-message"></div><div id="sources-content">${loading()}</div>`);
    context.refresh = (signal) => renderSection(context, "sources-content", () => API.sources(signal), (data) => {
      validateSources(data);
      state.sourceRevision = data.revision;
      state.sourceDirty = false;
      return renderSourcesForm(data);
    });
  }

  function validateSources(data) {
    const config = data.config;
    if (!config || !["debian", "ubuntu"].includes(config.os) || !["amd64", "arm64"].includes(config.architecture) || !codenames(config.os).includes(config.codename) || !config.apt || !config.dockerCE || !config.registry || data.revision === undefined || data.revision === null || data.applied !== false) throw invalid("软件源配置响应不符合契约，未采用任何本地替代值。");
    if (!["official", "tuna", "aliyun", "custom"].includes(config.apt.provider) || !["official", "tuna", "aliyun", "custom"].includes(config.dockerCE.provider) || !["none", "aliyun", "custom"].includes(config.registry.provider)) throw invalid("后端返回了未知的软件源类型。");
  }

  function codenames(os) {
    return os === "ubuntu" ? ["jammy", "noble"] : ["bookworm", "trixie"];
  }

  function options(items, selected) {
    return items.map((item) => {
      const [value, label] = Array.isArray(item) ? item : [item, item];
      return `<option value="${h(value)}"${value === selected ? " selected" : ""}>${h(label)}</option>`;
    }).join("");
  }

  function renderSourcesForm(data) {
    const config = data.config;
    const providers = [["official", "官方"], ["tuna", "清华 TUNA"], ["aliyun", "阿里云"], ["custom", "自定义"]];
    const detected = data.detected ?? {};
    const detectedText = detected.supported === true
      ? `识别到 ${text(detected.os)} / ${text(detected.codename)} / ${text(detected.architecture)}。源连通性尚未验证。`
      : "当前平台未识别到受支持的发行版；以下是未验证的配置模板，不代表主机已经使用这些软件源。";
    return `${notice(detectedText, detected.supported === true ? "info" : "warning")}
      <section class="panel section-card"><h2>源配置</h2><p class="body-copy">保存仅写入面板数据。预览不会访问源地址、修改系统文件、安装软件或重启 Docker。</p>
        <p class="panel-subtitle">版本：<span class="mono">${h(data.revision)}</span> · ${data.verification === "not_checked" ? "未验证" : `验证状态未知（${h(data.verification)}）`} · 未应用到主机</p>
        <form data-form="sources" class="form-stack source-form">
          <div class="form-grid three-columns">
            <label class="field"><span>发行版</span><select name="os">${options([["debian", "Debian"], ["ubuntu", "Ubuntu"]], config.os)}</select></label>
            <label class="field"><span>版本代号</span><select name="codename">${options(codenames(config.os), config.codename)}</select></label>
            <label class="field"><span>架构</span><select name="architecture">${options(["amd64", "arm64"], config.architecture)}</select></label>
          </div>
          <fieldset class="source-category"><legend>APT 软件源</legend><div class="form-grid">
            <label class="field full-width"><span>APT 提供方</span><select name="aptProvider">${options(providers, config.apt.provider)}</select></label>
            <label class="field"><span>软件源 URL</span><input type="url" name="aptUrl" value="${h(config.apt.url)}" maxlength="2048" ${config.apt.provider === "custom" ? "required" : "disabled"} placeholder="https://…" autocomplete="off" spellcheck="false" /></label>
            <label class="field"><span>安全更新源 URL</span><input type="url" name="aptSecurityUrl" value="${h(config.apt.securityUrl)}" maxlength="2048" ${config.apt.provider === "custom" ? "required" : "disabled"} placeholder="https://…" autocomplete="off" spellcheck="false" /></label>
          </div><p class="field-help">非自定义提供方的地址由后端解析，不接受前端伪造的预设地址。</p></fieldset>
          <fieldset class="source-category"><legend>Docker CE 软件仓库</legend><div class="form-grid">
            <label class="field"><span>Docker CE 提供方</span><select name="dockerProvider">${options(providers, config.dockerCE.provider)}</select></label>
            <label class="field"><span>仓库 URL</span><input type="url" name="dockerUrl" value="${h(config.dockerCE.url)}" maxlength="2048" ${config.dockerCE.provider === "custom" ? "required" : "disabled"} placeholder="https://…" autocomplete="off" spellcheck="false" /></label>
          </div><p class="field-help">用于 Docker 引擎的软件包仓库，不是 Docker Hub 镜像加速。</p></fieldset>
          <fieldset class="source-category"><legend>Docker Registry 镜像加速</legend><div class="form-grid">
            <label class="field"><span>镜像加速提供方</span><select name="registryProvider">${options([["none", "不配置"], ["aliyun", "阿里云个人地址"], ["custom", "自定义"]], config.registry.provider)}</select></label>
            <label class="field"><span>镜像加速 URL</span><input type="url" name="registryUrl" value="${h(config.registry.url)}" maxlength="2048" ${config.registry.provider === "none" ? "disabled" : "required"} placeholder="https://…" autocomplete="off" spellcheck="false" /></label>
          </div><p class="field-help">阿里云必须填写你的个人加速地址，没有通用共享预设。所有自定义地址必须为无凭据、查询参数及片段的 HTTPS URL。</p></fieldset>
          <div data-form-error></div><div class="form-actions"><button class="button" type="submit" name="intent" value="preview">预览配置</button><button class="button button-primary" type="submit" name="intent" value="save">保存设置</button><span id="source-dirty" class="muted">已读取保存版本</span></div>
        </form>
      </section><div id="source-preview"></div>
      <section class="panel section-card"><h2>真实软件安装</h2><p class="body-copy">软件页面提供 Nginx、PHP-FPM、MariaDB、Redis 的安装与启停，仅使用系统现有可信软件源。本页模板不会自动应用；不提供 Compose 部署或卸载。</p><a class="text-link" href="#apps">打开软件管理</a></section>`;
  }

  function sourceConfig(formData) {
    const read = (name) => String(formData.get(name) ?? "").trim();
    const apt = read("aptProvider");
    const docker = read("dockerProvider");
    const registry = read("registryProvider");
    const config = {
      os: read("os"),
      codename: read("codename"),
      architecture: read("architecture"),
      apt: { provider: apt, url: apt === "custom" ? read("aptUrl") : "", securityUrl: apt === "custom" ? read("aptSecurityUrl") : "" },
      dockerCE: { provider: docker, url: docker === "custom" ? read("dockerUrl") : "" },
      registry: { provider: registry, url: registry === "none" ? "" : read("registryUrl") },
    };
    const urls = [config.apt.url, config.apt.securityUrl, config.dockerCE.url, config.registry.url].filter(Boolean);
    for (const value of urls) {
      let url;
      try { url = new URL(value); } catch { throw new API.Error("请输入有效的 HTTPS 软件源地址。", "invalid_url"); }
      if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash || /\s/.test(value)) throw new API.Error("软件源地址必须使用 HTTPS，且不能含有凭据、空白、查询参数或片段。", "invalid_url");
    }
    return config;
  }

  function updateSourceFields(form) {
    const enabled = (name, available) => {
      const field = form.elements.namedItem(name);
      field.disabled = !available;
      field.required = available;
    };
    enabled("aptUrl", form.elements.aptProvider.value === "custom");
    enabled("aptSecurityUrl", form.elements.aptProvider.value === "custom");
    enabled("dockerUrl", form.elements.dockerProvider.value === "custom");
    enabled("registryUrl", form.elements.registryProvider.value !== "none");
    state.sourceDirty = true;
    const dirty = document.getElementById("source-dirty");
    if (dirty) dirty.textContent = "有未保存的修改";
    setHTML("source-preview", "");
  }

  function renderSourcePreview(data) {
    if (data.applied !== false || [data.apt, data.dockerCE, data.registry].some((value) => typeof value !== "string") || !Array.isArray(data.warnings)) throw invalid();
    return `<section class="panel section-card"><h2>配置预览</h2><p class="panel-subtitle">未应用到主机；预览不是安装命令。</p>${data.warnings.slice(0, 32).map((warning) => notice(String(warning), "warning")).join("")}
      ${[["APT", data.apt], ["Docker CE", data.dockerCE], ["Registry", data.registry]].map(([label, content]) => `<h3>${h(label)}</h3>${content ? `<pre class="code-preview">${h(content)}</pre>` : empty("未配置此项。")}`).join("")}</section>`;
  }

  // Docker writes enqueue bounded server tasks. A successful enqueue is not a completed action.
  function renderDockerPage(context) {
    setHTML(main, `${pageHeading("Docker", "仅连接后端显式配置的本地 Unix socket。", refreshButton())}<div id="route-message"></div><div id="docker-status">${loading("正在读取 Docker 状态…")}</div>
      <section class="panel docker-panel"><div class="tab-list" role="tablist" aria-label="Docker 资源类型">${Object.entries(dockerTabs).map(([tab, label]) => `<button id="docker-tab-${tab}" type="button" role="tab" data-docker-tab="${tab}" aria-selected="${state.dockerTab === tab}" aria-controls="docker-resource-panel" tabindex="${state.dockerTab === tab ? 0 : -1}">${label}</button>`).join("")}</div>
        <div id="docker-resource-panel" role="tabpanel" aria-labelledby="docker-tab-${state.dockerTab}">${loading()}</div>
      </section><section class="panel section-card"><h2>任务状态</h2><p class="panel-subtitle">最多显示后端保留的 32 项；失败任务不自动重试。</p><div id="docker-tasks">${loading()}</div></section>`);
    context.interval = 10000;
    context.refresh = async (signal) => {
      await Promise.all([
        loadDockerResources(context, signal),
        renderSection(context, "docker-tasks", () => API.tasks(signal), (data) => {
          const tasks = requireList(data, "tasks", 32);
          context.interval = tasks.some((task) => ["queued", "running"].includes(task.status)) ? 3000 : 10000;
          return renderTasks(tasks);
        }),
      ]);
    };
  }

  async function loadDockerResources(context, signal) {
    try {
      const status = await API.docker(signal);
      if (!current(context)) return;
      if ([status.configured, status.available, status.writeEnabled].some((value) => typeof value !== "boolean")) throw invalid();
      state.dockerStatus = status;
      const description = status.available
        ? `引擎可用${typeof status.version === "string" ? ` · ${status.version}` : ""} · ${status.writeEnabled ? "已允许容器启停" : "只读，写入未启用"}`
        : text(status.reason, status.configured ? "Docker 已配置，但引擎不可用。" : "Docker 未配置。");
      setHTML("docker-status", `${notice(description, status.available ? "info" : "warning")}${status.available && status.reason ? notice(status.reason) : ""}`);
      if (!status.configured || !status.available) {
        setHTML("docker-resource-panel", empty("无法读取 Docker 资源。请先检查后端配置和引擎状态。"));
        return;
      }
      const data = await API.dockerList(state.dockerTab, signal);
      if (current(context)) setHTML("docker-resource-panel", renderDockerList(data));
    } catch (error) {
      if (!aborted(error) && current(context)) {
        state.dockerStatus = null;
        setHTML("docker-status", errorMarkup(error));
        setHTML("docker-resource-panel", empty("本次读取失败，未显示旧数据作为当前状态。"));
      }
    }
  }

  function renderDockerList(data) {
    const tab = state.dockerTab;
    const rows = requireList(data, tab, 1000);
    const capNotice = data[tab].length > rows.length ? notice("当前仅显示响应中的前 1000 项。", "warning") : "";
    if (tab === "containers") {
      return capNotice + table(["容器 / ID", "镜像", "状态", "端口", "创建时间", "操作"], rows.map((item) => {
        const validId = typeof item.id === "string" && /^[a-f0-9]{64}$/i.test(item.id);
        const writable = state.dockerStatus?.writeEnabled === true && validId;
        const running = ["running", "paused", "restarting"].includes(item.state);
        const startable = ["created", "exited", "dead"].includes(item.state);
        return `<tr><td><strong>${h(text(item.name))}</strong><small class="mono" title="${h(item.id)}">${h(typeof item.id === "string" ? item.id.slice(0, 12) : "ID 未知")}</small></td><td class="wrap-cell">${h(text(item.image))}</td>
          <td>${containerBadge(item.state)}<small>${h(text(item.status))}</small></td><td class="mono wrap-cell">${h(text(item.ports, "无已发布端口"))}</td><td>${h(formatDate(item.created))}</td>
          <td><div class="row-actions"><button class="button button-small" type="button" data-container-logs="${h(item.id)}" data-focus-key="logs-${h(item.id)}" ${validId ? "" : "disabled"}>日志</button>
            ${Object.entries(actionNames).map(([action, label]) => `<button class="button button-small ${action === "start" ? "" : "button-danger"}" type="button" data-docker-action="${action}" data-container-id="${h(item.id)}" data-container-name="${h(item.name)}" data-focus-key="${action}-${h(item.id)}" ${writable && (action === "start" ? startable : running) ? "" : "disabled"}>${label}</button>`).join("")}
          </div></td></tr>`;
      }).join(""), "Docker 容器", "引擎中没有容器。");
    }
    if (tab === "images") return capNotice + table(["镜像 ID", "标签", "大小", "创建时间"], rows.map((item) => `<tr><td class="mono wrap-cell">${h(text(item.id))}</td><td class="wrap-cell">${h(Array.isArray(item.tags) && item.tags.length ? item.tags.join("\n") : "无标签")}</td><td>${h(formatBytes(item.size))}</td><td>${h(formatDate(item.created))}</td></tr>`).join(""), "Docker 镜像", "引擎中没有镜像。");
    if (tab === "volumes") return capNotice + table(["卷名称", "驱动", "挂载点"], rows.map((item) => `<tr><td class="mono wrap-cell">${h(text(item.name))}</td><td>${h(text(item.driver))}</td><td class="mono wrap-cell">${h(text(item.mountpoint))}</td></tr>`).join(""), "Docker 卷", "引擎中没有卷。");
    return capNotice + table(["网络名称", "ID", "驱动", "范围"], rows.map((item) => `<tr><td>${h(text(item.name))}</td><td class="mono wrap-cell">${h(text(item.id))}</td><td>${h(text(item.driver))}</td><td>${h(text(item.scope))}</td></tr>`).join(""), "Docker 网络", "引擎中没有网络。");
  }

  function renderTasks(tasks) {
    return table(["任务 / 目标", "操作", "状态", "时间", "结果"], tasks.map((task) => {
      const statusName = Object.hasOwn(taskNames, task.status) ? taskNames[task.status] : `未知（${text(task.status, "未提供")}）`;
      const tone = task.status === "succeeded" ? "success" : task.status === "failed" ? "error" : "neutral";
      const errorText = typeof task.error === "string" ? task.error : typeof task.error?.message === "string" ? task.error.message : "";
      return `<tr><td class="mono wrap-cell">${h(text(task.id))}<small title="${h(task.target)}">${h(text(task.target))}</small></td><td>${h(Object.hasOwn(actionNames, task.action) ? actionNames[task.action] : text(task.action, "未知操作"))}</td><td>${badge(statusName, tone)}</td><td>创建 ${h(formatDate(task.createdAt))}${task.startedAt ? `<small>开始 ${h(formatDate(task.startedAt))}</small>` : ""}${task.finishedAt ? `<small>结束 ${h(formatDate(task.finishedAt))}</small>` : ""}</td><td class="wrap-cell">${h(errorText || (task.status === "succeeded" ? "操作已完成" : task.status === "failed" ? "执行失败，后端未提供原因" : "—"))}</td></tr>`;
    }).join(""), "Docker 任务", "没有保留的任务记录。");
  }

  function openLogs(id) {
    if (!/^[a-f0-9]{64}$/i.test(id)) return showToast("容器 ID 无效，不能读取日志。");
    const modal = openDialog("容器日志", loading("正在读取有界日志…"), '<button class="button" type="button" data-action="refresh-dialog">重新读取</button><button class="button" type="button" data-action="close-dialog">关闭</button>');
    modal.load = async (signal) => {
      const data = await API.containerLogs(id, signal);
      if (typeof data.text !== "string" || typeof data.truncated !== "boolean") throw invalid();
      const bounded = data.text.slice(0, 131072);
      return `<p class="mono path-caption">${h(id)}</p><p class="muted">固定尾部日志，最多 128 KiB；不持续跟随。</p>${data.truncated || bounded.length !== data.text.length ? notice("日志已截断，仅显示有界尾部内容。", "warning") : ""}${bounded ? `<pre class="code-preview log-preview">${h(bounded)}</pre>` : empty("本次读取没有日志内容。")}`;
    };
    void loadDialog(modal);
  }

  function confirmContainerAction(id, action, name) {
    if (!state.dockerStatus?.writeEnabled || !Object.hasOwn(actionNames, action) || !/^[a-f0-9]{64}$/i.test(id)) return showToast("当前不允许此容器操作，请刷新 Docker 状态。");
    const context = state.route;
    const consequence = action === "start" ? "将运行该容器已有的工作负载。" : "将中断容器中正在运行的服务。";
    openConfirm(`${actionNames[action]}容器`, `<p>目标：<strong>${h(text(name, id))}</strong></p><p class="mono path-caption">${h(id)}</p>${notice(`${consequence}确认后提交后台任务，不会自动重试。`, "warning")}`, `确认${actionNames[action]}`, async (signal) => {
      const result = await API.containerAction(id, action, signal);
      if (!result.task || typeof result.task.id !== "string" || !Object.hasOwn(taskNames, result.task.status)) throw invalid("后端没有返回有效任务，请先读取任务列表确认操作结果。");
      if (!current(context)) return;
      closeDialog(true);
      showToast(`任务已接收：${result.task.id}（${taskNames[result.task.status]}）。请查看任务最终结果。`);
      context.interval = 3000;
      await refreshRoute(context);
    });
  }

  // Account forms never persist passwords, enrollment secrets, or recovery codes.
  function renderAccountPage(context) {
    setHTML(main, `${pageHeading("个人与安全", "管理管理员凭据、双重验证、会话和审计记录。", refreshButton())}<div id="route-message"></div>
      <div id="account-main">${loading("正在读取账户…")}</div>
      <section class="panel section-card"><h2>登录会话</h2><p class="panel-subtitle">撤销当前会话将立即返回登录。IP 与客户端信息由后端报告。</p><div id="account-sessions">${loading()}</div></section>
      <section class="panel section-card"><h2>安全审计</h2><p class="panel-subtitle">后端保留的近期事件，最新记录在前。</p><div id="account-audit">${loading()}</div></section>`);
    context.refresh = (signal) => Promise.all([
      renderSection(context, "account-main", () => API.account(signal), (data) => {
        if (typeof data.username !== "string" || typeof data.twoFactorEnabled !== "boolean") throw invalid();
        state.accountData = data;
        state.accountDirty = false;
        state.totpPending = false;
        document.getElementById("account-username").textContent = data.username;
        document.getElementById("header-username").textContent = data.username;
        return renderAccountForms(data);
      }),
      renderSection(context, "account-sessions", () => API.sessions(signal), (data) => renderSessions(requireList(data, "sessions", 128))),
      renderSection(context, "account-audit", () => API.audit(signal), (data) => renderAudit(requireList(data, "events", 300))),
    ]);
  }

  function currentPasswordField() {
    return '<label class="field"><span>当前密码</span><input name="currentPassword" type="password" required autocomplete="current-password" /></label>';
  }

  function codeField(label = "验证码或一次性恢复码", onlyTOTP = false) {
    return `<label class="field"><span>${h(label)}</span><input name="code" type="text" required maxlength="${onlyTOTP ? 6 : 128}" ${onlyTOTP ? 'inputmode="numeric" pattern="[0-9]{6}"' : 'autocapitalize="none"'} autocomplete="one-time-code" spellcheck="false" /></label>`;
  }

  function renderAccountForms(data) {
    return `<div class="account-grid"><section class="panel section-card"><h2>用户名与密码</h2><p class="body-copy">修改后撤销所有登录会话。新密码留空表示不修改密码。</p>
      <form data-form="credentials" class="form-stack">
        <label class="field"><span>用户名</span><input name="username" value="${h(data.username)}" required minlength="3" maxlength="32" autocomplete="username" autocapitalize="none" spellcheck="false" /></label>
        ${currentPasswordField()}${data.twoFactorEnabled ? codeField() : ""}
        <label class="field"><span>新密码 <small>可选</small></span><input name="newPassword" type="password" autocomplete="new-password" /><small>12–128 个 UTF-8 字节。</small></label>
        <label class="field"><span>确认新密码</span><input name="confirmPassword" type="password" autocomplete="new-password" /></label>
        <div data-form-error></div><button class="button button-primary" type="submit">保存并重新登录</button>
      </form></section>
      <section class="panel section-card"><h2>双重验证</h2><div id="totp-section">${renderTOTPSection(data)}</div>
        <p class="panel-subtitle">会话最长有效期：${number(data.sessionTTLMinutes) ? `${h(data.sessionTTLMinutes)} 分钟` : "未提供"}；后端还可能提前结束闲置会话。</p>
      </section></div>`;
  }

  function renderTOTPSection(data) {
    if (data.twoFactorEnabled) {
      return `${badge("已启用 TOTP", "success")}<p class="body-copy">停用需要当前密码及有效验证码或一次性恢复码。操作后撤销所有会话。</p>
        <form data-form="totp-disable" class="form-stack">${currentPasswordField()}${codeField()}<div data-form-error></div><button class="button button-danger" type="submit">停用双重验证</button></form>`;
    }
    return `${badge("未启用")}<p class="body-copy">生成临时密钥后，在验证器中手动添加账户。验证成功时只显示一次恢复码。</p>
      <form data-form="totp-begin" class="form-stack">${currentPasswordField()}<div data-form-error></div><button class="button" type="submit">开始设置 TOTP</button></form>`;
  }

  function renderEnrollment(data) {
    if (typeof data.secret !== "string" || typeof data.otpauthUri !== "string" || !data.secret || !data.otpauthUri) throw invalid();
    return `${notice("临时密钥会过期，仅生成密钥不会启用双重验证。请在本页完成验证。", "warning")}
      <h3>手动密钥</h3><pre class="code-preview secret-preview">${h(data.secret)}</pre><h3>验证器配置 URI</h3><pre class="code-preview secret-preview">${h(data.otpauthUri)}</pre>
      <p class="field-help">手动录入上方密钥；URI 仅以文本显示，不发送给第三方二维码服务。</p>
      <form data-form="totp-enable" class="form-stack">${currentPasswordField()}${codeField("验证器当前的 6 位验证码", true)}<div data-form-error></div>
        <div class="form-actions"><button class="button button-primary" type="submit">验证并启用</button><button class="button" type="button" data-action="cancel-totp">取消显示</button></div>
      </form>`;
  }

  function showRecoveryCodes(codes) {
    if (!Array.isArray(codes) || !codes.length || codes.some((code) => typeof code !== "string")) throw invalid("双重验证响应缺少恢复码。请确认账户状态后重新登录。");
    leavePanel();
    state.recoveryVisible = true;
    document.title = "服务器面板 · 保存恢复码";
    setHTML(gateContent, `<h1 id="gate-title">保存一次性恢复码</h1>${notice("双重验证已启用，原有会话已撤销。这些恢复码只显示一次；每个恢复码只能使用一次。请安全离线保存。", "warning")}
      <pre class="code-preview recovery-codes">${h(codes.join("\n"))}</pre>
      <label class="checkbox-field"><input id="recovery-saved" type="checkbox" />我已安全保存恢复码</label>
      <button class="button button-primary" type="button" data-action="finish-recovery" disabled>已保存，重新登录</button>`);
    gateContent.focus({ preventScroll: true });
  }

  function renderSessions(sessions) {
    return table(["会话", "时间", "客户端", "操作"], sessions.map((session) => `<tr><td class="mono wrap-cell">${h(text(session.id))}${session.current === true ? `<small>${badge("当前会话", "success")}</small>` : ""}</td>
      <td>创建 ${h(formatDate(session.createdAt))}<small>最近活动 ${h(formatDate(session.lastSeenAt))}</small><small>过期 ${h(formatDate(session.expiresAt))}</small></td>
      <td class="wrap-cell">${h(text(session.ip))}<small>${h(text(session.userAgent))}</small></td><td><button class="button button-small button-danger" type="button" data-revoke-session="${h(session.id)}" data-current-session="${session.current === true}" ${typeof session.id === "string" && session.id ? "" : "disabled"}>撤销${session.current === true ? "当前会话" : ""}</button></td></tr>`).join(""), "登录会话", "后端没有返回会话记录。");
  }

  function renderAudit(events) {
    return table(["时间", "操作", "详情", "来源 IP"], events.map((event) => `<tr><td>${h(formatDate(event.time))}</td><td class="mono wrap-cell">${h(text(event.action))}</td><td class="wrap-cell">${h(text(event.detail, "—"))}</td><td class="mono">${h(text(event.ip, "—"))}</td></tr>`).join(""), "安全审计", "没有保留的审计事件。");
  }

  function confirmRevokeSession(id, isCurrent) {
    const context = state.route;
    openConfirm("撤销登录会话", `<p class="mono path-caption">${h(id)}</p>${notice(isCurrent ? "这是当前会话。撤销后立即返回登录。" : "该会话将失去访问权限。", "warning")}`, "确认撤销", async (signal) => {
      const result = requireOK(await API.revokeSession(id, signal));
      if (!current(context) || signal.aborted) return;
      if (typeof result.reauthenticate !== "boolean") throw invalid();
      if (result.reauthenticate) forceLogin("会话已撤销，请重新登录。");
      else if (current(context)) {
        closeDialog(true);
        showToast("会话已撤销。");
        await Promise.all([
          renderSection(context, "account-sessions", () => API.sessions(context.signal), (data) => renderSessions(requireList(data, "sessions", 128))),
          renderSection(context, "account-audit", () => API.audit(context.signal), (data) => renderAudit(requireList(data, "events", 300))),
        ]);
      }
    });
  }

  // All form handlers capture input before disabling controls, then release it after use.
  async function submitForm(form, data, intent) {
    if (form.dataset.busy === "true") return;
    form.dataset.busy = "true";
    form.setAttribute("aria-busy", "true");
    const controls = Array.from(form.querySelectorAll("input, select, button, textarea"));
    const previouslyDisabled = controls.map((control) => control.disabled);
    controls.forEach((control) => { control.disabled = true; });
    const errorTarget = form.querySelector("[data-form-error]");
    setHTML(errorTarget, "");
    const read = (name) => String(data.get(name) ?? "");
    const context = state.route;
    const signal = context?.signal;
    try {
      switch (form.dataset.form) {
        case "setup":
        case "login": {
          const setup = form.dataset.form === "setup";
          const body = { username: read("username").trim(), password: read("password") };
          if (setup) {
            validatePassword(body.password, read("confirmPassword"));
            body.token = read("token").trim();
          } else if (read("code").trim()) body.code = read("code").trim();
          await securityChange(async () => {
            const result = setup ? await API.setup(body, state.authController?.signal) : await API.login(body, state.authController?.signal);
            enterPanel(result);
          });
          break;
        }
        case "credentials": {
          const body = { currentPassword: read("currentPassword") };
          const username = read("username").trim();
          if (username !== state.accountData?.username) body.username = username;
          if (read("newPassword")) {
            validatePassword(read("newPassword"), read("confirmPassword"));
            body.newPassword = read("newPassword");
          } else if (read("confirmPassword")) throw new API.Error("请填写新密码，或清空确认新密码。", "password_mismatch");
          if (!body.username && !body.newPassword) throw new API.Error("用户名与密码没有变更。", "no_changes");
          if (read("code").trim()) body.code = read("code").trim();
          await securityChange(async () => {
            const result = requireOK(await API.updateAccount(body, signal));
            if (result.reauthenticate !== true) throw invalid();
            forceLogin("账户凭据已更新，所有会话已撤销。请使用新的凭据登录。");
          });
          break;
        }
        case "totp-begin": {
          const result = await API.beginTOTP({ currentPassword: read("currentPassword") }, signal);
          if (current(context)) {
            const content = renderEnrollment(result);
            state.totpPending = true;
            setHTML("totp-section", content);
          }
          break;
        }
        case "totp-enable": {
          await securityChange(async () => {
            const result = requireOK(await API.enableTOTP({ currentPassword: read("currentPassword"), code: read("code").trim() }, signal));
            if (result.reauthenticate !== true) throw invalid();
            showRecoveryCodes(result.recoveryCodes);
          });
          break;
        }
        case "totp-disable": {
          const password = read("currentPassword");
          const code = read("code").trim();
          openConfirm("停用双重验证", notice("停用后仅密码即可登录，所有现有会话将被撤销。", "warning"), "确认停用", async (dialogSignal) => {
            await securityChange(async () => {
              const result = requireOK(await API.disableTOTP({ currentPassword: password, code }, dialogSignal));
              if (result.reauthenticate !== true) throw invalid();
              forceLogin("双重验证已停用，所有会话已撤销。请重新登录。");
            });
          });
          form.reset();
          break;
        }
        case "sources": {
          const config = sourceConfig(data);
          if (intent === "preview") {
            const result = await API.previewSources(config, signal);
            if (current(context)) setHTML("source-preview", renderSourcePreview(result));
          } else {
            if (state.sourceRevision === null) throw new API.Error("请先读取当前配置版本。", "revision_missing");
            const revision = state.sourceRevision;
            openConfirm("保存源配置模板", notice("只把配置模板保存到面板数据，不切换系统仓库、不安装软件。软件页仍使用主机现有可信源。", "warning"), "确认保存模板", async (dialogSignal) => {
              const result = await API.saveSources(revision, config, dialogSignal);
              if (current(context)) {
                validateSources(result);
                state.sourceRevision = result.revision;
                state.sourceDirty = false;
                closeDialog(true);
                setHTML("sources-content", renderSourcesForm(result));
                setHTML("route-message", notice("设置已保存到面板数据。未修改系统源文件、安装软件或重启 Docker。", "success"));
              }
            });
          }
          break;
        }
      }
    } catch (error) {
      if (!aborted(error) && form.isConnected) {
        setHTML(errorTarget, `${errorMarkup(error)}${error?.status === 409 && form.dataset.form === "sources" ? notice("配置版本发生冲突，本页草稿未保存。请保留需要的内容，再点“重新读取”获取新版本；不会自动覆盖其他会话的修改。", "warning") : ""}`);
      }
    } finally {
      controls.forEach((control, index) => { control.disabled = previouslyDisabled[index]; });
      delete form.dataset.busy;
      form.removeAttribute("aria-busy");
    }
  }

  // Dialog reads have a shorter lifetime than route reads; closing a dialog aborts them.
  function openDialog(title, content, footer = '<button class="button" type="button" data-action="close-dialog">关闭</button>') {
    closeDialog(true);
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
    const controller = new AbortController();
    const modal = { controller, signal: controller.signal, trigger: document.activeElement, busy: false, confirm: null, load: null, reading: false };
    state.modal = modal;
    document.getElementById("dialog-title").textContent = title;
    setHTML("dialog-content", content);
    setHTML("dialog-footer", footer);
    document.getElementById("dialog-close").disabled = false;
    dialog.showModal();
    updateRefreshLabel();
    return modal;
  }

  function closeDialog(force = false) {
    const modal = state.modal;
    if (modal?.busy && !force) return;
    state.modal = null;
    if (modal) {
      modal.controller.abort();
      modal.confirm = null;
      modal.load = null;
      modal.editor = null;
      modal.site = null;
      modal.context = null;
    }
    if (dialog.open) dialog.close();
    setHTML("dialog-content", "");
    setHTML("dialog-footer", "");
    if (modal?.trigger?.isConnected && !modal.trigger.closest("[inert]")) modal.trigger.focus({ preventScroll: true });
    if (state.route) schedulePoll(state.route);
  }

  async function loadDialog(modal) {
    if (state.modal !== modal || modal.reading || !modal.load) return;
    modal.reading = true;
    dialog.querySelector('[data-action="refresh-dialog"]')?.setAttribute("disabled", "");
    try {
      const content = await modal.load(modal.signal);
      if (state.modal === modal && !modal.signal.aborted) setHTML("dialog-content", content);
    } catch (error) {
      if (!aborted(error) && state.modal === modal) setHTML("dialog-content", errorMarkup(error));
    } finally {
      modal.reading = false;
      if (state.modal === modal) dialog.querySelector('[data-action="refresh-dialog"]')?.removeAttribute("disabled");
    }
  }

  function openConfirm(title, content, label, work) {
    const modal = openDialog(title, `${content}<div id="dialog-error"></div>`, `<button class="button" type="button" data-action="close-dialog">取消</button><button class="button button-danger" type="button" data-action="confirm-dialog">${h(label)}</button>`);
    modal.confirm = work;
  }

  async function confirmDialog() {
    const modal = state.modal;
    if (!modal?.confirm || modal.busy) return;
    modal.busy = true;
    dialog.querySelectorAll("button").forEach((button) => { button.disabled = true; });
    setHTML("dialog-error", "");
    try {
      await modal.confirm(modal.signal);
    } catch (error) {
      if (!aborted(error) && state.modal === modal) setHTML("dialog-error", errorMarkup(error));
    } finally {
      if (state.modal === modal) {
        modal.busy = false;
        dialog.querySelectorAll("button").forEach((button) => { button.disabled = false; });
      }
    }
  }

  function showPreferences() {
    openDialog("显示设置", `<p class="body-copy">仅在当前浏览器保存布局密度，不修改服务器设置。</p><label class="field"><span>信息密度</span><select id="density-select">${options([["comfortable", "舒适"], ["compact", "紧凑"]], document.body.dataset.density === "compact" ? "compact" : "comfortable")}</select></label>`);
  }

  function requestRefresh() {
    const context = state.route;
    if (!context || context.refreshing || main.querySelector('form[data-busy="true"]')) return;
    if ((state.page === "sources" && state.sourceDirty) || (state.page === "account" && (state.accountDirty || state.totpPending))) {
      openConfirm("重新读取", notice("重新读取将清除本页尚未提交的输入和临时密钥显示。", "warning"), "放弃输入并重新读取", async () => {
        closeDialog(true);
        setHTML("route-message", "");
        await refreshRoute(context);
      });
    } else {
      setHTML("route-message", "");
      void refreshRoute(context);
    }
  }

  function updateSidebarAccess() {
    sidebar.inert = mobileQuery.matches && !document.body.classList.contains("sidebar-open");
  }

  function closeSidebar(restoreFocus = true) {
    const wasOpen = document.body.classList.contains("sidebar-open");
    document.body.classList.remove("sidebar-open");
    overlay.hidden = true;
    menuButton.setAttribute("aria-expanded", "false");
    menuButton.setAttribute("aria-label", "打开导航");
    updateSidebarAccess();
    if (wasOpen && restoreFocus) menuButton.focus();
  }

  async function handleAction(action, target) {
    switch (action) {
      case "retry-auth": await checkAuth(); break;
      case "refresh": requestRefresh(); break;
      case "check-update": await checkUpdates(target); break;
      case "preferences": showPreferences(); break;
      case "close-dialog": closeDialog(); break;
      case "confirm-dialog": await confirmDialog(); break;
      case "refresh-dialog": if (state.modal) await loadDialog(state.modal); break;
      case "app-operation": confirmAppAction(target.dataset.appId, target.dataset.operation); break;
      case "show-job": showJob(target.dataset.jobId); break;
      case "close-job-detail": {
        const context = state.route;
        if (context) {
          context.jobDetailController?.abort();
          context.jobId = "";
          context.jobDetailSequence = (context.jobDetailSequence || 0) + 1;
        }
        state.jobSelection = "";
        setHTML("job-detail", "");
        break;
      }
      case "create-site": openSiteForm(); break;
      case "edit-site": openSiteForm(target.dataset.siteId); break;
      case "site-details": openSiteDetails(target.dataset.siteId); break;
      case "toggle-site": confirmSiteChange(target.dataset.siteId); break;
      case "delete-site": confirmSiteChange(target.dataset.siteId, true); break;
      case "site-tls": openSiteTLS(target.dataset.siteId); break;
      case "site-files": selectSiteFiles(target.dataset.siteId); break;
      case "site-directory": moveSiteDirectory(target.dataset.path); break;
      case "new-site-text": openSiteText("", true); break;
      case "edit-site-text": openSiteText(target.dataset.path); break;
      case "upload-site-file": openSiteUpload(); break;
      case "close-site-files": {
        const context = state.route;
        context?.fileController?.abort();
        if (context) context.siteFiles = null;
        setHTML("site-files", "");
        break;
      }
      case "site-files-next":
      case "site-files-previous": {
        const context = state.route;
        const nav = context?.siteFiles;
        if (!nav?.data) return;
        const next = action === "site-files-next";
        if (next ? nav.data.nextOffset === null : !nav.previous.length) return;
        const previous = [...nav.previous];
        const offset = next ? nav.data.nextOffset : previous.pop();
        if (next) previous.push(nav.offset);
        context.siteFiles = { id: nav.id, path: nav.path, offset, previous: previous.slice(-64), data: null };
        await loadSiteFiles(context, context.signal);
        break;
      }
      case "cancel-totp":
        state.totpPending = false;
        if (state.accountData) setHTML("totp-section", renderTOTPSection(state.accountData));
        break;
      case "finish-recovery":
        if (!document.getElementById("recovery-saved")?.checked) return;
        state.recoveryVisible = false;
        forceLogin("恢复码不再显示。请使用密码及验证码登录。");
        break;
      case "files-next":
        if (state.files.data?.nextOffset == null) return;
        state.files.previous = [...state.files.previous, state.files.offset].slice(-64);
        state.files.offset = state.files.data.nextOffset;
        state.files.query = "";
        renderPage();
        break;
      case "files-previous":
        if (!state.files.previous.length) return;
        state.files.offset = state.files.previous.pop();
        state.files.query = "";
        renderPage();
        break;
      case "logout": {
        if (!state.authenticated || target.disabled) return;
        target.disabled = true;
        try {
          const context = state.route;
          requireOK(await API.logout(context?.signal));
          if (current(context)) forceLogin("已退出登录。");
        } catch (error) {
          if (!aborted(error) && state.authenticated) showToast(error instanceof API.Error ? error.message : "退出失败，请重试。");
        } finally { target.disabled = false; }
        break;
      }
    }
  }

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.dataset.form) return;
    event.preventDefault();
    if (state.authChanging || form.dataset.busy === "true" || !form.reportValidity()) return;
    const data = new FormData(form);
    if (["site-config", "site-content", "site-upload", "site-tls"].includes(form.dataset.form)) {
      void submitHostingForm(form, data);
      return;
    }
    void submitForm(form, data, event.submitter?.value || "save");
  });

  document.addEventListener("click", (event) => {
    const target = event.target.closest("button, a");
    if (!target || target.disabled) return;
    if (state.authChanging) {
      event.preventDefault();
      return;
    }
    if (target.classList.contains("skip-link")) {
      event.preventDefault();
      (state.authenticated ? main : gateContent).focus();
      return;
    }
    if (target.matches('a[href^="#"]') && (state.authChanging || state.recoveryVisible)) {
      event.preventDefault();
      if (state.authChanging) showToast("正在更新登录或安全状态，请等待返回结果。");
      return;
    }
    if (target.dataset.action) void handleAction(target.dataset.action, target).catch((error) => {
      if (!aborted(error)) showToast(error instanceof API.Error ? error.message : "操作未完成，请重新读取状态。");
    });
    if (Object.hasOwn(target.dataset, "directory")) openDirectory(target.dataset.directory);
    if (target.dataset.previewFile) openFilePreview(target.dataset.previewFile);
    if (target.dataset.containerLogs) openLogs(target.dataset.containerLogs);
    if (target.dataset.dockerAction) confirmContainerAction(target.dataset.containerId, target.dataset.dockerAction, target.dataset.containerName);
    if (target.dataset.revokeSession) confirmRevokeSession(target.dataset.revokeSession, target.dataset.currentSession === "true");
    if (target.dataset.dockerTab && Object.hasOwn(dockerTabs, target.dataset.dockerTab) && target.dataset.dockerTab !== state.dockerTab) {
      state.dockerTab = target.dataset.dockerTab;
      renderPage();
      document.getElementById(`docker-tab-${state.dockerTab}`)?.focus({ preventScroll: true });
    }
    if (target.dataset.page === state.page) closeSidebar();
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (target.id === "file-search") {
      state.files.query = target.value;
      setHTML("file-table", renderFileRows());
    }
    if (target.closest('form[data-form="sources"]')) {
      state.sourceDirty = true;
      const dirty = document.getElementById("source-dirty");
      if (dirty) dirty.textContent = "有未保存的修改";
      setHTML("source-preview", "");
    }
    if (target.closest('form[data-form="credentials"], form[data-form^="totp-"]')) state.accountDirty = true;
  });

  document.addEventListener("change", (event) => {
    const target = event.target;
    if (target.id === "density-select") {
      const density = target.value === "compact" ? "compact" : "comfortable";
      document.body.dataset.density = density;
      try { localStorage.setItem("panel-density", density); } catch { /* Storage is optional. */ }
    }
    if (target.id === "recovery-saved") {
      gateContent.querySelector('[data-action="finish-recovery"]').disabled = !target.checked;
    }
    const siteForm = target.closest('form[data-form="site-config"]');
    if (siteForm && siteForm.dataset.busy !== "true" && target.name === "type") updateSiteTypeFields(siteForm);
    const form = target.closest('form[data-form="sources"]');
    if (form && form.dataset.busy !== "true") {
      if (target.name === "os") {
        const codes = codenames(target.value);
        const codename = form.elements.codename;
        codename.innerHTML = options(codes, codes.includes(codename.value) ? codename.value : codes[0]);
      }
      updateSourceFields(form);
    }
  });

  menuButton.addEventListener("click", () => {
    if (document.body.classList.contains("sidebar-open")) return closeSidebar();
    document.body.classList.add("sidebar-open");
    overlay.hidden = false;
    sidebar.inert = false;
    menuButton.setAttribute("aria-expanded", "true");
    menuButton.setAttribute("aria-label", "关闭导航");
    sidebar.querySelector('[aria-current="page"]')?.focus();
  });
  overlay.addEventListener("click", () => closeSidebar());
  mobileQuery.addEventListener("change", () => closeSidebar(false));

  document.addEventListener("keydown", (event) => {
    if (event.target.matches('[role="tab"][data-docker-tab]') && ["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const tabs = Object.keys(dockerTabs);
      let index = tabs.indexOf(state.dockerTab);
      if (event.key === "Home") index = 0;
      else if (event.key === "End") index = tabs.length - 1;
      else index = (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length;
      document.getElementById(`docker-tab-${tabs[index]}`)?.click();
      return;
    }
    if (!document.body.classList.contains("sidebar-open") || dialog.open) return;
    if (event.key === "Escape") closeSidebar();
    if (event.key === "Tab") {
      const links = Array.from(sidebar.querySelectorAll("a, button:not(:disabled)"));
      const first = links[0];
      const last = links[links.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    }
  });

  document.getElementById("dialog-close").addEventListener("click", () => closeDialog());
  dialog.addEventListener("cancel", (event) => { event.preventDefault(); closeDialog(); });
  dialog.addEventListener("click", (event) => {
    if (event.target !== dialog) return;
    const bounds = dialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) closeDialog();
  });

  window.addEventListener("hashchange", () => {
    if (state.authChanging) {
      history.replaceState(null, "", `#${state.page}`);
      showToast("安全操作正在提交，暂不切换页面。");
      return;
    }
    renderPage(true);
  });
  window.addEventListener("panel:unauthorized", () => {
    if (state.authChanging) state.authLost = true;
    else if (state.authenticated && !state.recoveryVisible) forceLogin();
  });
  document.addEventListener("visibilitychange", () => {
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
    const context = state.route;
    if (document.hidden) {
      if (context?.interval) context.readController?.abort();
      context?.jobDetailController?.abort();
      context?.fileController?.abort();
    }
    updateRefreshLabel();
    if (!document.hidden && context?.interval) void refreshRoute(context, true);
  });
  window.addEventListener("beforeunload", (event) => {
    if (state.authChanging || state.recoveryVisible) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
  window.addEventListener("pagehide", () => {
    state.authController?.abort();
    stopRoute();
  });
  window.addEventListener("pageshow", (event) => {
    if (event.persisted && !state.recoveryVisible) void checkAuth();
  });

  try {
    if (localStorage.getItem("panel-density") === "compact") document.body.dataset.density = "compact";
  } catch { /* Credentials and secrets are never stored here. */ }
  updateSidebarAccess();
  void checkAuth();
})();
