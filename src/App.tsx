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
    throw new Error(payload?.error?.message || `Request failed: ${response.status}`);
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

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(value));
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

function App() {
  const [surface, setSurface] = useState<"home" | "console">("home");
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
  const [selectedUserId, setSelectedUserId] = useState("usr_1001");
  const [selectedUser, setSelectedUser] = useState<UserDetail | null>(null);
  const [toast, setToast] = useState("");

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

  useEffect(() => {
    loadAll().catch(() => setToast("加载数据失败"));
  }, []);

  useEffect(() => {
    loadUser(selectedUserId).catch(() => setToast("加载用户详情失败"));
  }, [selectedUserId]);

  useEffect(() => {
    window.localStorage.setItem("catieapi-theme", theme);
  }, [theme]);

  if (surface === "home") {
    return <PublicHome theme={theme} setTheme={setTheme} enterConsole={() => setSurface("console")} />;
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
          <button className="primary-button" onClick={() => loadAll()}>
            刷新
          </button>
          <button className="secondary-button home-link" onClick={() => setSurface("home")}>
            首页
          </button>
        </div>
      </header>

        {active === "overview" && <OverviewView overview={overview} channels={channels} logs={logs} />}
        {active === "users" && (
          <UsersView
            users={filteredUsers}
            query={query}
            selectedUser={selectedUser}
            onQuery={setQuery}
            onSelect={setSelectedUserId}
            onUpdate={updateUser}
          />
        )}
        {active === "keys" && <KeysView selectedUser={selectedUser} />}
        {active === "models" && <ModelsView models={models} />}
        {active === "channels" && <ChannelsView channels={channels} />}
        {active === "logs" && <LogsView logs={logs} />}
        {active === "settings" && <SettingsView />}
      </main>

      {toast && <div className="toast">{toast}</div>}
    </div>
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
  -d '{"model":"gpt-5.6"}'`}</pre>
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

function OverviewView({ overview, channels, logs }: { overview: Overview | null; channels: Channel[]; logs: RequestLog[] }) {
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
        <QuickAction icon="key" label="创建 Key" />
        <QuickAction icon="route" label="新增渠道" />
        <QuickAction icon="users" label="调整额度" />
        <QuickAction icon="logs" label="查看异常" />
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
          {channels.map((channel) => (
            <div className="list-row" key={channel.id}>
              <div>
                <strong>{channel.name}</strong>
                <span>{channel.models.join(", ")}</span>
              </div>
              <Badge tone={channel.status}>{statusLabel(channel.status)}</Badge>
            </div>
          ))}
        </Panel>
        <Panel title="最近请求">
          {logs.slice(0, 4).map((log) => (
            <div className="list-row" key={log.id}>
              <div>
                <strong>{log.model}</strong>
                <span>{log.id} · {formatDate(log.createdAt)}</span>
              </div>
              <Badge tone={log.status}>{statusLabel(log.status)}</Badge>
            </div>
          ))}
        </Panel>
      </div>
    </section>
  );
}

function QuickAction({ icon, label }: { icon: IconName; label: string }) {
  return (
    <button className="quick-action">
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
  onUpdate
}: {
  users: User[];
  query: string;
  selectedUser: UserDetail | null;
  onQuery: (value: string) => void;
  onSelect: (id: string) => void;
  onUpdate: (id: string, patch: Partial<User>) => void;
}) {
  return (
    <section className="users-layout">
      <Panel title="用户列表">
        <div className="panel-toolbar">
          <div className="search-box">
            <Icon name="search" />
            <input value={query} onChange={(event) => onQuery(event.target.value)} placeholder="搜索 ID、姓名或邮箱" />
          </div>
          <button className="icon-button" title="新增用户">
            <Icon name="plus" />
          </button>
        </div>
        <div className="table">
          <div className="table-head users-table">
            <span>用户</span>
            <span>状态</span>
            <span>余额</span>
            <span>今日</span>
          </div>
          {users.map((user) => (
            <button className="table-row users-table" key={user.id} onClick={() => onSelect(user.id)}>
              <span>
                <strong>{user.name}</strong>
                <small>{user.email}</small>
              </span>
              <Badge tone={user.status}>{statusLabel(user.status)}</Badge>
              <span>{user.balance.toFixed(2)}</span>
              <span>{user.requestsToday}</span>
            </button>
          ))}
        </div>
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
            </div>
          </div>
        ) : (
          <Empty text="请选择一个用户" />
        )}
      </Panel>
    </section>
  );
}

function KeysView({ selectedUser }: { selectedUser: UserDetail | null }) {
  return (
    <Panel title="密钥管理">
      {selectedUser?.apiKeys.length ? (
        selectedUser.apiKeys.map((key) => (
          <div className="list-row" key={key.id}>
            <div>
              <strong>{key.name}</strong>
              <span>{key.prefix}*** · 最后使用 {formatDate(key.lastUsedAt)}</span>
            </div>
            <button className="icon-button" title="复制前缀">
              <Icon name="copy" />
            </button>
          </div>
        ))
      ) : (
        <Empty text="暂无密钥" />
      )}
    </Panel>
  );
}

function ModelsView({ models }: { models: ModelItem[] }) {
  const recommended = models.filter((model) => model.recommended);

  return (
    <section className="models-page">
      <div className="model-hero">
        <div>
          <span>Model Catalog</span>
          <strong>Models</strong>
        </div>
      </div>

      <Panel title="推荐模型">
        <div className="model-grid">
          {recommended.map((model) => (
            <ModelCard key={model.id} model={model} featured />
          ))}
        </div>
      </Panel>

      <Panel title="全部模型">
        <div className="model-list">
          {models.map((model) => (
            <ModelCard key={model.id} model={model} />
          ))}
        </div>
      </Panel>
    </section>
  );
}

function ModelCard({ model, featured = false }: { model: ModelItem; featured?: boolean }) {
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
        <button className="icon-button" title="复制模型 ID">
          <Icon name="copy" />
        </button>
      </div>
    </article>
  );
}

function ChannelsView({ channels }: { channels: Channel[] }) {
  return (
    <Panel title="渠道管理">
      <div className="table">
        <div className="table-head channels-table">
          <span>渠道</span>
          <span>供应商</span>
          <span>优先级</span>
          <span>权重</span>
          <span>运行状态</span>
        </div>
        {channels.map((channel) => (
          <div className="table-row channels-table" key={channel.id}>
            <span>
              <strong>{channel.name}</strong>
              <small>{channel.baseUrl}</small>
            </span>
            <span>{channel.provider}</span>
            <span>{channel.priority}</span>
            <span>{channel.weight}</span>
            <Badge tone={channel.status}>{statusLabel(channel.status)}</Badge>
          </div>
        ))}
      </div>
    </Panel>
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

function SettingsView() {
  const [discord, setDiscord] = useState<DiscordSettings | null>(null);
  const [clientSecret, setClientSecret] = useState("");
  const [adminToken, setAdminToken] = useState(() => window.sessionStorage.getItem("catieapi-admin-token") || "");
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchJson<{ discord: DiscordSettings }>("/api/settings/discord")
      .then((data) => setDiscord(data.discord))
      .catch(() => setMessage("Discord 配置加载失败"));
  }, []);

  async function saveDiscordSettings() {
    if (!discord) return;
    setSaving(true);
    setMessage("");
    try {
      const data = await fetchJson<{ discord: DiscordSettings }>("/api/settings/discord", {
        method: "PATCH",
        body: JSON.stringify({ ...discord, clientSecret })
      });
      setDiscord(data.discord);
      setClientSecret("");
      setMessage("Discord 配置已保存");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "保存失败，请检查填写内容");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="settings-layout">
      <Panel title="管理验证">
        <form
          className="admin-token-form"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            const value = adminToken.trim();
            if (value) {
              window.sessionStorage.setItem("catieapi-admin-token", value);
            } else {
              window.sessionStorage.removeItem("catieapi-admin-token");
            }
            window.location.reload();
          }}
        >
          <label>
            <span>管理密钥</span>
            <input
              type="password"
              autoComplete="current-password"
              value={adminToken}
              onChange={(event) => setAdminToken(event.target.value)}
              placeholder="ADMIN_TOKEN"
            />
          </label>
          <button className="secondary-button" type="submit">应用</button>
        </form>
      </Panel>

      <Panel title="系统设置">
        <div className="settings-group">
          <Setting label="网关模式" value="OpenAI Compatible" />
          <Setting label="默认模型" value="gpt-5.6" />
          <Setting label="请求限流" value="启用" switchOn />
          <Setting label="审计日志" value="启用" switchOn />
        </div>
      </Panel>

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
                  placeholder="1446547305208746115"
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
