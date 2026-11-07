(() => {
  "use strict";

  class APIError extends Error {
    constructor(message, code = "unknown_error", status = 0) {
      super(message);
      this.name = "APIError";
      this.code = code;
      this.status = status;
    }
  }

  let csrfToken = "";
  const publicPaths = new Set([
    "/api/auth/status",
    "/api/auth/setup",
    "/api/auth/login",
  ]);

  // Session cookies are HttpOnly. Only the CSRF token lives in this page's memory.
  async function request(path, { method = "GET", body, signal, binary = false } = {}) {
    const mutation = method !== "GET";
    const headers = { Accept: "application/json" };
    if (mutation) {
      headers["Content-Type"] = binary ? "application/octet-stream" : "application/json";
      if (binary) headers["X-Confirm-Operation"] = "true";
      if (!publicPaths.has(path)) {
        if (!csrfToken) {
          throw new APIError("会话校验信息缺失，请重新登录。", "csrf_missing", 403);
        }
        headers["X-CSRF-Token"] = csrfToken;
      }
    }

    const controller = new AbortController();
    let timedOut = false;
    const abort = () => controller.abort();
    if (signal?.aborted) controller.abort();
    else signal?.addEventListener("abort", abort, { once: true });
    const timeout = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, 20000);

    try {
      const response = await fetch(path, {
        method,
        headers,
        body: mutation ? (binary ? body : JSON.stringify(body ?? {})) : undefined,
        credentials: "same-origin",
        mode: "same-origin",
        cache: "no-store",
        redirect: "error",
        signal: controller.signal,
      });

      if (response.status === 401 && !publicPaths.has(path)) {
        csrfToken = "";
        window.dispatchEvent(new CustomEvent("panel:unauthorized"));
      }

      const contentType = response.headers.get("Content-Type") || "";
      if (!contentType.includes("application/json") && !contentType.includes("+json")) {
        throw new APIError(
          "未收到后端 API 响应。请启动面板后端，并通过其 HTTP(S) 地址访问；静态预览不能登录或读取服务器数据。",
          "backend_missing",
          response.status,
        );
      }

      let payload;
      try {
        payload = await response.json();
      } catch {
        throw new APIError("后端返回了无法解析的 JSON 响应。", "invalid_response", response.status);
      }
      if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
        throw new APIError("后端响应格式不符合接口契约。", "invalid_response", response.status);
      }
      if (!response.ok) {
        const detail = payload.error;
        const message = typeof detail?.message === "string"
          ? detail.message
          : `请求失败（HTTP ${response.status}），请检查后端状态。`;
        const code = typeof detail?.code === "string" ? detail.code : "http_error";
        throw new APIError(message, code, response.status);
      }
      return payload;
    } catch (error) {
      if (error instanceof APIError) throw error;
      if (signal?.aborted) throw new DOMException("请求已取消", "AbortError");
      const outcome = mutation ? " 操作结果尚未确认，请先重新读取状态，不要直接重复提交。" : "";
      if (timedOut) {
        throw new APIError(`后端请求超时。${outcome}`, "request_timeout");
      }
      throw new APIError(
        `无法连接后端。请确认面板服务已启动，并通过同源 HTTP(S) 地址访问。${outcome}`,
        "backend_unreachable",
      );
    } finally {
      clearTimeout(timeout);
      signal?.removeEventListener("abort", abort);
    }
  }

  const get = (path, signal) => request(path, { signal });
  const write = (path, method, body, signal) => request(path, { method, body, signal });
  const part = (value) => encodeURIComponent(String(value));

  window.PanelAPI = Object.freeze({
    Error: APIError,
    setSession(status) {
      if (status?.authenticated !== true || typeof status.csrfToken !== "string" || !status.csrfToken) {
        throw new APIError("已登录响应缺少 CSRF 校验信息。", "invalid_response");
      }
      csrfToken = status.csrfToken;
    },
    clearSession() { csrfToken = ""; },

    authStatus: (signal) => get("/api/auth/status", signal),
    setup: (body, signal) => write("/api/auth/setup", "POST", body, signal),
    login: (body, signal) => write("/api/auth/login", "POST", body, signal),
    logout: (signal) => write("/api/auth/logout", "POST", {}, signal),
    account: (signal) => get("/api/account", signal),
    updateAccount: (body, signal) => write("/api/account", "PATCH", body, signal),
    beginTOTP: (body, signal) => write("/api/account/totp/begin", "POST", body, signal),
    enableTOTP: (body, signal) => write("/api/account/totp/enable", "POST", body, signal),
    disableTOTP: (body, signal) => write("/api/account/totp/disable", "POST", body, signal),
    sessions: (signal) => get("/api/account/sessions", signal),
    revokeSession: (id, signal) => write(`/api/account/sessions/${part(id)}`, "DELETE", {}, signal),
    audit: (signal) => get("/api/account/audit", signal),

    capabilities: (signal) => get("/api/capabilities", signal),
    system: (signal) => get("/api/system", signal),
    services: (signal) => get("/api/services", signal),
    updateStatus: (signal) => get("/api/update", signal),
    checkUpdate: (signal) => write("/api/update/check", "POST", {}, signal),
    files: (path, offset, signal) => get(`/api/files?${new URLSearchParams({ path, offset: String(offset) })}`, signal),
    previewFile: (path, signal) => get(`/api/files/preview?${new URLSearchParams({ path })}`, signal),

    management: (signal) => get("/api/management", signal),
    apps: (signal) => get("/api/apps", signal),
    appAction: (id, action, signal) => {
      if (!["nginx", "php-fpm", "mariadb", "redis"].includes(id) || !["install", "start", "stop", "restart"].includes(action)) {
        return Promise.reject(new APIError("未知的软件或操作。", "invalid_app_action"));
      }
      return write(`/api/apps/${part(id)}/${action}`, "POST", { confirmed: true }, signal);
    },
    jobs: (signal) => get("/api/jobs", signal),
    job: (id, signal) => get(`/api/jobs/${part(id)}`, signal),
    sites: (signal) => get("/api/sites", signal),
    site: (id, signal) => get(`/api/sites/${part(id)}`, signal),
    createSite: (site, signal) => write("/api/sites", "POST", { confirmed: true, site }, signal),
    updateSite: (id, revision, site, signal) => write(`/api/sites/${part(id)}`, "PATCH", { confirmed: true, revision, site }, signal),
    deleteSite: (id, revision, signal) => write(`/api/sites/${part(id)}`, "DELETE", { confirmed: true, revision }, signal),
    siteTLS: (id, revision, certificate, privateKey, signal) => write(`/api/sites/${part(id)}/tls`, "POST", { confirmed: true, revision, certificate, privateKey }, signal),
    siteFiles: (id, path, offset, signal) => get(`/api/sites/${part(id)}/files?${new URLSearchParams({ path, offset: String(offset) })}`, signal),
    siteContent: (id, path, signal) => get(`/api/sites/${part(id)}/content?${new URLSearchParams({ path })}`, signal),
    saveSiteContent: (id, path, content, sha256, signal) => write(`/api/sites/${part(id)}/content`, "PUT", { confirmed: true, path, content, sha256 }, signal),
    uploadSiteFile: (id, path, file, signal) => {
      if (!(file instanceof Blob) || file.size > 2097152) return Promise.reject(new APIError("单文件上传不得超过 2 MiB。", "upload_too_large"));
      return request(`/api/sites/${part(id)}/upload?${new URLSearchParams({ path })}`, { method: "POST", body: file, signal, binary: true });
    },

    sources: (signal) => get("/api/sources", signal),
    saveSources: (revision, config, signal) => write("/api/sources", "PUT", { revision, config }, signal),
    previewSources: (config, signal) => write("/api/sources/preview", "POST", { config }, signal),
    docker: (signal) => get("/api/docker", signal),
    dockerList: (tab, signal) => {
      if (!["containers", "images", "volumes", "networks"].includes(tab)) {
        return Promise.reject(new APIError("未知的 Docker 列表。", "invalid_tab"));
      }
      return get(`/api/docker/${tab}`, signal);
    },
    containerLogs: (id, signal) => get(`/api/docker/containers/${part(id)}/logs`, signal),
    containerAction: (id, action, signal) => {
      if (!/^[a-f0-9]{64}$/i.test(id) || !["start", "stop", "restart"].includes(action)) {
        return Promise.reject(new APIError("容器 ID 或操作不合法。", "invalid_container_action"));
      }
      return write(`/api/docker/containers/${part(id)}/${action}`, "POST", { confirmed: true }, signal);
    },
    tasks: (signal) => get("/api/tasks", signal),
  });
})();
