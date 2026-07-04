import type { FormEvent } from "react";
import { useEffect, useMemo, useState } from "react";

type Overview = {
  activeUsers: number;
  channels: number;
  requestsToday: number;
  totalBalance: number;
  successRate: number;
};

type User = {
  id: string;
  name: string;
  email: string;
  role: "admin" | "user";
  status: "active" | "disabled" | "limited" | "overdue";
  balance: number;
  requestsToday: number;
  totalRequests: number;
  createdAt: string;
  lastLoginAt: string;
  note: string;
};

type ApiKey = {
  id: string;
  userId: string;
  name: string;
  prefix: string;
  status: string;
  createdAt: string;
  lastUsedAt: string;
  requestCount: number;
};

type Channel = {
  id: string;
  name: string;
  provider: string;
  baseUrl: string;
  status: string;
  priority: number;
  weight: number;
  models: string[];
};

type ChannelPatch = Partial<Channel> & {
  upstreamApiKey?: string;
};

type ModelCreate = {
  id: string;
  name: string;
  vendor: string;
  aliases: string[];
  category: string;
  description: string;
  price: string;
  context: string;
};

type ChannelSyncResult = {
  channel: Channel;
  models: string[];
  addedModels: ModelItem[];
};

type ModelItem = {
  id: string;
  name: string;
  vendor: string;
  aliases: string[];
  category: string;
  description: string;
  price: string;
  context: string;
  status: "available" | "limited" | "disabled";
  recommended: boolean;
};

type RequestLog = {
  id: string;
  userId: string;
  apiKeyPrefix: string;
  model: string;
  channel: string;
  status: "success" | "failed";
  cost: number;
  latencyMs: number;
  errorCode?: string;
  createdAt: string;
};

type UserDetail = {
  user: User;
  apiKeys: ApiKey[];
  logs: RequestLog[];
};

type DiscordSettings = {
  enabled: boolean;
  clientId: string;
  clientSecretSet: boolean;
  redirectUri: string;
  allowedGuildId: string;
  allowedRoleId: string;
  authSuccessUrl: string;
  sessionTtlHours: number;
};

type AuthSession = {
  id: string;
  provider: string;
  userId: string;
  username: string;
  avatar: string;
  role: "admin" | "user";
  expiresAt: string;
};

type AuthStatus = {
  initialized: boolean;
  authenticated: boolean;
  registrationEnabled: boolean;
  registrationMode: RegistrationMode;
  discordEnabled: boolean;
  session: AuthSession | null;
};

type RegistrationMode = "username" | "email" | "discord";

type AuthSettings = {
  registrationEnabled: boolean;
  registrationMode: RegistrationMode;
};

type AccountProfile = {
  id: string;
  userId: string;
  username: string;
  email: string;
  discordUserId: string;
  role: "admin" | "user";
};

class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type IconName =
  | "home"
  | "users"
  | "key"
  | "models"
  | "route"
  | "logs"
  | "settings"
  | "search"
  | "copy"
  | "ban"
  | "check"
  | "moon"
  | "sun"
  | "plus";

const iconPaths: Record<IconName, string> = {
  home: "M3 10.5 12 3l9 7.5V21a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1z",
  users: "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75",
  key: "M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.78 7.78 5.5 5.5 0 0 1 7.78-7.78ZM14 8l7-7M21 8h-5V3",
  models: "M12 2 4 6v12l8 4 8-4V6zM4 6l8 4 8-4M12 10v12",
  route: "M4 19a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM20 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM7 16h3a4 4 0 0 0 4-4V8h3",
  logs: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M8 13h8M8 17h6",
  settings: "M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7ZM19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 8.92 4a1.65 1.65 0 0 0 1-1.51V2a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9c.14.31.39.57.71.71.23.1.49.18.8.2H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z",
  search: "M21 21l-4.35-4.35M10.5 18a7.5 7.5 0 1 1 0-15 7.5 7.5 0 0 1 0 15Z",
  copy: "M8 8h11a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1ZM4 16H3a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h11a1 1 0 0 1 1 1v1",
  ban: "M4.93 4.93 19.07 19.07M22 12A10 10 0 1 1 2 12a10 10 0 0 1 20 0Z",
  check: "M20 6 9 17l-5-5",
  moon: "M21 12.8A8.5 8.5 0 1 1 11.2 3 6.5 6.5 0 0 0 21 12.8Z",
  sun: "M12 4V2M12 22v-2M4.93 4.93 3.52 3.52M20.48 20.48l-1.41-1.41M4 12H2M22 12h-2M4.93 19.07l-1.41 1.41M20.48 3.52l-1.41 1.41M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
  plus: "M12 5v14M5 12h14"
};

function Icon({ name }: { name: IconName }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="icon">
      <path d={iconPaths[name]} />
    </svg>
  );
}

async function fetchJson<T>(url: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set("Content-Type", "application/json");
  const adminToken = window.sessionStorage.getItem("catieapi-admin-token");
  if (adminToken) headers.set("Authorization", `Bearer ${adminToken}`);
  const response = await fetch(url, {
    credentials: "include",
    ...init,
    headers
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw new ApiError(response.status, payload?.error?.message || `Request failed: ${response.status}`);
  }
  return response.json();
}

const navItems = [
  { id: "overview", label: "概览", icon: "home" },
  { id: "users", label: "用户", icon: "users" },
  { id: "keys", label: "密钥", icon: "key" },
  { id: "models", label: "模型", icon: "models" },
  { id: "channels", label: "渠道", icon: "route" },
  { id: "logs", label: "日志", icon: "logs" },
  { id: "settings", label: "设置", icon: "settings" }
] as const;

const providerOptions = [
  { value: "openai", label: "OpenAI" },
  { value: "anthropic", label: "Anthropic / Claude" },
  { value: "google", label: "Google Gemini" },
  { value: "deepseek", label: "DeepSeek" },
  { value: "openrouter", label: "OpenRouter" },
  { value: "groq", label: "Groq" },
  { value: "siliconflow", label: "SiliconFlow" },
  { value: "moonshot", label: "Moonshot" },
  { value: "compatible", label: "OpenAI Compatible" }
];

function formatDate(value: string) {
  if (!value) return "未使用";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(date);
}

function statusLabel(status: string) {
  const labels: Record<string, string> = {
    active: "正常",
    disabled: "禁用",
    limited: "受限",
    overdue: "欠费",
    healthy: "正常",
    standby: "备用",
    available: "Available",
    success: "成功",
    failed: "失败"
  };
  return labels[status] || status;
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  document.body.removeChild(textarea);
}

function currentOrigin() {
  return window.location.origin;
}

function defaultDiscordRedirectUri() {
  return `${currentOrigin()}/api/auth/discord/callback`;
}

function defaultAuthSuccessUrl() {
  return `${currentOrigin()}/`;
}

function withBrowserDiscordDefaults(settings: DiscordSettings): DiscordSettings {
  return {
    ...settings,
    redirectUri: settings.redirectUri && !settings.redirectUri.includes("localhost") ? settings.redirectUri : defaultDiscordRedirectUri(),
    authSuccessUrl: settings.authSuccessUrl && !settings.authSuccessUrl.includes("localhost") ? settings.authSuccessUrl : defaultAuthSuccessUrl(),
    sessionTtlHours: settings.sessionTtlHours || 168
  };
}

function normalizeRegistrationMode(value?: string): RegistrationMode {
  if (value === "email" || value === "discord") return value;
  return "username";
}

function usernameFromEmail(email: string) {
  const local = email.split("@")[0] || "user";
  const safe = local.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^[-_]+|[-_]+$/g, "");
  return (safe.length >= 3 ? safe : "user").slice(0, 24);
}

function App() {
  const [surface, setSurface] = useState<"home" | "auth" | "console" | "account">("home");
  const [authMode, setAuthMode] = useState<"setup" | "login" | "register">("login");
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);
  const [active, setActive] = useState<(typeof navItems)[number]["id"]>("overview");
  const [theme, setTheme] = useState<"light" | "dark">(() => {
    const saved = window.localStorage.getItem("catieapi-theme");
    if (saved === "light" || saved === "dark") return saved;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  });
  const [density, setDensity] = useState<"comfortable" | "compact">("comfortable");
  const [overview, setOverview] = useState<Overview | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [models, setModels] = useState<ModelItem[]>([]);
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [query, setQuery] = useState("");
  const [selectedUserId, setSelectedUserId] = useState("");
  const [selectedUser, setSelectedUser] = useState<UserDetail | null>(null);
  const [toast, setToast] = useState("");
  const [consoleReady, setConsoleReady] = useState(false);

  const filteredUsers = useMemo(() => {
    const value = query.trim().toLowerCase();
    if (!value) return users;
    return users.filter((user) => `${user.id} ${user.name} ${user.email}`.toLowerCase().includes(value));
  }, [query, users]);

  async function loadAll() {
    const [overviewData, usersData, channelsData, modelsData, logsData] = await Promise.all([
      fetchJson<Overview>("/api/overview"),
      fetchJson<{ users: User[] }>("/api/users"),
      fetchJson<{ channels: Channel[] }>("/api/channels"),
      fetchJson<{ models: ModelItem[] }>("/api/models"),
      fetchJson<{ logs: RequestLog[] }>("/api/logs")
    ]);
    setOverview(overviewData);
    setUsers(usersData.users);
    setChannels(channelsData.channels);
    setModels(modelsData.models);
    setLogs(logsData.logs);
    setSelectedUserId((current) => {
      if (current && usersData.users.some((user) => user.id === current)) return current;
      return usersData.users[0]?.id || "";
    });
    if (usersData.users.length === 0) {
      setSelectedUser(null);
    }
    setConsoleReady(true);
  }

  async function loadUser(id: string) {
    const data = await fetchJson<UserDetail>(`/api/users/${id}`);
    setSelectedUser(data);
  }

  async function updateUser(id: string, patch: Partial<User>) {
    const data = await fetchJson<{ user: User }>(`/api/users/${id}`, {
      method: "PATCH",
      body: JSON.stringify(patch)
    });
    setUsers((current) => current.map((user) => (user.id === id ? data.user : user)));
    setSelectedUser((current) => (current?.user.id === id ? { ...current, user: data.user } : current));
    setToast("已更新用户");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function createAPIKeyForUser(userId: string) {
    const data = await fetchJson<{ apiKey: ApiKey; secret: string }>(`/api/users/${userId}/api-keys`, {
      method: "POST",
      body: JSON.stringify({ name: "Console Key" })
    });
    if (selectedUser?.user.id === userId) {
      setSelectedUser({ ...selectedUser, apiKeys: [...selectedUser.apiKeys, data.apiKey] });
    }
    await copyText(data.secret);
    setToast("新 Key 已创建并复制，请立即保存");
    window.setTimeout(() => setToast(""), 2400);
  }

  async function updateChannel(id: string, patch: ChannelPatch) {
    const data = await fetchJson<{ channel: Channel }>(`/api/channels/${id}`, {
      method: "PATCH",
      body: JSON.stringify(patch)
    });
    setChannels((current) => current.map((channel) => (channel.id === id ? data.channel : channel)));
    setToast("渠道已更新");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function syncChannelModels(id: string) {
    const data = await fetchJson<ChannelSyncResult>(`/api/channels/${id}/sync-models`, {
      method: "POST",
      body: JSON.stringify({})
    });
    setChannels((current) => current.map((channel) => (channel.id === id ? data.channel : channel)));
    if (data.addedModels.length > 0) {
      setModels((current) => {
        const seen = new Set(current.map((model) => model.id.toLowerCase()));
        return [...current, ...data.addedModels.filter((model) => !seen.has(model.id.toLowerCase()))];
      });
    }
    setToast(data.models.length ? `已拉取 ${data.models.length} 个模型` : "上游没有返回模型");
    window.setTimeout(() => setToast(""), 2200);
  }

  async function createChannel() {
    const data = await fetchJson<{ channel: Channel }>("/api/channels", {
      method: "POST",
      body: JSON.stringify({})
    });
    setChannels((current) => [...current, data.channel]);
    setActive("channels");
    setToast("已创建禁用的新渠道，请补充上游地址后启用");
    window.setTimeout(() => setToast(""), 2400);
  }

  async function createModel(model: ModelCreate) {
    const data = await fetchJson<{ model: ModelItem }>("/api/models", {
      method: "POST",
      body: JSON.stringify(model)
    });
    setModels((current) => [...current, data.model]);
    setToast("模型已添加");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function copyAndToast(value: string, label = "已复制") {
    await copyText(value);
    setToast(label);
    window.setTimeout(() => setToast(""), 1600);
  }

  function handleLoadError(error: unknown, fallback: string) {
    if (error instanceof ApiError && error.status === 401) {
      setConsoleReady(false);
      setAuthMode("login");
      setSurface("auth");
      return;
    }
    setToast(fallback);
  }

  async function openPortal() {
    try {
      const status = await fetchJson<AuthStatus>("/api/auth/status");
      setAuthStatus(status);
      if (!status.initialized) {
        setAuthMode("setup");
        setSurface("auth");
        return;
      }
      if (!status.authenticated) {
        setAuthMode("login");
        setSurface("auth");
        return;
      }
      setSurface(status.session?.role === "admin" ? "console" : "account");
    } catch (error) {
      handleLoadError(error, "认证状态加载失败");
    }
  }

  async function logout() {
    await fetchJson("/api/auth/logout", { method: "POST" });
    window.sessionStorage.removeItem("catieapi-admin-token");
    setAuthStatus(null);
    setSurface("home");
  }

  useEffect(() => {
    if (surface !== "console") return;
    setConsoleReady(false);
    loadAll().catch((error) => handleLoadError(error, "加载数据失败"));
  }, [surface]);

  useEffect(() => {
    if (surface !== "console" || !consoleReady) return;
    loadUser(selectedUserId).catch((error) => handleLoadError(error, "加载用户详情失败"));
  }, [selectedUserId, surface, consoleReady]);

  useEffect(() => {
    window.localStorage.setItem("catieapi-theme", theme);
  }, [theme]);

  useEffect(() => {
    window.scrollTo({ top: 0, left: 0 });
  }, [surface, active]);

  if (surface === "home") {
    return <PublicHome theme={theme} setTheme={setTheme} enterConsole={openPortal} />;
  }

  if (surface === "auth") {
    return (
      <AuthScreen
        theme={theme}
        mode={authMode}
        status={authStatus}
        setTheme={setTheme}
        setMode={setAuthMode}
        goHome={() => setSurface("home")}
        onAuthenticated={(session) => {
          setAuthStatus((current) => current ? { ...current, authenticated: true, initialized: true, session } : null);
          setSurface(session.role === "admin" ? "console" : "account");
        }}
      />
    );
  }

  if (surface === "account") {
    return <AccountHome theme={theme} setTheme={setTheme} goHome={() => setSurface("home")} openLogin={openPortal} />;
  }

  return (
    <div className="app-shell" data-theme={theme} data-density={density}>
      <aside className="sidebar">
        <div className="ios-window-dots" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
        <div className="brand">
          <div className="brand-mark">C</div>
          <div>
            <strong>CatieAPI</strong>
            <span>聚合网关</span>
          </div>
        </div>
        <nav>
          {navItems.map((item) => (
            <button key={item.id} className={active === item.id ? "nav-item active" : "nav-item"} onClick={() => setActive(item.id)}>
              <Icon name={item.icon} />
              <span className="nav-label">{item.label}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span>Gateway</span>
          <strong>Online</strong>
        </div>
      </aside>

      <main className="content">
        <header className="topbar">
          <div>
            <p className="eyebrow">Admin Console</p>
            <h1>{navItems.find((item) => item.id === active)?.label}</h1>
          </div>
          <div className="topbar-actions">
            <SegmentedControl
              value={density}
              options={[
                { value: "comfortable", label: "舒适" },
                { value: "compact", label: "紧凑" }
              ]}
              onChange={(value) => setDensity(value as "comfortable" | "compact")}
            />
            <button className="theme-toggle" aria-label="切换暗色模式" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
              <Icon name={theme === "dark" ? "sun" : "moon"} />
              <span>{theme === "dark" ? "浅色" : "暗色"}</span>
            </button>
          <button className="primary-button" onClick={() => loadAll().catch((error) => handleLoadError(error, "刷新失败"))}>
            刷新
          </button>
          <button className="secondary-button home-link" onClick={() => setSurface("home")}>
            首页
          </button>
          <button className="secondary-button" onClick={logout}>
            退出
          </button>
        </div>
      </header>

        {active === "overview" && (
          <OverviewView
            overview={overview}
            channels={channels}
            logs={logs}
            onNavigate={(target) => {
              setActive(target);
              if (target === "channels" && channels.length === 0) setToast("渠道页可以创建第一个上游");
              if (target === "logs" && logs.length === 0) setToast("暂无异常日志");
              window.setTimeout(() => setToast(""), 1800);
            }}
          />
        )}
        {active === "users" && (
          <UsersView
            users={filteredUsers}
            query={query}
            selectedUser={selectedUser}
            onQuery={setQuery}
            onSelect={setSelectedUserId}
            onUpdate={updateUser}
            onCreateKey={createAPIKeyForUser}
            onOpenRegistration={() => {
              setActive("settings");
              setToast("在账号与注册里开放注册，用户即可自助创建账号");
              window.setTimeout(() => setToast(""), 2400);
            }}
          />
        )}
        {active === "keys" && <KeysView selectedUser={selectedUser} onCopy={copyAndToast} onCreateKey={createAPIKeyForUser} />}
        {active === "models" && <ModelsView models={models} onCopy={copyAndToast} onCreate={createModel} />}
        {active === "channels" && <ChannelsView channels={channels} onUpdate={updateChannel} onCreate={createChannel} onSyncModels={syncChannelModels} />}
        {active === "logs" && <LogsView logs={logs} />}
        {active === "settings" && <SettingsView models={models} channels={channels} />}
      </main>

      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}

function AuthScreen({
  theme,
  mode,
  status,
  setTheme,
  setMode,
  goHome,
  onAuthenticated
}: {
  theme: "light" | "dark";
  mode: "setup" | "login" | "register";
  status: AuthStatus | null;
  setTheme: (theme: "light" | "dark") => void;
  setMode: (mode: "setup" | "login" | "register") => void;
  goHome: () => void;
  onAuthenticated: (session: AuthSession) => void;
}) {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [discordUserId, setDiscordUserId] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [message, setMessage] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const isSetup = mode === "setup";
  const isRegister = mode === "register";
  const registrationMode = normalizeRegistrationMode(status?.registrationMode);
  const isEmailRegister = isRegister && registrationMode === "email";
  const isDiscordRegister = isRegister && registrationMode === "discord";

  async function submit() {
    if (isDiscordRegister) return;
    if ((isSetup || isRegister) && password !== confirmPassword) {
      setMessage("两次输入的密码不一致");
      return;
    }
    setSubmitting(true);
    setMessage("");
    try {
      const endpoint = isSetup ? "/api/auth/setup" : isRegister ? "/api/auth/register" : "/api/auth/login";
      const registerUsername = isEmailRegister ? usernameFromEmail(email) : username;
      const body = mode === "login"
        ? { identifier: username, password }
        : { username: registerUsername, password, displayName, email, discordUserId: isSetup ? discordUserId : "" };
      const data = await fetchJson<{ session: AuthSession }>(endpoint, {
        method: "POST",
        body: JSON.stringify(body)
      });
      onAuthenticated(data.session);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "操作失败");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-page" data-theme={theme}>
      <header className="auth-topbar">
        <button className="auth-brand" onClick={goHome}>
          <span className="brand-mark">C</span>
          <strong>CatieAPI</strong>
        </button>
        <button className="theme-toggle" aria-label="切换暗色模式" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
          <Icon name={theme === "dark" ? "sun" : "moon"} />
          <span>{theme === "dark" ? "浅色" : "暗色"}</span>
        </button>
      </header>

      <section className="auth-stage">
        <div className="auth-intro">
          <span>{isSetup ? "First Run" : "Welcome Back"}</span>
          <h1>{isSetup ? "初始化 CatieAPI" : isRegister ? "创建账号" : "登录"}</h1>
          <p>{isSetup ? "创建第一个管理员账号，完成后即可进入控制台。" : "使用你的 CatieAPI 账号继续。"}</p>
        </div>

        <form
          className="auth-form"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          {isDiscordRegister ? (
            <div className="auth-discord-register">
              <strong>使用 Discord 创建账号</strong>
              <span>继续后会按站点设置校验服务器和身份组。</span>
              {status?.discordEnabled ? (
                <a className="discord-login-button" href="/api/auth/discord/start">继续使用 Discord</a>
              ) : (
                <div className="auth-message">管理员还没有启用 Discord 登录</div>
              )}
            </div>
          ) : (
            <>
              {mode !== "login" && (
                <label>
                  <span>显示名称</span>
                  <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} autoComplete="name" placeholder="Catie" />
                </label>
              )}
              {!isEmailRegister && (
                <label>
                  <span>{mode === "login" ? "账号或邮箱" : "账号"}</span>
                  <input
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    autoComplete="username"
                    placeholder={mode === "login" ? "输入账号或邮箱" : "3-32 位字母、数字、_ 或 -"}
                  />
                </label>
              )}
              {(isSetup || isEmailRegister) && (
                <label>
                  <span>{isEmailRegister ? "邮箱" : "邮箱（可选）"}</span>
                  <input type="email" value={email} onChange={(event) => setEmail(event.target.value)} autoComplete="email" placeholder="name@example.com" />
                </label>
              )}
              {isSetup && (
                <label>
                  <span>Discord 用户 ID（可选）</span>
                  <input
                    inputMode="numeric"
                    autoComplete="off"
                    value={discordUserId}
                    onChange={(event) => setDiscordUserId(event.target.value)}
                    placeholder="绑定管理员 Discord 账号"
                  />
                </label>
              )}
              <label>
                <span>密码</span>
                <input
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete={mode === "login" ? "current-password" : "new-password"}
                  placeholder="至少 8 个字符"
                />
              </label>
              {mode !== "login" && (
                <label>
                  <span>确认密码</span>
                  <input
                    type="password"
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                    autoComplete="new-password"
                    placeholder="再次输入密码"
                  />
                </label>
              )}
              <div className="auth-message" role="status">{message}</div>
              <button className="primary-button auth-submit" type="submit" disabled={submitting}>
                {submitting ? "请稍候" : isSetup ? "创建管理员" : isRegister ? "注册" : "登录"}
              </button>
            </>
          )}

          {!isSetup && !isDiscordRegister && status?.discordEnabled && (
            <a className="discord-login-button" href="/api/auth/discord/start">使用 Discord 登录</a>
          )}
          {!isSetup && (
            <div className="auth-switch">
              {mode === "login" && status?.registrationEnabled ? (
                <button type="button" onClick={() => setMode("register")}>创建账号</button>
              ) : (
                <button type="button" onClick={() => setMode("login")}>返回登录</button>
              )}
            </div>
          )}
        </form>
      </section>
    </main>
  );
}

function AccountHome({
  theme,
  setTheme,
  goHome,
  openLogin
}: {
  theme: "light" | "dark";
  setTheme: (theme: "light" | "dark") => void;
  goHome: () => void;
  openLogin: () => void;
}) {
  const [data, setData] = useState<{ user: User; apiKeys: ApiKey[]; session: AuthSession } | null>(null);
  const [models, setModels] = useState<ModelItem[]>([]);
  const [newSecret, setNewSecret] = useState("");
  const [message, setMessage] = useState("");

  async function load() {
    try {
      const [account, catalog] = await Promise.all([
        fetchJson<{ user: User; apiKeys: ApiKey[]; session: AuthSession }>("/api/account/me"),
        fetchJson<{ models: ModelItem[] }>("/api/catalog/models")
      ]);
      setData(account);
      setModels(catalog.models);
    } catch {
      openLogin();
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function createKey() {
    try {
      const result = await fetchJson<{ secret: string }>("/api/account/api-keys", {
        method: "POST",
        body: JSON.stringify({ name: "My API Key" })
      });
      setNewSecret(result.secret);
      setMessage("新密钥只显示这一次");
      await load();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "创建密钥失败");
    }
  }

  async function logout() {
    await fetchJson("/api/auth/logout", { method: "POST" });
    goHome();
  }

  return (
    <main className="account-page" data-theme={theme}>
      <header className="account-topbar">
        <button className="auth-brand" onClick={goHome}>
          <span className="brand-mark">C</span>
          <strong>CatieAPI</strong>
        </button>
        <div className="account-actions">
          <button className="theme-toggle" aria-label="切换暗色模式" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
            <Icon name={theme === "dark" ? "sun" : "moon"} />
          </button>
          <button className="secondary-button" onClick={logout}>退出</button>
        </div>
      </header>

      <section className="account-content">
        <div className="account-heading">
          <div>
            <p className="eyebrow">My CatieAPI</p>
            <h1>{data?.user.name || "账户"}</h1>
          </div>
          <div className="account-balance">
            <span>余额</span>
            <strong>{data ? data.user.balance.toFixed(2) : "-"}</strong>
          </div>
        </div>

        <section className="account-section">
          <div className="account-section-title">
            <h2>API 密钥</h2>
            <button className="primary-button" onClick={createKey}>创建密钥</button>
          </div>
          {newSecret && <code className="one-time-secret">{newSecret}</code>}
          {message && <p className="account-message">{message}</p>}
          <div className="account-key-list">
            {data?.apiKeys.map((key) => (
              <div key={key.id}>
                <span><strong>{key.name}</strong><small>{key.prefix}...</small></span>
                <Badge tone={key.status}>{statusLabel(key.status)}</Badge>
              </div>
            ))}
            {data?.apiKeys.length === 0 && <div className="empty">还没有 API 密钥</div>}
          </div>
        </section>

        <section className="account-section">
          <div className="account-section-title"><h2>可用模型</h2></div>
          <div className="account-model-grid">
            {models.map((model) => (
              <article key={model.id}>
                <span>{model.vendor}</span>
                <strong>{model.name}</strong>
                <p>{model.description}</p>
                <code>{model.id}</code>
              </article>
            ))}
          </div>
        </section>
      </section>
    </main>
  );
}

function PublicHome({
  theme,
  setTheme,
  enterConsole
}: {
  theme: "light" | "dark";
  setTheme: (theme: "light" | "dark") => void;
  enterConsole: () => void;
}) {
  return (
    <main className="public-home" data-theme={theme}>
      <header className="home-topbar">
        <div className="home-brand">
          <div className="brand-mark">C</div>
          <div>
            <strong>CatieAPI</strong>
            <span>AI 聚合网关</span>
          </div>
        </div>
        <div className="home-actions">
          <button className="theme-toggle" aria-label="切换暗色模式" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
            <Icon name={theme === "dark" ? "sun" : "moon"} />
            <span>{theme === "dark" ? "浅色" : "暗色"}</span>
          </button>
          <button className="primary-button" onClick={enterConsole}>
            控制台
          </button>
        </div>
      </header>

      <section className="home-hero">
        <div className="home-copy">
          <span className="home-kicker">OpenAI Compatible Gateway</span>
          <h1>CatieAPI</h1>
          <p>给普通用户和开发者用的轻量 AI 聚合网关。管理模型渠道、API Key、额度和调用日志，不做臃肿后台。</p>
          <div className="home-cta">
            <button className="primary-button" onClick={enterConsole}>
              进入控制台
            </button>
            <a className="secondary-button" href="#features">
              了解功能
            </a>
          </div>
        </div>

        <div className="gateway-card">
          <div className="gateway-card-head">
            <span>Gateway Status</span>
            <div className="live-island">
              <div className="pulse-dot" />
              <span>Online</span>
            </div>
          </div>
          <div className="endpoint-box">
            <span>POST</span>
            <strong>/v1/chat/completions</strong>
          </div>
          <div className="home-flow">
            <div>认证</div>
            <div>额度</div>
            <div>路由</div>
            <div>响应</div>
          </div>
          <pre>{`curl /v1/chat/completions
  -H "Authorization: Bearer cat_..."
  -d '{"model":"你的模型ID"}'`}</pre>
        </div>
      </section>

      <section className="home-grid" id="features">
        <HomeFeature icon="key" title="Key 管理" text="创建、禁用、查看调用统计。" />
        <HomeFeature icon="models" title="模型目录" text="用户只看用途、价格、代称和调用 ID。" />
        <HomeFeature icon="users" title="用户额度" text="面向运营的余额、封禁和备注。" />
        <HomeFeature icon="logs" title="日志排障" text="请求、渠道、错误码集中查看。" />
      </section>
    </main>
  );
}

function HomeFeature({ icon, title, text }: { icon: IconName; title: string; text: string }) {
  return (
    <article className="home-feature">
      <Icon name={icon} />
      <strong>{title}</strong>
      <span>{text}</span>
    </article>
  );
}

function OverviewView({
  overview,
  channels,
  logs,
  onNavigate
}: {
  overview: Overview | null;
  channels: Channel[];
  logs: RequestLog[];
  onNavigate: (target: (typeof navItems)[number]["id"]) => void;
}) {
  const failedLogs = logs.filter((log) => log.status !== "success");
  return (
    <section className="page-stack">
      <div className="hero-strip">
        <div>
          <span>Live Gateway</span>
          <strong>CatieAPI 网关正在服务 {overview?.activeUsers ?? "-"} 个活跃用户</strong>
          <p>请求进入 CatieAPI 后，会按额度、模型和渠道状态自动选择最合适的上游。</p>
        </div>
        <div className="live-island">
          <div className="pulse-dot" />
          <span>在线</span>
        </div>
      </div>
      <div className="quick-actions" aria-label="快捷操作">
        <QuickAction icon="key" label="创建 Key" onClick={() => onNavigate("keys")} />
        <QuickAction icon="route" label="配置渠道" onClick={() => onNavigate("channels")} />
        <QuickAction icon="users" label="调整额度" onClick={() => onNavigate("users")} />
        <QuickAction icon="logs" label="查看异常" onClick={() => onNavigate("logs")} />
      </div>
      <div className="metrics-grid">
        <Metric label="活跃用户" value={overview?.activeUsers ?? "-"} />
        <Metric label="今日请求" value={overview?.requestsToday ?? "-"} />
        <Metric label="账户余额" value={overview ? overview.totalBalance.toFixed(2) : "-"} />
        <Metric label="成功率" value={overview ? `${overview.successRate}%` : "-"} />
      </div>
      <GatewayFlow />
      <div className="split-grid">
        <Panel title="渠道状态">
          {channels.length ? (
            channels.map((channel) => (
              <div className="list-row" key={channel.id}>
                <div>
                  <strong>{channel.name}</strong>
                  <span>{channel.models.join(", ") || "未绑定模型"}</span>
                </div>
                <Badge tone={channel.status}>{statusLabel(channel.status)}</Badge>
              </div>
            ))
          ) : (
            <Empty text="暂无渠道" />
          )}
        </Panel>
        <Panel title="最近请求">
          {logs.length ? (
            logs.slice(0, 4).map((log) => (
              <div className="list-row" key={log.id}>
                <div>
                  <strong>{log.model || "未知模型"}</strong>
                  <span>{log.id} · {formatDate(log.createdAt)}</span>
                </div>
                <Badge tone={log.status}>{statusLabel(log.status)}</Badge>
              </div>
            ))
          ) : (
            <Empty text={failedLogs.length ? "暂无最近请求" : "暂无请求"} />
          )}
        </Panel>
      </div>
    </section>
  );
}

function QuickAction({ icon, label, onClick }: { icon: IconName; label: string; onClick: () => void }) {
  return (
    <button className="quick-action" onClick={onClick}>
      <Icon name={icon} />
      <span>{label}</span>
    </button>
  );
}

function GatewayFlow() {
  const steps = [
    { label: "认证", detail: "校验 API Key" },
    { label: "额度", detail: "检查余额" },
    { label: "路由", detail: "选择渠道" },
    { label: "响应", detail: "返回结果" }
  ];

  return (
    <section className="flow-panel" aria-label="网关流转">
      <div className="flow-copy">
        <span>Request Flow</span>
        <strong>请求处理流程</strong>
      </div>
      <div className="flow-steps">
        {steps.map((step, index) => (
          <div className="flow-step" key={step.label}>
            <div className="flow-index">{index + 1}</div>
            <strong>{step.label}</strong>
            <span>{step.detail}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function UsersView({
  users,
  query,
  selectedUser,
  onQuery,
  onSelect,
  onUpdate,
  onCreateKey,
  onOpenRegistration
}: {
  users: User[];
  query: string;
  selectedUser: UserDetail | null;
  onQuery: (value: string) => void;
  onSelect: (id: string) => void;
  onUpdate: (id: string, patch: Partial<User>) => void;
  onCreateKey: (id: string) => void;
  onOpenRegistration: () => void;
}) {
  const pageSize = 25;
  const [page, setPage] = useState(1);
  const totalPages = Math.max(1, Math.ceil(users.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const pagedUsers = users.slice((safePage - 1) * pageSize, safePage * pageSize);

  useEffect(() => {
    setPage(1);
  }, [query]);

  return (
    <section className="users-layout">
      <Panel title="用户管理">
        <div className="panel-toolbar">
          <div className="search-box">
            <Icon name="search" />
            <input value={query} onChange={(event) => onQuery(event.target.value)} placeholder="搜索 ID、姓名或邮箱" />
          </div>
          <button className="icon-button" title="开放注册" onClick={onOpenRegistration}>
            <Icon name="plus" />
          </button>
        </div>
        <div className="user-summary-strip">
          <span><strong>{users.length}</strong> 匹配用户</span>
          <span><strong>{users.filter((user) => user.status === "active").length}</strong> 正常</span>
          <span><strong>{users.filter((user) => user.status === "disabled").length}</strong> 禁用</span>
          <span><strong>{users.reduce((sum, user) => sum + user.requestsToday, 0)}</strong> 今日请求</span>
        </div>
        <div className="table">
          <div className="table-head users-table">
            <span>用户</span>
            <span>状态</span>
            <span>余额</span>
            <span>今日</span>
          </div>
          {pagedUsers.map((user) => (
            <button className={selectedUser?.user.id === user.id ? "table-row users-table selected" : "table-row users-table"} key={user.id} onClick={() => onSelect(user.id)}>
              <span>
                <strong>{user.name}</strong>
                <small>{user.id} · {user.email || "未绑定邮箱"}</small>
              </span>
              <Badge tone={user.status}>{statusLabel(user.status)}</Badge>
              <span>{user.balance.toFixed(2)}</span>
              <span>{user.requestsToday}</span>
            </button>
          ))}
          {users.length === 0 && <Empty text="暂无用户" />}
        </div>
        {users.length > pageSize && (
          <div className="pagination-bar">
            <button className="secondary-button" disabled={safePage <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))}>上一页</button>
            <span>{safePage} / {totalPages}</span>
            <button className="secondary-button" disabled={safePage >= totalPages} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>下一页</button>
          </div>
        )}
      </Panel>

      <Panel title="用户详情">
        {selectedUser ? (
          <div className="detail-stack">
            <div className="user-hero">
              <div className="avatar">{selectedUser.user.name.slice(0, 1)}</div>
              <div>
                <h2>{selectedUser.user.name}</h2>
                <p>{selectedUser.user.email}</p>
              </div>
              <Badge tone={selectedUser.user.status}>{statusLabel(selectedUser.user.status)}</Badge>
            </div>

            <div className="settings-group">
              <Setting label="角色" value={selectedUser.user.role === "admin" ? "管理员" : "用户"} />
              <Setting label="余额" value={selectedUser.user.balance.toFixed(2)} />
              <Setting label="总请求" value={String(selectedUser.user.totalRequests)} />
              <Setting label="最后登录" value={formatDate(selectedUser.user.lastLoginAt)} />
              <Setting label="API 调用" value={selectedUser.user.status === "disabled" ? "关闭" : "允许"} switchOn={selectedUser.user.status !== "disabled"} />
            </div>

            <div className="action-row">
              <button className="secondary-button" onClick={() => onUpdate(selectedUser.user.id, { balance: Number((selectedUser.user.balance + 10).toFixed(2)) })}>
                额度 +10
              </button>
              <button className="secondary-button" onClick={() => onCreateKey(selectedUser.user.id)}>
                创建 Key
              </button>
              <button
                className="secondary-button"
                onClick={() =>
                  onUpdate(selectedUser.user.id, {
                    status: selectedUser.user.status === "disabled" ? "active" : "disabled"
                  })
                }
              >
                {selectedUser.user.status === "disabled" ? "解封" : "禁用"}
              </button>
            </div>

            <div>
              <h3>API Key</h3>
              {selectedUser.apiKeys.map((key) => (
                <div className="list-row" key={key.id}>
                  <div>
                    <strong>{key.name}</strong>
                    <span>{key.prefix}*** · {key.requestCount} 次</span>
                  </div>
                  <Badge tone={key.status}>{statusLabel(key.status)}</Badge>
                </div>
              ))}
              {selectedUser.apiKeys.length === 0 && <Empty text="暂无 API Key" />}
            </div>
          </div>
        ) : (
          <Empty text="请选择一个用户" />
        )}
      </Panel>
    </section>
  );
}

function KeysView({
  selectedUser,
  onCopy,
  onCreateKey
}: {
  selectedUser: UserDetail | null;
  onCopy: (value: string, label?: string) => void;
  onCreateKey: (id: string) => void;
}) {
  return (
    <Panel title="密钥管理">
      {selectedUser ? (
        <>
          <div className="panel-toolbar">
            <span className="muted-inline">{selectedUser.user.name}</span>
            <button className="primary-button" onClick={() => onCreateKey(selectedUser.user.id)}>
              创建 Key
            </button>
          </div>
          {selectedUser.apiKeys.length ? (
            selectedUser.apiKeys.map((key) => (
              <div className="list-row" key={key.id}>
                <div>
                  <strong>{key.name}</strong>
                  <span>{key.prefix}*** · 最后使用 {formatDate(key.lastUsedAt)}</span>
                </div>
                <button className="icon-button" title="复制前缀" onClick={() => onCopy(key.prefix, "Key 前缀已复制")}>
                  <Icon name="copy" />
                </button>
              </div>
            ))
          ) : (
            <Empty text="暂无密钥" />
          )}
        </>
      ) : (
        <Empty text="请选择一个用户" />
      )}
    </Panel>
  );
}

function ModelsView({ models, onCopy, onCreate }: { models: ModelItem[]; onCopy: (value: string, label?: string) => void; onCreate: (model: ModelCreate) => Promise<void> }) {
  const recommended = models.filter((model) => model.recommended);
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [vendor, setVendor] = useState("");
  const [aliases, setAliases] = useState("");
  const [description, setDescription] = useState("");
  const [message, setMessage] = useState("");

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const modelId = id.trim();
    if (!modelId) return;
    setMessage("");
    try {
      await onCreate({
        id: modelId,
        name: name.trim() || modelId,
        vendor: vendor.trim() || "Custom",
        aliases: aliases.split(",").map((alias) => alias.trim()).filter(Boolean),
        category: "通用",
        description: description.trim(),
        price: "自定义",
        context: "未配置上下文"
      });
      setId("");
      setName("");
      setVendor("");
      setAliases("");
      setDescription("");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "模型添加失败");
    }
  }

  return (
    <section className="models-page">
      <div className="model-hero">
        <div>
          <span>Model Catalog</span>
          <strong>Models</strong>
        </div>
      </div>

      <Panel title="新增模型">
        <form className="model-create-form" onSubmit={submit}>
          <input value={id} onChange={(event) => setId(event.target.value)} placeholder="模型 ID，例如 openai/gpt-4.1" />
          <input value={name} onChange={(event) => setName(event.target.value)} placeholder="显示名称（可选）" />
          <input value={vendor} onChange={(event) => setVendor(event.target.value)} placeholder="供应商（可选）" />
          <input value={aliases} onChange={(event) => setAliases(event.target.value)} placeholder="代称，多个用逗号分隔（可选）" />
          <input className="model-create-wide" value={description} onChange={(event) => setDescription(event.target.value)} placeholder="描述（可选）" />
          <button className="primary-button" type="submit">新增模型</button>
          <div className="model-create-message" role="status">{message}</div>
        </form>
      </Panel>

      {recommended.length > 0 && (
        <Panel title="推荐模型">
        <div className="model-grid">
          {recommended.map((model) => (
            <ModelCard key={model.id} model={model} featured onCopy={onCopy} />
          ))}
        </div>
        </Panel>
      )}

      <Panel title="全部模型">
        {models.length > 0 ? (
          <div className="model-list">
            {models.map((model) => (
              <ModelCard key={model.id} model={model} onCopy={onCopy} />
            ))}
          </div>
        ) : (
          <Empty text="暂无模型，请先添加你要开放给用户调用的模型 ID" />
        )}
      </Panel>
    </section>
  );
}

function ModelCard({ model, featured = false, onCopy }: { model: ModelItem; featured?: boolean; onCopy: (value: string, label?: string) => void }) {
  return (
    <article className={featured ? "model-card featured" : "model-card"}>
      <div className="model-card-head">
        <div>
          <strong>{model.name}</strong>
          <span>{model.vendor} · {model.category}</span>
        </div>
        <Badge tone={model.status}>{statusLabel(model.status)}</Badge>
      </div>
      <p>{model.description}</p>
      <div className="alias-row">
        {model.aliases.map((alias) => (
          <span key={alias}>{alias}</span>
        ))}
      </div>
      <div className="model-meta">
        <span>价格：{model.price}</span>
        <span>{model.context}</span>
      </div>
      <div className="model-id">
        <code>{model.id}</code>
        <button className="icon-button" title="复制模型 ID" onClick={() => onCopy(model.id, "模型 ID 已复制")}>
          <Icon name="copy" />
        </button>
      </div>
    </article>
  );
}

function ChannelsView({
  channels,
  onUpdate,
  onCreate,
  onSyncModels
}: {
  channels: Channel[];
  onUpdate: (id: string, patch: ChannelPatch) => Promise<void>;
  onCreate: () => void;
  onSyncModels: (id: string) => Promise<void>;
}) {
  return (
    <Panel title="渠道管理">
      <div className="panel-toolbar">
        <span className="muted-inline">新增渠道默认禁用，补充地址后再启用。</span>
        <button className="primary-button" onClick={onCreate}>
          新增渠道
        </button>
      </div>
      <div className="channels-stack">
        {channels.map((channel) => (
          <ChannelEditor key={channel.id} channel={channel} onUpdate={onUpdate} onSyncModels={onSyncModels} />
        ))}
        {channels.length === 0 && <Empty text="暂无渠道，先在后端添加渠道接口或导入配置" />}
      </div>
    </Panel>
  );
}

function ChannelEditor({
  channel,
  onUpdate,
  onSyncModels
}: {
  channel: Channel;
  onUpdate: (id: string, patch: ChannelPatch) => Promise<void>;
  onSyncModels: (id: string) => Promise<void>;
}) {
  const [provider, setProvider] = useState(channel.provider);
  const [baseUrl, setBaseUrl] = useState(channel.baseUrl);
  const [models, setModels] = useState(channel.models.join(", "));
  const [upstreamApiKey, setUpstreamApiKey] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    setProvider(channel.provider);
    setBaseUrl(channel.baseUrl);
    setModels(channel.models.join(", "));
    setUpstreamApiKey("");
  }, [channel.id, channel.provider, channel.baseUrl, channel.models]);

  async function save() {
    const patch: ChannelPatch = {
      provider,
      baseUrl,
      models: models
        .split(",")
        .map((model) => model.trim())
        .filter(Boolean)
    };
    if (upstreamApiKey.trim()) {
      patch.upstreamApiKey = upstreamApiKey.trim();
    }
    setBusy("save");
    try {
      await onUpdate(channel.id, patch);
      setUpstreamApiKey("");
    } finally {
      setBusy("");
    }
  }

  async function syncModels() {
    setBusy("sync");
    try {
      await save();
      await onSyncModels(channel.id);
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="channel-card">
      <div className="channel-card-head">
        <div>
          <strong>{channel.name}</strong>
          <span>{channel.baseUrl || "未配置上游地址"}</span>
        </div>
        <Badge tone={channel.status}>{statusLabel(channel.status)}</Badge>
      </div>

      <div className="provider-chip-grid" role="radiogroup" aria-label="供应商">
        {providerOptions.map((option) => (
          <button key={option.value} type="button" className={provider === option.value ? "selected" : ""} onClick={() => setProvider(option.value)}>
            {option.label}
          </button>
        ))}
      </div>

      <div className="channel-form-grid">
        <label className="channel-form-wide">
          <span>Base URL</span>
          <input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="https://provider.example/v1" />
        </label>
        <label>
          <span>优先级</span>
          <input value={channel.priority} readOnly />
        </label>
        <label className="channel-form-wide">
          <span>模型</span>
          <textarea value={models} onChange={(event) => setModels(event.target.value)} placeholder="先拉取上游模型，也可以手动补充，多个用逗号分隔" />
        </label>
        <label>
          <span>上游 Key</span>
          <input type="password" value={upstreamApiKey} onChange={(event) => setUpstreamApiKey(event.target.value)} placeholder="留空表示不修改" autoComplete="new-password" />
        </label>
      </div>

      <div className="channel-card-actions">
        <button className="secondary-button" onClick={syncModels} disabled={busy !== ""}>
          {busy === "sync" ? "拉取中" : "拉取模型"}
        </button>
        <button className="secondary-button" onClick={save} disabled={busy !== ""}>
          {busy === "save" ? "保存中" : "保存"}
        </button>
        <button className="status-button" onClick={() => onUpdate(channel.id, { status: channel.status === "disabled" ? "healthy" : "disabled" })}>
          <Badge tone={channel.status}>{channel.status === "disabled" ? "启用" : "禁用"}</Badge>
        </button>
      </div>
    </div>
  );
}

function LogsView({ logs }: { logs: RequestLog[] }) {
  return (
    <Panel title="调用日志">
      <div className="table">
        <div className="table-head logs-table">
          <span>请求</span>
          <span>模型</span>
          <span>渠道</span>
          <span>消耗</span>
          <span>状态</span>
        </div>
        {logs.map((log) => (
          <div className="table-row logs-table" key={log.id}>
            <span>
              <strong>{log.id}</strong>
              <small>{formatDate(log.createdAt)} · {log.latencyMs}ms</small>
            </span>
            <span>{log.model}</span>
            <span>{log.channel}</span>
            <span>{log.cost.toFixed(2)}</span>
            <Badge tone={log.status}>{statusLabel(log.status)}</Badge>
          </div>
        ))}
      </div>
    </Panel>
  );
}

function SettingsView({ models, channels }: { models: ModelItem[]; channels: Channel[] }) {
  const [discord, setDiscord] = useState<DiscordSettings | null>(null);
  const [registrationEnabled, setRegistrationEnabled] = useState<boolean | null>(null);
  const [registrationMode, setRegistrationMode] = useState<RegistrationMode>("username");
  const [account, setAccount] = useState<AccountProfile | null>(null);
  const [accountUsername, setAccountUsername] = useState("");
  const [accountDisplayName, setAccountDisplayName] = useState("");
  const [accountEmail, setAccountEmail] = useState("");
  const [discordUserId, setDiscordUserId] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);
  const [settingsTab, setSettingsTab] = useState("system");
  const defaultModel = models.find((model) => model.recommended && model.status === "available")?.id || models.find((model) => model.status === "available")?.id || "未配置";
  const activeChannels = channels.filter((channel) => channel.status !== "disabled").length;

  useEffect(() => {
    Promise.all([
      fetchJson<{ discord: DiscordSettings }>("/api/settings/discord"),
      fetchJson<{ auth: AuthSettings }>("/api/settings/auth"),
      fetchJson<{ account: AccountProfile; user: User }>("/api/account/me")
    ])
      .then(([discordData, authData, accountData]) => {
        setDiscord(withBrowserDiscordDefaults(discordData.discord));
        setRegistrationEnabled(authData.auth.registrationEnabled);
        setRegistrationMode(normalizeRegistrationMode(authData.auth.registrationMode));
        setAccount(accountData.account);
        setAccountUsername(accountData.account?.username || "");
        setAccountDisplayName(accountData.user.name || "");
        setAccountEmail(accountData.account?.email || "");
        setDiscordUserId(accountData.account?.discordUserId || "");
      })
      .catch(() => setMessage("Discord 配置加载失败"));
  }, []);

  async function saveAuthSettings(nextEnabled = registrationEnabled, nextMode = registrationMode) {
    if (nextEnabled === null) return;
    try {
      const data = await fetchJson<{ auth: AuthSettings }>("/api/settings/auth", {
        method: "PATCH",
        body: JSON.stringify({ registrationEnabled: nextEnabled, registrationMode: nextMode })
      });
      setRegistrationEnabled(data.auth.registrationEnabled);
      setRegistrationMode(normalizeRegistrationMode(data.auth.registrationMode));
      setMessage(data.auth.registrationEnabled ? "注册设置已保存" : "已关闭用户注册");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "注册设置保存失败");
    }
  }

  async function toggleRegistration() {
    if (registrationEnabled === null) return;
    saveAuthSettings(!registrationEnabled, registrationMode);
  }

  async function saveDiscordSettings() {
    if (!discord) return;
    setSaving(true);
    setMessage("");
    try {
      const nextDiscord = withBrowserDiscordDefaults(discord);
      const data = await fetchJson<{ discord: DiscordSettings }>("/api/settings/discord", {
        method: "PATCH",
        body: JSON.stringify({ ...nextDiscord, clientSecret })
      });
      setDiscord(withBrowserDiscordDefaults(data.discord));
      setClientSecret("");
      setMessage("Discord 配置已保存");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "保存失败，请检查填写内容");
    } finally {
      setSaving(false);
    }
  }

  async function saveAccountBinding() {
    try {
      const data = await fetchJson<{ account: AccountProfile }>("/api/account/profile", {
        method: "PATCH",
        body: JSON.stringify({
          username: accountUsername,
          displayName: accountDisplayName,
          email: accountEmail,
          discordUserId,
          currentPassword,
          newPassword
        })
      });
      setAccount(data.account);
      setAccountUsername(data.account.username);
      setAccountEmail(data.account.email || "");
      setDiscordUserId(data.account.discordUserId || "");
      setCurrentPassword("");
      setNewPassword("");
      setMessage("管理员账号已保存");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "账号设置保存失败");
    }
  }

  return (
    <div className="settings-layout">
      <div className="settings-tabs">
        {[
          { value: "system", label: "系统" },
          { value: "auth", label: "账号注册" },
          { value: "admin", label: "管理员" },
          { value: "discord", label: "Discord" }
        ].map((tab) => (
          <button key={tab.value} type="button" className={settingsTab === tab.value ? "selected" : ""} onClick={() => setSettingsTab(tab.value)}>
            {tab.label}
          </button>
        ))}
      </div>

      {settingsTab === "system" && (
      <Panel title="系统设置">
        <div className="settings-group">
          <Setting label="接口兼容" value="OpenAI API" />
          <Setting label="当前默认模型" value={defaultModel} />
          <Setting label="已配置渠道" value={`${channels.length} 个，${activeChannels} 个启用`} />
          <Setting label="可选供应商" value={`${providerOptions.length} 种`} />
        </div>
      </Panel>
      )}

      {settingsTab === "auth" && (
      <Panel title="账号与注册">
        <div className="settings-group">
          <div className="setting">
            <span>开放用户注册</span>
            <div className="setting-value">
              <strong>{registrationEnabled ? "启用" : "关闭"}</strong>
              <button
                type="button"
                className={registrationEnabled ? "ios-switch is-on" : "ios-switch"}
                aria-label={registrationEnabled ? "关闭用户注册" : "开放用户注册"}
                aria-pressed={Boolean(registrationEnabled)}
                onClick={toggleRegistration}
              >
                <span />
              </button>
            </div>
          </div>
          <div className="setting">
            <span>注册方式</span>
            <div className="registration-mode-control" role="group" aria-label="注册方式">
              {[
                { value: "username", label: "账号密码" },
                { value: "email", label: "邮箱" },
                { value: "discord", label: "Discord" }
              ].map((option) => (
                <button
                  key={option.value}
                  type="button"
                  className={registrationMode === option.value ? "selected" : ""}
                  onClick={() => saveAuthSettings(registrationEnabled ?? true, option.value as RegistrationMode)}
                >
                  {option.label}
                </button>
              ))}
            </div>
          </div>
        </div>
      </Panel>
      )}

      {settingsTab === "admin" && (
      <Panel title="管理员账号">
        <form
          className="discord-settings"
          onSubmit={(event) => {
            event.preventDefault();
            saveAccountBinding();
          }}
        >
          <div className="settings-form-grid">
            <label>
              <span>登录账号</span>
              <input value={accountUsername} onChange={(event) => setAccountUsername(event.target.value)} autoComplete="username" />
            </label>
            <label>
              <span>显示名称</span>
              <input value={accountDisplayName} onChange={(event) => setAccountDisplayName(event.target.value)} autoComplete="name" />
            </label>
            <label>
              <span>邮箱</span>
              <input type="email" value={accountEmail} onChange={(event) => setAccountEmail(event.target.value)} autoComplete="email" />
            </label>
            <label>
              <span>Discord 用户 ID（可选）</span>
              <input
                inputMode="numeric"
                autoComplete="off"
                value={discordUserId}
                onChange={(event) => setDiscordUserId(event.target.value)}
                placeholder={account?.discordUserId ? "已绑定" : "输入管理员的 Discord 用户 ID"}
              />
            </label>
            <label>
              <span>当前密码</span>
              <input
                type="password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
                autoComplete="current-password"
                placeholder="修改密码时填写"
              />
            </label>
            <label>
              <span>新密码</span>
              <input
                type="password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
                autoComplete="new-password"
                placeholder="留空表示不修改"
              />
            </label>
          </div>
          <div className="settings-save-row">
            <span role="status">{message}</span>
            <button className="primary-button" type="submit">保存账号</button>
          </div>
        </form>
      </Panel>
      )}

      {settingsTab === "discord" && (
      <Panel title="Discord 登录">
        {!discord ? (
          <div className="empty">正在读取配置</div>
        ) : (
          <form
            className="discord-settings"
            autoComplete="off"
            onSubmit={(event) => {
              event.preventDefault();
              saveDiscordSettings();
            }}
          >
            <div className="discord-toggle-row">
              <div>
                <strong>Discord 登录</strong>
                <span>{discord.enabled ? "已启用" : "未启用"}</span>
              </div>
              <button
                type="button"
                className={discord.enabled ? "ios-switch is-on" : "ios-switch"}
                aria-label={discord.enabled ? "停用 Discord 登录" : "启用 Discord 登录"}
                aria-pressed={discord.enabled}
                onClick={() => setDiscord({ ...discord, enabled: !discord.enabled })}
              >
                <span />
              </button>
            </div>

            <div className="settings-form-grid">
              <label>
                <span>Client ID</span>
                <input
                  inputMode="numeric"
                  autoComplete="off"
                  value={discord.clientId}
                  onChange={(event) => setDiscord({ ...discord, clientId: event.target.value })}
                  placeholder="100000000000000001"
                />
              </label>
              <label>
                <span>Client Secret</span>
                <input
                  type="password"
                  autoComplete="new-password"
                  value={clientSecret}
                  onChange={(event) => setClientSecret(event.target.value)}
                  placeholder={discord.clientSecretSet ? "已设置，留空表示不修改" : "粘贴 Discord Client Secret"}
                />
              </label>
              <label className="settings-form-wide">
                <span>回调地址</span>
                <input
                  type="url"
                  value={discord.redirectUri}
                  onChange={(event) => setDiscord({ ...discord, redirectUri: event.target.value })}
                  placeholder="https://你的域名/api/auth/discord/callback"
                />
              </label>
              <label>
                <span>服务器 ID</span>
                <input
                  inputMode="numeric"
                  value={discord.allowedGuildId}
                  onChange={(event) => setDiscord({ ...discord, allowedGuildId: event.target.value })}
                  placeholder="允许登录的服务器 ID"
                />
              </label>
              <label>
                <span>身份组 ID</span>
                <input
                  inputMode="numeric"
                  value={discord.allowedRoleId}
                  onChange={(event) => setDiscord({ ...discord, allowedRoleId: event.target.value })}
                  placeholder="允许登录的身份组 ID"
                />
              </label>
              <label className="settings-form-wide">
                <span>登录成功跳转地址</span>
                <input
                  type="url"
                  value={discord.authSuccessUrl}
                  onChange={(event) => setDiscord({ ...discord, authSuccessUrl: event.target.value })}
                  placeholder="https://你的域名/"
                />
              </label>
              <label>
                <span>登录有效期（小时）</span>
                <input
                  type="number"
                  min="1"
                  max="8760"
                  value={discord.sessionTtlHours}
                  onChange={(event) => setDiscord({ ...discord, sessionTtlHours: Number(event.target.value) })}
                />
              </label>
            </div>

            <div className="settings-save-row">
              <span role="status">{message}</span>
              <button className="primary-button" type="submit" disabled={saving}>
                {saving ? "保存中" : "保存配置"}
              </button>
            </div>
          </form>
        )}
      </Panel>
      )}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="panel">
      <div className="panel-title">
        <h2>{title}</h2>
      </div>
      {children}
    </section>
  );
}

function Badge({ tone, children }: { tone: string; children: React.ReactNode }) {
  return <span className={`badge tone-${tone}`}>{children}</span>;
}

function Setting({ label, value, switchOn }: { label: string; value: string; switchOn?: boolean }) {
  return (
    <div className="setting">
      <span>{label}</span>
      <div className="setting-value">
        <strong>{value}</strong>
        {typeof switchOn === "boolean" && (
          <div className={switchOn ? "ios-switch is-on" : "ios-switch"} aria-hidden="true">
            <span />
          </div>
        )}
      </div>
    </div>
  );
}

function SegmentedControl({
  value,
  options,
  onChange
}: {
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  return (
    <div className="segmented-control">
      {options.map((option) => (
        <button key={option.value} className={value === option.value ? "selected" : ""} onClick={() => onChange(option.value)}>
          {option.label}
        </button>
      ))}
    </div>
  );
}

function Empty({ text }: { text: string }) {
  return <div className="empty">{text}</div>;
}

export default App;
