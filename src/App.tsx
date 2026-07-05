import type { CSSProperties, FormEvent } from "react";
import { useEffect, useMemo, useState } from "react";

type Overview = {
  activeUsers: number;
  channels: number;
  requestsToday: number;
  totalBalance: number;
  successRate: number;
  todayInputTokens: number;
  todayOutputTokens: number;
  todayCost: number;
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
  allowedModels: string[];
  expiresAt?: string;
  rateLimitPerMinute?: number;
};

type Channel = {
  id: string;
  name: string;
  provider: string;
  baseUrl: string;
  status: string;
  streamMode: "auto" | "real" | "fake" | "disabled";
  priority: number;
  weight: number;
  models: string[];
  inputPricePer1K: number;
  outputPricePer1K: number;
  pricingConfigured: boolean;
  lastCheckedAt: string;
  lastError: string;
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
  userId: string | null;
  apiKeyPrefix: string | null;
  model: string | null;
  channel: string | null;
  status: "success" | "failed";
  cost: number;
  inputTokens?: number;
  outputTokens?: number;
  attempts?: number;
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
  defaultBalance: number;
};

type MaintenanceSettings = {
  logRetentionDays: number;
  maxLogs: number;
  maxQuotaEntries: number;
};

type AccountProfile = {
  id: string;
  userId: string;
  username: string;
  email: string;
  discordUserId: string;
  role: "admin" | "user";
};

function arrayOf<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function normalizeChannel(channel: Channel): Channel {
  const streamMode = streamModeOptions.some((option) => option.value === channel.streamMode) ? channel.streamMode : "auto";
  return { ...channel, streamMode, models: arrayOf(channel.models) };
}

function normalizeModel(model: ModelItem): ModelItem {
  return { ...model, aliases: arrayOf(model.aliases) };
}

function normalizeUserDetail(detail: UserDetail): UserDetail {
  return {
    ...detail,
    apiKeys: arrayOf(detail.apiKeys),
    logs: arrayOf(detail.logs)
  };
}

class ApiError extends Error {
  status: number;
  payload: unknown;

  constructor(status: number, message: string, payload?: unknown) {
    super(message);
    this.status = status;
    this.payload = payload;
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
    throw new ApiError(response.status, payload?.error?.message || `Request failed: ${response.status}`, payload);
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

const streamModeOptions = [
  { value: "auto", label: "自动", description: "按请求参数处理" },
  { value: "real", label: "真流", description: "直连上游 SSE" },
  { value: "fake", label: "假流", description: "非流转 SSE" },
  { value: "disabled", label: "禁用流", description: "流式请求跳过" }
] as const;

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

function toLocalDateTime(value: string) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function fromLocalDateTime(value: string) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

function formatFullDate(value: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit"
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

function formatAmount(value: number | null | undefined, digits = 2) {
  const number = Number(value || 0);
  return new Intl.NumberFormat("zh-CN", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits
  }).format(number);
}

function formatTokenCount(value: number | null | undefined) {
  return new Intl.NumberFormat("zh-CN").format(Math.max(0, Math.round(Number(value || 0))));
}

function logTitle(log: RequestLog) {
  if (log.model) return log.model;
  if (log.errorCode === "invalid_api_key") return "密钥无效";
  if (log.errorCode === "model_not_available") return "模型不可用";
  if (log.errorCode === "insufficient_quota") return "额度不足";
  if (log.errorCode === "rate_limit_exceeded") return "请求过快";
  return log.errorCode || "请求失败";
}

function logModelText(log: RequestLog) {
  return log.model || (log.errorCode ? `错误：${log.errorCode}` : "-");
}

function logChannelText(log: RequestLog) {
  return log.channel || (log.apiKeyPrefix ? `Key ${log.apiKeyPrefix}` : "-");
}

function channelModelSummary(models: string[]) {
  const list = arrayOf(models);
  if (list.length === 0) return "未绑定模型";
  const visible = list.slice(0, 2).join(", ");
  return list.length > 2 ? `${visible} · 其余 ${list.length - 2} 个` : visible;
}

function providerDisplayName(provider: string) {
  return providerOptions.find((option) => option.value === provider)?.label || provider || "Custom";
}

function modelProvider(model: ModelItem) {
  const source = `${model.vendor} ${model.id} ${model.name}`.toLowerCase();
  if (source.includes("openai") || /\bgpt[-_/]/.test(source) || source.includes("o1-") || source.includes("o3-")) return "openai";
  if (source.includes("anthropic") || source.includes("claude")) return "anthropic";
  if (source.includes("google") || source.includes("gemini") || source.includes("gcli-")) return "google";
  if (source.includes("deepseek")) return "deepseek";
  if (source.includes("openrouter")) return "openrouter";
  if (source.includes("groq")) return "groq";
  if (source.includes("siliconflow")) return "siliconflow";
  if (source.includes("moonshot") || source.includes("kimi")) return "moonshot";
  return model.vendor && model.vendor.toLowerCase() !== "custom" ? model.vendor.toLowerCase() : "compatible";
}

function ProviderIcon({ provider }: { provider: string }) {
  if (provider === "deepseek") {
    return (
      <span className="provider-icon provider-icon-deepseek" aria-hidden="true">
        <svg viewBox="0 0 32 32" role="img">
          <rect x="1" y="1" width="30" height="30" rx="8" />
          <path transform="translate(2.4 4.4)" d="M26.517 3.395c-.282-.138-.403.125-.568.258-.057.044-.105.1-.152.152-.413.44-.895.73-1.524.695-.92-.052-1.705.237-2.4.941-.147-.868-.638-1.386-1.384-1.718-.39-.173-.786-.346-1.06-.721-.19-.268-.243-.566-.338-.86-.061-.176-.121-.357-.325-.388-.222-.034-.309.151-.396.307-.347.635-.481 1.334-.468 2.042.03 1.594.703 2.863 2.04 3.765.152.104.191.207.143.359-.091.31-.2.613-.295.924-.06.198-.151.242-.364.155-.734-.306-1.367-.76-1.927-1.308-.951-.92-1.81-1.934-2.882-2.729-.252-.185-.504-.358-.764-.522-1.094-1.062.143-1.935.43-2.038.3-.108.104-.48-.864-.475-.968.004-1.853.328-2.982.76-.165.065-.339.112-.516.151-1.024-.194-2.088-.237-3.199-.112-2.092.233-3.763 1.222-4.991 2.91C.254 7.972-.093 10.278.332 12.682c.446 2.535 1.74 4.633 3.728 6.274 2.062 1.7 4.436 2.534 7.145 2.375 1.645-.095 3.476-.316 5.542-2.064.521.259 1.068.363 1.975.44.699.065 1.371-.034 1.892-.142.816-.173.76-.929.465-1.067-2.392-1.114-1.866-.661-2.344-1.027 1.215-1.438 3.071-3.993 3.644-7.473.056-.384.128-.925.12-1.236-.005-.19.038-.263.255-.285.6-.069 1.18-.233 1.715-.527 1.55-.846 2.175-2.237 2.322-3.903.022-.255-.005-.518-.274-.652ZM13.014 18.395c-2.318-1.823-3.442-2.423-3.906-2.397-.434.026-.356.523-.26.847.1.32.23.54.412.82.126.186.213.462-.126.67-.746.461-2.044-.156-2.105-.186-1.51-.89-2.773-2.064-3.664-3.67-.86-1.545-1.358-3.204-1.44-4.974-.022-.427.104-.578.529-.656.56-.103 1.137-.125 1.697-.043 2.366.346 4.379 1.403 6.068 3.079.963.954 1.692 2.094 2.443 3.208.799 1.183 1.658 2.31 2.752 3.234.387.324.695.57.99.751-.89.1-2.374.121-3.39-.683Zm1.111-7.146c0-.19.152-.341.343-.341.043 0 .082.009.117.021.048.018.092.044.126.083.061.06.096.146.096.237a.341.341 0 0 1-.343.341.34.34 0 0 1-.339-.341Zm3.451 1.77c-.222.09-.443.168-.656.177-.33.017-.69-.117-.885-.281-.304-.255-.521-.397-.612-.842-.039-.19-.017-.483.017-.652.078-.362-.009-.595-.265-.807-.208-.172-.473-.22-.764-.22-.108 0-.208-.048-.282-.086-.121-.061-.221-.212-.126-.398.031-.06.178-.207.213-.233.395-.225.85-.151 1.272.018.39.16.686.453 1.111.867.434.501.512.639.759 1.015.196.294.373.596.495.942.073.215-.022.392-.277.5Z" />
        </svg>
      </span>
    );
  }
  const mark = provider === "openai" ? "◎"
    : provider === "anthropic" ? "A"
      : provider === "google" ? "✦"
        : provider === "openrouter" ? "↗"
            : provider === "groq" ? "G"
              : provider === "siliconflow" ? "S"
                : provider === "moonshot" ? "M"
                  : "◇";
  return (
    <span className={`provider-icon provider-icon-${provider}`} aria-hidden="true">
      <svg viewBox="0 0 32 32" role="img">
        <rect x="1" y="1" width="30" height="30" rx="8" />
        <text x="16" y="21" textAnchor="middle">{mark}</text>
      </svg>
    </span>
  );
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
  const [createdKeySecret, setCreatedKeySecret] = useState("");
  const [consoleReady, setConsoleReady] = useState(false);

  const filteredUsers = useMemo(() => {
    const value = query.trim().toLowerCase();
    if (!value) return users;
    return users.filter((user) => `${user.id} ${user.name} ${user.email}`.toLowerCase().includes(value));
  }, [query, users]);

  async function loadAll() {
    const timezoneOffset = new Date().getTimezoneOffset();
    const [overviewData, usersData, channelsData, modelsData, logsData] = await Promise.all([
      fetchJson<Overview>(`/api/overview?timezoneOffset=${timezoneOffset}`),
      fetchJson<{ users: User[] }>("/api/users"),
      fetchJson<{ channels: Channel[] }>("/api/channels"),
      fetchJson<{ models: ModelItem[] }>("/api/models"),
      fetchJson<{ logs: RequestLog[] }>("/api/logs")
    ]);
    const nextUsers = arrayOf(usersData.users);
    const nextChannels = arrayOf(channelsData.channels).map(normalizeChannel);
    const nextModels = arrayOf(modelsData.models).map(normalizeModel);
    const nextLogs = arrayOf(logsData.logs);
    setOverview(overviewData);
    setUsers(nextUsers);
    setChannels(nextChannels);
    setModels(nextModels);
    setLogs(nextLogs);
    setSelectedUserId((current) => {
      if (current && nextUsers.some((user) => user.id === current)) return current;
      return nextUsers[0]?.id || "";
    });
    if (nextUsers.length === 0) {
      setSelectedUser(null);
    }
    setConsoleReady(true);
  }

  async function loadUser(id: string) {
    const data = await fetchJson<UserDetail>(`/api/users/${id}`);
    setSelectedUser(normalizeUserDetail(data));
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

  async function bulkUpdateUsers(
    userIds: string[],
    action: "set_status" | "set_role" | "adjust_balance",
    options: { value?: string; amount?: number; reason?: string } = {}
  ) {
    const data = await fetchJson<{ users: User[]; updated: number }>("/api/users/bulk", {
      method: "POST",
      body: JSON.stringify({ userIds, action, ...options })
    });
    const updated = new Map(arrayOf(data.users).map((user) => [user.id, user]));
    setUsers((current) => current.map((user) => updated.get(user.id) || user));
    setSelectedUser((current) => {
      if (!current) return current;
      const user = updated.get(current.user.id);
      return user ? { ...current, user } : current;
    });
    setToast(`已处理 ${data.updated} 个用户`);
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
    setCreatedKeySecret(data.secret);
    await copyText(data.secret);
    setToast("新 Key 已创建并复制，请立即保存");
    window.setTimeout(() => setToast(""), 2400);
  }

  async function deleteAPIKey(id: string) {
    if (!window.confirm("删除这个 Key？删除后使用它的请求会立即失效。")) return;
    await fetchJson<{ deleted: boolean }>(`/api/api-keys/${id}`, { method: "DELETE" });
    setSelectedUser((current) => (current ? { ...current, apiKeys: current.apiKeys.filter((key) => key.id !== id) } : current));
    setToast("Key 已删除");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function updateAPIKey(id: string, patch: Partial<ApiKey>) {
    const data = await fetchJson<{ apiKey: ApiKey }>(`/api/api-keys/${id}`, {
      method: "PATCH",
      body: JSON.stringify(patch)
    });
    setSelectedUser((current) =>
      current
        ? { ...current, apiKeys: current.apiKeys.map((key) => (key.id === id ? data.apiKey : key)) }
        : current
    );
    setToast("Key 已更新");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function updateChannel(id: string, patch: ChannelPatch) {
    const data = await fetchJson<{ channel: Channel }>(`/api/channels/${id}`, {
      method: "PATCH",
      body: JSON.stringify(patch)
    });
    setChannels((current) => current.map((channel) => (channel.id === id ? normalizeChannel(data.channel) : channel)));
    setToast("渠道已更新");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function deleteChannel(id: string) {
    const channel = channels.find((item) => item.id === id);
    if (!window.confirm(`删除渠道「${channel?.name || id}」？`)) return;
    await fetchJson<{ deleted: boolean }>(`/api/channels/${id}`, { method: "DELETE" });
    setChannels((current) => current.filter((item) => item.id !== id));
    setToast("渠道已删除");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function syncChannelModels(id: string) {
    const data = await fetchJson<ChannelSyncResult>(`/api/channels/${id}/sync-models`, {
      method: "POST",
      body: JSON.stringify({})
    });
    const syncedChannel = normalizeChannel(data.channel);
    const addedModels = arrayOf(data.addedModels).map(normalizeModel);
    const syncedModels = arrayOf(data.models);
    setChannels((current) => current.map((channel) => (channel.id === id ? syncedChannel : channel)));
    if (addedModels.length > 0) {
      setModels((current) => {
        const seen = new Set(current.map((model) => model.id.toLowerCase()));
        return [...current, ...addedModels.filter((model) => !seen.has(model.id.toLowerCase()))];
      });
    }
    setToast(syncedModels.length ? `已拉取 ${syncedModels.length} 个模型` : "上游没有返回模型");
    window.setTimeout(() => setToast(""), 2200);
  }

  async function checkChannel(id: string) {
    try {
      const data = await fetchJson<{ channel: Channel; ok: boolean; models?: string[] }>(`/api/channels/${id}/check`, {
        method: "POST",
        body: JSON.stringify({})
      });
      setChannels((current) => current.map((channel) => (channel.id === id ? normalizeChannel(data.channel) : channel)));
      setToast(data.ok ? `渠道可用，检测到 ${arrayOf(data.models).length} 个模型` : "渠道检测失败");
    } catch (error) {
      const channel = error instanceof ApiError ? (error.payload as { channel?: Channel } | null)?.channel : null;
      if (channel) {
        setChannels((current) => current.map((item) => (item.id === id ? normalizeChannel(channel) : item)));
      }
      setToast(error instanceof Error ? error.message : "渠道检测失败");
    }
    window.setTimeout(() => setToast(""), 2400);
  }

  async function createChannel() {
    const data = await fetchJson<{ channel: Channel }>("/api/channels", {
      method: "POST",
      body: JSON.stringify({})
    });
    setChannels((current) => [...current, normalizeChannel(data.channel)]);
    setActive("channels");
    setToast("已创建禁用的新渠道，请补充上游地址后启用");
    window.setTimeout(() => setToast(""), 2400);
  }

  async function createModel(model: ModelCreate) {
    const data = await fetchJson<{ model: ModelItem }>("/api/models", {
      method: "POST",
      body: JSON.stringify(model)
    });
    setModels((current) => [...current, normalizeModel(data.model)]);
    setToast("模型已添加");
    window.setTimeout(() => setToast(""), 1800);
  }

  async function updateModel(id: string, patch: Partial<ModelItem>) {
    const data = await fetchJson<{ model: ModelItem }>(`/api/models/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: JSON.stringify(patch)
    });
    setModels((current) => current.map((model) => (model.id === id ? normalizeModel(data.model) : model)));
    setToast("模型已更新");
    window.setTimeout(() => setToast(""), 1600);
  }

  async function deleteModel(id: string) {
    if (!window.confirm(`删除模型 ${id}？渠道和 Key 中的引用也会一起清理。`)) return;
    await fetchJson(`/api/models/${encodeURIComponent(id)}`, { method: "DELETE" });
    setModels((current) => current.filter((model) => model.id !== id));
    setChannels((current) => current.map((channel) => ({ ...channel, models: channel.models.filter((modelId) => modelId !== id) })));
    setSelectedUser((current) => current ? {
      ...current,
      apiKeys: current.apiKeys.map((apiKey) => ({ ...apiKey, allowedModels: apiKey.allowedModels.filter((modelId) => modelId !== id) }))
    } : current);
    setToast("模型已删除");
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
            onBulkUpdate={bulkUpdateUsers}
            onCreateKey={createAPIKeyForUser}
            onOpenRegistration={() => {
              setActive("settings");
              setToast("在账号与注册里开放注册，用户即可自助创建账号");
              window.setTimeout(() => setToast(""), 2400);
            }}
          />
        )}
        {active === "keys" && <KeysView selectedUser={selectedUser} onCreateKey={createAPIKeyForUser} onUpdateKey={updateAPIKey} onDeleteKey={deleteAPIKey} />}
        {active === "models" && <ModelsView models={models} onCopy={copyAndToast} onCreate={createModel} onUpdate={updateModel} onDelete={deleteModel} />}
        {active === "channels" && <ChannelsView channels={channels} onUpdate={updateChannel} onCreate={createChannel} onDelete={deleteChannel} onSyncModels={syncChannelModels} onCheck={checkChannel} />}
        {active === "logs" && <LogsView logs={logs} onCopy={copyAndToast} />}
        {active === "settings" && <SettingsView models={models} channels={channels} />}
      </main>

      {createdKeySecret && (
        <div className="secret-dialog-backdrop" role="presentation">
          <section className="secret-dialog" role="dialog" aria-modal="true" aria-labelledby="secret-dialog-title">
            <div>
              <p className="eyebrow">One-time secret</p>
              <h2 id="secret-dialog-title">完整 API Key</h2>
            </div>
            <p>完整密钥只显示这一次。列表中的星号内容只是识别前缀，不能用于 API 调用。</p>
            <code>{createdKeySecret}</code>
            <div className="secret-dialog-actions">
              <button
                className="secondary-button"
                onClick={() => {
                  copyText(createdKeySecret);
                  setToast("完整 Key 已复制");
                  window.setTimeout(() => setToast(""), 1800);
                }}
              >
                复制
              </button>
              <button className="primary-button" onClick={() => setCreatedKeySecret("")}>完成</button>
            </div>
          </section>
        </div>
      )}
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
  const [setupRegistrationEnabled, setSetupRegistrationEnabled] = useState(false);
  const [setupRegistrationMode, setSetupRegistrationMode] = useState<RegistrationMode>("username");
  const [setupDefaultBalance, setSetupDefaultBalance] = useState("0");
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
        : {
          username: registerUsername,
          password,
          displayName,
          email,
          discordUserId: isSetup ? discordUserId : "",
          registrationEnabled: isSetup ? setupRegistrationEnabled : undefined,
          registrationMode: isSetup ? setupRegistrationMode : undefined,
          defaultBalance: isSetup ? Number(setupDefaultBalance || 0) : undefined
        };
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
                <>
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
                  <div className="setup-options">
                    <div className="setting">
                      <span>开放注册</span>
                      <button
                        type="button"
                        className={setupRegistrationEnabled ? "ios-switch is-on" : "ios-switch"}
                        aria-pressed={setupRegistrationEnabled}
                        onClick={() => setSetupRegistrationEnabled((value) => !value)}
                      >
                        <span />
                      </button>
                    </div>
                    <label>
                      <span>注册方式</span>
                      <select value={setupRegistrationMode} onChange={(event) => setSetupRegistrationMode(normalizeRegistrationMode(event.target.value))}>
                        <option value="username">账号密码</option>
                        <option value="email">邮箱</option>
                        <option value="discord">Discord</option>
                      </select>
                    </label>
                    <label>
                      <span>新用户初始额度</span>
                      <input type="number" min="0" step="0.01" value={setupDefaultBalance} onChange={(event) => setSetupDefaultBalance(event.target.value)} />
                    </label>
                  </div>
                </>
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
      setData({ ...account, apiKeys: arrayOf(account.apiKeys) });
      setModels(arrayOf(catalog.models).map(normalizeModel));
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
            {data?.apiKeys?.map((key) => (
              <div key={key.id}>
                <span><strong>{key.name}</strong><small>{key.prefix}...</small></span>
                <Badge tone={key.status}>{statusLabel(key.status)}</Badge>
              </div>
            ))}
            {arrayOf(data?.apiKeys).length === 0 && <div className="empty">还没有 API 密钥</div>}
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

        <div className="gateway-card" aria-label="网关实时请求示意">
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
            <div style={{ "--step": 0 } as CSSProperties}>认证</div>
            <div style={{ "--step": 1 } as CSSProperties}>额度</div>
            <div style={{ "--step": 2 } as CSSProperties}>路由</div>
            <div style={{ "--step": 3 } as CSSProperties}>响应</div>
          </div>
          <pre><span className="request-trace" aria-hidden="true" />{`curl /v1/chat/completions
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
        <Metric label="活跃用户" value={overview ? formatTokenCount(overview.activeUsers) : "-"} />
        <Metric label="今日请求" value={overview ? formatTokenCount(overview.requestsToday) : "-"} />
        <Metric label="今日输入" value={overview ? formatTokenCount(overview.todayInputTokens) : "-"} />
        <Metric label="今日输出" value={overview ? formatTokenCount(overview.todayOutputTokens) : "-"} />
        <Metric label="今日扣费" value={overview ? formatAmount(overview.todayCost, 4) : "-"} />
        <Metric label="账户余额" value={overview ? formatAmount(overview.totalBalance) : "-"} />
        <Metric label="成功率" value={overview ? `${overview.successRate}%` : "-"} />
      </div>
      <GatewayFlow />
      <div className="split-grid">
        <Panel title="渠道状态">
          {channels.length ? (
            channels.map((channel) => (
              <div className="list-row overview-channel-row" key={channel.id}>
                <div>
                  <strong>{channel.name}</strong>
                  <span title={arrayOf(channel.models).join(", ")}>{channelModelSummary(channel.models)}</span>
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
                  <strong>{logTitle(log)}</strong>
                  <span>{log.id} · {log.errorCode || formatDate(log.createdAt)}</span>
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
  onBulkUpdate,
  onCreateKey,
  onOpenRegistration
}: {
  users: User[];
  query: string;
  selectedUser: UserDetail | null;
  onQuery: (value: string) => void;
  onSelect: (id: string) => void;
  onUpdate: (id: string, patch: Partial<User>) => void;
  onBulkUpdate: (
    ids: string[],
    action: "set_status" | "set_role" | "adjust_balance",
    options?: { value?: string; amount?: number; reason?: string }
  ) => Promise<void>;
  onCreateKey: (id: string) => void;
  onOpenRegistration: () => void;
}) {
  const pageSize = 25;
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState<"all" | User["status"]>("all");
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [bulkAmount, setBulkAmount] = useState("10");
  const [bulkReason, setBulkReason] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [balanceAmount, setBalanceAmount] = useState("10");
  const [balanceReason, setBalanceReason] = useState("");
  const [balanceMessage, setBalanceMessage] = useState("");
  const [balanceBusy, setBalanceBusy] = useState(false);
  const visibleUsers = statusFilter === "all" ? users : users.filter((user) => user.status === statusFilter);
  const bulkSelectableUsers = visibleUsers.filter((user) => user.role !== "admin");
  const totalPages = Math.max(1, Math.ceil(visibleUsers.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const pagedUsers = visibleUsers.slice((safePage - 1) * pageSize, safePage * pageSize);
  const allVisibleSelected = bulkSelectableUsers.length > 0 && bulkSelectableUsers.every((user) => selectedIds.has(user.id));

  useEffect(() => {
    setPage(1);
  }, [query, statusFilter]);

  useEffect(() => {
    const available = new Set(users.map((user) => user.id));
    setSelectedIds((current) => new Set([...current].filter((id) => available.has(id))));
  }, [users]);

  useEffect(() => {
    setBalanceAmount("10");
    setBalanceReason("");
    setBalanceMessage("");
  }, [selectedUser?.user.id]);

  function toggleUser(id: string) {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleAllVisible() {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (allVisibleSelected) bulkSelectableUsers.forEach((user) => next.delete(user.id));
      else bulkSelectableUsers.forEach((user) => next.add(user.id));
      return next;
    });
  }

  async function runBulk(
    action: "set_status" | "adjust_balance",
    options: { value?: string; amount?: number; reason?: string }
  ) {
    if (selectedIds.size === 0) return;
    setBulkBusy(true);
    try {
      await onBulkUpdate([...selectedIds], action, options);
      setSelectedIds(new Set());
      setBulkReason("");
    } finally {
      setBulkBusy(false);
    }
  }

  async function adjustSelectedBalance(direction: 1 | -1) {
    if (!selectedUser) return;
    const amount = Math.abs(Number(balanceAmount));
    if (!Number.isFinite(amount) || amount <= 0) {
      setBalanceMessage("请输入大于 0 的金额");
      return;
    }
    setBalanceBusy(true);
    setBalanceMessage("");
    try {
      await onBulkUpdate([selectedUser.user.id], "adjust_balance", {
        amount: Number((amount * direction).toFixed(4)),
        reason: balanceReason.trim() || (direction > 0 ? "管理员增加额度" : "管理员扣减额度")
      });
      setBalanceMessage(direction > 0 ? "额度已增加" : "额度已扣减");
      setBalanceReason("");
    } catch (error) {
      setBalanceMessage(error instanceof Error ? error.message : "额度调整失败");
    } finally {
      setBalanceBusy(false);
    }
  }

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
        <div className="user-filter-row" role="group" aria-label="用户状态筛选">
          {[
            { value: "all", label: "全部" },
            { value: "active", label: "正常" },
            { value: "limited", label: "受限" },
            { value: "disabled", label: "禁用" }
          ].map((item) => (
            <button
              key={item.value}
              type="button"
              className={statusFilter === item.value ? "selected" : ""}
              onClick={() => setStatusFilter(item.value as typeof statusFilter)}
            >
              {item.label}
            </button>
          ))}
        </div>
        <button type="button" className="secondary-button mobile-bulk-select" onClick={toggleAllVisible}>
          {allVisibleSelected ? "取消全选" : `全选结果（${bulkSelectableUsers.length}）`}
        </button>
        {selectedIds.size > 0 && (
          <div className="bulk-action-bar">
            <strong>已选 {selectedIds.size} 人</strong>
            <input
              type="number"
              step="0.01"
              value={bulkAmount}
              onChange={(event) => setBulkAmount(event.target.value)}
              aria-label="额度调整值"
            />
            <input
              value={bulkReason}
              onChange={(event) => setBulkReason(event.target.value)}
              placeholder="原因，例如：活动赠送"
              aria-label="调整原因"
            />
            <button
              type="button"
              className="secondary-button"
              disabled={bulkBusy || !Number(bulkAmount)}
              onClick={() => runBulk("adjust_balance", { amount: Number(bulkAmount), reason: bulkReason })}
            >
              调整额度
            </button>
            <button type="button" className="secondary-button" disabled={bulkBusy} onClick={() => runBulk("set_status", { value: "active" })}>
              启用
            </button>
            <button type="button" className="danger-button" disabled={bulkBusy} onClick={() => runBulk("set_status", { value: "disabled" })}>
              禁用
            </button>
          </div>
        )}
        <div className="table">
          <div className="table-head users-table">
            <input type="checkbox" checked={allVisibleSelected} onChange={toggleAllVisible} aria-label="选择当前筛选结果" />
            <span>用户</span>
            <span>状态</span>
            <span>余额</span>
            <span>今日</span>
          </div>
          {pagedUsers.map((user) => (
            <div
              className={selectedUser?.user.id === user.id ? "table-row users-table selected" : "table-row users-table"}
              key={user.id}
              role="button"
              tabIndex={0}
              onClick={() => onSelect(user.id)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") onSelect(user.id);
              }}
            >
              <input
                type="checkbox"
                checked={selectedIds.has(user.id)}
                disabled={user.role === "admin"}
                onChange={() => toggleUser(user.id)}
                onClick={(event) => event.stopPropagation()}
                aria-label={user.role === "admin" ? `${user.name} 是管理员，不参与批量操作` : `选择 ${user.name}`}
              />
              <span>
                <strong>{user.name}</strong>
                <small>{user.id} · {user.email || "未绑定邮箱"}</small>
              </span>
              <Badge tone={user.status}>{statusLabel(user.status)}</Badge>
              <span>{formatAmount(user.balance)}</span>
              <span>{user.requestsToday}</span>
            </div>
          ))}
          {visibleUsers.length === 0 && <Empty text="暂无匹配用户" />}
        </div>
        {visibleUsers.length > pageSize && (
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
              <Setting label="余额" value={formatAmount(selectedUser.user.balance)} />
              <Setting label="总请求" value={String(selectedUser.user.totalRequests)} />
              <Setting label="最后登录" value={formatDate(selectedUser.user.lastLoginAt)} />
              <Setting label="API 调用" value={selectedUser.user.status === "disabled" ? "关闭" : "允许"} switchOn={selectedUser.user.status !== "disabled"} />
            </div>

            <div className="balance-adjuster">
              <div className="balance-adjuster-title">
                <strong>调整余额</strong>
                <span>当前 {formatAmount(selectedUser.user.balance)}</span>
              </div>
              <div className="balance-adjuster-fields">
                <label>
                  <span>金额</span>
                  <input
                    type="number"
                    min="0.0001"
                    step="0.01"
                    value={balanceAmount}
                    onChange={(event) => setBalanceAmount(event.target.value)}
                  />
                </label>
                <label>
                  <span>备注</span>
                  <input
                    value={balanceReason}
                    onChange={(event) => setBalanceReason(event.target.value)}
                    placeholder="可选，会记录到流水"
                  />
                </label>
              </div>
              <div className="balance-adjuster-actions">
                <button className="secondary-button" disabled={balanceBusy} onClick={() => adjustSelectedBalance(1)}>
                  增加
                </button>
                <button className="danger-button" disabled={balanceBusy} onClick={() => adjustSelectedBalance(-1)}>
                  扣减
                </button>
                <span role="status">{balanceMessage}</span>
              </div>
            </div>

            <div className="action-row">
              <button className="secondary-button" onClick={() => onCreateKey(selectedUser.user.id)}>
                创建 Key
              </button>
              <button
                className="secondary-button"
                disabled={selectedUser.user.role === "admin"}
                onClick={() =>
                  onUpdate(selectedUser.user.id, {
                    status: selectedUser.user.status === "disabled" ? "active" : "disabled"
                  })
                }
              >
                {selectedUser.user.role === "admin" ? "管理员保护" : selectedUser.user.status === "disabled" ? "解封" : "禁用"}
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
  onCreateKey,
  onUpdateKey,
  onDeleteKey
}: {
  selectedUser: UserDetail | null;
  onCreateKey: (id: string) => void;
  onUpdateKey: (id: string, patch: Partial<ApiKey>) => Promise<void>;
  onDeleteKey: (id: string) => void;
}) {
  return (
    <Panel title="密钥管理">
      {selectedUser ? (
        <>
          <div className="panel-toolbar">
            <span className="muted-inline">{selectedUser.user.name} · 完整 Key 只在创建时显示，丢失请重新创建</span>
            <button className="primary-button" onClick={() => onCreateKey(selectedUser.user.id)}>
              创建 Key
            </button>
          </div>
          {selectedUser.apiKeys.length ? (
            selectedUser.apiKeys.map((key) => (
              <KeyEditor key={key.id} apiKey={key} onSave={onUpdateKey} onDelete={onDeleteKey} />
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

function KeyEditor({
  apiKey,
  onSave,
  onDelete
}: {
  apiKey: ApiKey;
  onSave: (id: string, patch: Partial<ApiKey>) => Promise<void>;
  onDelete: (id: string) => void;
}) {
  const [name, setName] = useState(apiKey.name);
  const [allowedModels, setAllowedModels] = useState(arrayOf(apiKey.allowedModels).join(", "));
  const [expiresAt, setExpiresAt] = useState(toLocalDateTime(apiKey.expiresAt || ""));
  const [rateLimit, setRateLimit] = useState(String(apiKey.rateLimitPerMinute || ""));
  const [saving, setSaving] = useState(false);
  const modelSummary = arrayOf(apiKey.allowedModels).length ? arrayOf(apiKey.allowedModels).join(", ") : "全部模型";

  useEffect(() => {
    setName(apiKey.name);
    setAllowedModels(arrayOf(apiKey.allowedModels).join(", "));
    setExpiresAt(toLocalDateTime(apiKey.expiresAt || ""));
    setRateLimit(String(apiKey.rateLimitPerMinute || ""));
  }, [apiKey]);

  async function save() {
    setSaving(true);
    try {
      await onSave(apiKey.id, {
        name: name.trim() || "API Key",
        allowedModels: allowedModels.split(",").map((item) => item.trim()).filter(Boolean),
        expiresAt: fromLocalDateTime(expiresAt),
        rateLimitPerMinute: Number(rateLimit || 0)
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="key-editor">
      <div className="key-editor-head">
        <div>
          <strong>{apiKey.name}</strong>
          <span>{apiKey.prefix}*** · {modelSummary} · 最后使用 {formatDate(apiKey.lastUsedAt)}</span>
        </div>
        <div className="row-actions">
          <Badge tone={apiKey.status}>{statusLabel(apiKey.status)}</Badge>
          <button
            className="secondary-button compact-button"
            onClick={() => onSave(apiKey.id, { status: apiKey.status === "active" ? "disabled" : "active" })}
          >
            {apiKey.status === "active" ? "禁用" : "启用"}
          </button>
          <button className="danger-button compact-button" onClick={() => onDelete(apiKey.id)}>删除</button>
        </div>
      </div>
      <div className="key-editor-grid">
        <label>
          名称
          <input value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label>
          允许模型
          <input value={allowedModels} onChange={(event) => setAllowedModels(event.target.value)} placeholder="留空表示全部模型，多个用逗号分隔" />
        </label>
        <label>
          过期时间
          <input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} />
        </label>
        <label>
          每分钟限制
          <input type="number" min="0" value={rateLimit} onChange={(event) => setRateLimit(event.target.value)} placeholder="0 使用全局限制" />
        </label>
      </div>
      <div className="key-editor-actions">
        <button className="primary-button" disabled={saving} onClick={save}>{saving ? "保存中" : "保存设置"}</button>
      </div>
    </div>
  );
}

function ModelsView({
  models,
  onCopy,
  onCreate,
  onUpdate,
  onDelete
}: {
  models: ModelItem[];
  onCopy: (value: string, label?: string) => void;
  onCreate: (model: ModelCreate) => Promise<void>;
  onUpdate: (id: string, patch: Partial<ModelItem>) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}) {
  const recommended = models.filter((model) => model.recommended);
  const [query, setQuery] = useState("");
  const [activeProvider, setActiveProvider] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [page, setPage] = useState(1);
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [vendor, setVendor] = useState("");
  const [aliases, setAliases] = useState("");
  const [description, setDescription] = useState("");
  const [message, setMessage] = useState("");
  const pageSize = 60;
  const providerStats = useMemo(() => {
    const stats = new Map<string, number>();
    models.forEach((model) => {
      const provider = modelProvider(model);
      stats.set(provider, (stats.get(provider) || 0) + 1);
    });
    return Array.from(stats.entries())
      .sort((a, b) => b[1] - a[1] || providerDisplayName(a[0]).localeCompare(providerDisplayName(b[0])))
      .map(([provider, count]) => ({ provider, count }));
  }, [models]);
  const filteredModels = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return models.filter((model) => {
      if (activeProvider !== "all" && modelProvider(model) !== activeProvider) return false;
      if (statusFilter !== "all" && model.status !== statusFilter) return false;
      if (!keyword) return true;
      return [model.id, model.name, model.vendor, model.category, model.description, ...arrayOf(model.aliases)]
        .join(" ")
        .toLowerCase()
        .includes(keyword);
    });
  }, [models, query, activeProvider, statusFilter]);
  const modelPages = useMemo(() => {
    const groups = new Map<string, ModelItem[]>();
    filteredModels.forEach((model) => {
      const provider = modelProvider(model);
      groups.set(provider, [...(groups.get(provider) || []), model]);
    });

    const pages: Array<Array<[string, ModelItem[]]>> = [];
    let currentPage: Array<[string, ModelItem[]]> = [];
    let currentCount = 0;
    const sortedGroups = Array.from(groups.entries()).sort((a, b) => providerDisplayName(a[0]).localeCompare(providerDisplayName(b[0])));
    for (const group of sortedGroups) {
      if (currentPage.length > 0 && currentCount + group[1].length > pageSize) {
        pages.push(currentPage);
        currentPage = [];
        currentCount = 0;
      }
      currentPage.push(group);
      currentCount += group[1].length;
    }
    if (currentPage.length > 0) pages.push(currentPage);
    return pages;
  }, [filteredModels]);
  const totalPages = Math.max(1, modelPages.length);
  const safePage = Math.min(page, totalPages);
  const groupedModels = modelPages[safePage - 1] || [];

  useEffect(() => {
    setPage(1);
  }, [query, activeProvider, statusFilter, models.length]);

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
              <ModelCard key={model.id} model={model} featured onCopy={onCopy} onUpdate={onUpdate} />
            ))}
          </div>
        </Panel>
      )}

      <Panel title="全部模型">
        <div className="panel-toolbar model-list-toolbar">
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型 ID、名称、供应商或代称" />
          <div className="model-filter-actions">
            {[
              { value: "all", label: "全部" },
              { value: "available", label: "可用" },
              { value: "disabled", label: "禁用" }
            ].map((option) => (
              <button key={option.value} type="button" className={statusFilter === option.value ? "selected" : ""} onClick={() => setStatusFilter(option.value)}>
                {option.label}
              </button>
            ))}
          </div>
        </div>
        <div className="model-provider-filter" aria-label="按供应商筛选模型">
          <button type="button" className={activeProvider === "all" ? "selected" : ""} onClick={() => setActiveProvider("all")}>
            <span className="provider-icon provider-icon-all" aria-hidden="true">All</span>
            <strong>全部</strong>
            <small>{models.length}</small>
          </button>
          {providerStats.map((item) => (
            <button key={item.provider} type="button" className={activeProvider === item.provider ? "selected" : ""} onClick={() => setActiveProvider(item.provider)}>
              <ProviderIcon provider={item.provider} />
              <strong>{providerDisplayName(item.provider)}</strong>
              <small>{item.count}</small>
            </button>
          ))}
        </div>
        <div className="model-list-summary">
          <span>{activeProvider === "all" ? "全部供应商" : providerDisplayName(activeProvider)}</span>
          <strong>{filteredModels.length}</strong>
          <span>个模型</span>
        </div>
        {filteredModels.length > 0 ? (
          <>
            <div className="model-provider-groups">
              {groupedModels.map(([provider, providerModels]) => (
                <section className="model-provider-group" key={provider}>
                  <header>
                    <ProviderIcon provider={provider} />
                    <div>
                      <strong>{providerDisplayName(provider)}</strong>
                      <span>{providerModels.length} 个模型</span>
                    </div>
                  </header>
                  <div className="model-compact-grid">
                    {providerModels.map((model) => (
                      <ModelCompactRow key={model.id} model={model} onCopy={onCopy} onUpdate={onUpdate} onDelete={onDelete} />
                    ))}
                  </div>
                </section>
              ))}
            </div>
            {totalPages > 1 && (
              <div className="pager">
                <button className="secondary-button compact-button" onClick={() => setPage((current) => Math.max(1, current - 1))} disabled={safePage <= 1}>上一页</button>
                <span>{safePage} / {totalPages}</span>
                <button className="secondary-button compact-button" onClick={() => setPage((current) => Math.min(totalPages, current + 1))} disabled={safePage >= totalPages}>下一页</button>
              </div>
            )}
          </>
        ) : (
          <Empty text={models.length ? "没有匹配的模型" : "暂无模型，请先添加你要开放给用户调用的模型 ID"} />
        )}
      </Panel>
    </section>
  );
}

function ModelCompactRow({
  model,
  onCopy,
  onUpdate,
  onDelete
}: {
  model: ModelItem;
  onCopy: (value: string, label?: string) => void;
  onUpdate: (id: string, patch: Partial<ModelItem>) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}) {
  const aliases = arrayOf(model.aliases).slice(0, 3);
  const disabled = model.status === "disabled";
  return (
    <article className="model-compact-row">
      <div className="model-compact-main">
        <strong>{model.name}</strong>
        <small>{model.id}</small>
        {aliases.length > 0 && (
          <span>
            {aliases.map((alias) => (
              <em key={alias}>{alias}</em>
            ))}
          </span>
        )}
      </div>
      <div className="model-row-actions">
        <Badge tone={model.status}>{statusLabel(model.status)}</Badge>
        <button className="icon-button" title="复制模型 ID" onClick={() => onCopy(model.id, "模型 ID 已复制")}>
          <Icon name="copy" />
        </button>
        <button className="secondary-button compact-button" type="button" onClick={() => onUpdate(model.id, { recommended: !model.recommended })}>
          {model.recommended ? "取消推荐" : "推荐"}
        </button>
        <button className="secondary-button compact-button" type="button" onClick={() => onUpdate(model.id, { status: disabled ? "available" : "disabled" })}>
          {disabled ? "启用" : "停用"}
        </button>
        <button className="danger-button compact-button" type="button" onClick={() => onDelete(model.id)}>删除</button>
      </div>
    </article>
  );
}

function ModelCard({
  model,
  featured = false,
  onCopy,
  onUpdate
}: {
  model: ModelItem;
  featured?: boolean;
  onCopy: (value: string, label?: string) => void;
  onUpdate?: (id: string, patch: Partial<ModelItem>) => Promise<void>;
}) {
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
        {arrayOf(model.aliases).map((alias) => (
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
      {onUpdate && (
        <div className="model-card-actions">
          <button className="secondary-button compact-button" type="button" onClick={() => onUpdate(model.id, { recommended: false })}>取消推荐</button>
          <button className="secondary-button compact-button" type="button" onClick={() => onUpdate(model.id, { status: model.status === "disabled" ? "available" : "disabled" })}>
            {model.status === "disabled" ? "启用" : "停用"}
          </button>
        </div>
      )}
    </article>
  );
}

function ChannelsView({
  channels,
  onUpdate,
  onCreate,
  onDelete,
  onSyncModels,
  onCheck
}: {
  channels: Channel[];
  onUpdate: (id: string, patch: ChannelPatch) => Promise<void>;
  onCreate: () => void;
  onDelete: (id: string) => void;
  onSyncModels: (id: string) => Promise<void>;
  onCheck: (id: string) => Promise<void>;
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
          <ChannelEditor key={channel.id} channel={channel} onUpdate={onUpdate} onDelete={onDelete} onSyncModels={onSyncModels} onCheck={onCheck} />
        ))}
        {channels.length === 0 && <Empty text="暂无渠道，先在后端添加渠道接口或导入配置" />}
      </div>
    </Panel>
  );
}

function ChannelEditor({
  channel,
  onUpdate,
  onDelete,
  onSyncModels,
  onCheck
}: {
  channel: Channel;
  onUpdate: (id: string, patch: ChannelPatch) => Promise<void>;
  onDelete: (id: string) => void;
  onSyncModels: (id: string) => Promise<void>;
  onCheck: (id: string) => Promise<void>;
}) {
  const [name, setName] = useState(channel.name);
  const [provider, setProvider] = useState(channel.provider);
  const [streamMode, setStreamMode] = useState<Channel["streamMode"]>(channel.streamMode || "auto");
  const [baseUrl, setBaseUrl] = useState(channel.baseUrl);
  const [models, setModels] = useState(arrayOf(channel.models).join(", "));
  const [inputPrice, setInputPrice] = useState(String(channel.inputPricePer1K || 0));
  const [outputPrice, setOutputPrice] = useState(String(channel.outputPricePer1K || 0));
  const [upstreamApiKey, setUpstreamApiKey] = useState("");
  const [busy, setBusy] = useState("");

  useEffect(() => {
    setName(channel.name);
    setProvider(channel.provider);
    setStreamMode(channel.streamMode || "auto");
    setBaseUrl(channel.baseUrl);
    setModels(arrayOf(channel.models).join(", "));
    setInputPrice(String(channel.inputPricePer1K || 0));
    setOutputPrice(String(channel.outputPricePer1K || 0));
    setUpstreamApiKey("");
  }, [channel.id, channel.name, channel.provider, channel.streamMode, channel.baseUrl, channel.models, channel.inputPricePer1K, channel.outputPricePer1K]);

  async function save() {
    const patch: ChannelPatch = {
      name: name.trim() || channel.name,
      provider,
      streamMode,
      baseUrl,
      inputPricePer1K: Number(inputPrice) || 0,
      outputPricePer1K: Number(outputPrice) || 0,
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

  async function check() {
    setBusy("check");
    try {
      await save();
      await onCheck(channel.id);
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
          {(channel.lastCheckedAt || channel.lastError) && (
            <small>{channel.lastError ? `检测失败：${channel.lastError}` : `上次检测 ${formatDate(channel.lastCheckedAt)}`}</small>
          )}
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

      <div className="stream-mode-grid" role="radiogroup" aria-label="流式处理">
        {streamModeOptions.map((option) => (
          <button key={option.value} type="button" className={streamMode === option.value ? "selected" : ""} onClick={() => setStreamMode(option.value)}>
            <strong>{option.label}</strong>
            <span>{option.description}</span>
          </button>
        ))}
      </div>

      <div className="channel-form-grid">
        <label>
          <span>渠道名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 Gemini 主线路" />
        </label>
        <label>
          <span>供应商</span>
          <input value={providerDisplayName(provider)} readOnly />
        </label>
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
        <label>
          <span>输入单价 / 1K Token</span>
          <input type="number" min="0" step="0.0001" value={inputPrice} onChange={(event) => setInputPrice(event.target.value)} />
        </label>
        <label>
          <span>输出单价 / 1K Token</span>
          <input type="number" min="0" step="0.0001" value={outputPrice} onChange={(event) => setOutputPrice(event.target.value)} />
        </label>
        <span className="channel-billing-note">渠道价格优先；均为 0 时使用模型价格，模型也未定价则不扣费。</span>
      </div>

      <div className="channel-card-actions">
        <button className="secondary-button" onClick={check} disabled={busy !== ""}>
          {busy === "check" ? "检测中" : "检测渠道"}
        </button>
        <button className="secondary-button" onClick={syncModels} disabled={busy !== ""}>
          {busy === "sync" ? "拉取中" : "拉取模型"}
        </button>
        <button className="secondary-button" onClick={save} disabled={busy !== ""}>
          {busy === "save" ? "保存中" : "保存"}
        </button>
        <button className="status-button" onClick={() => onUpdate(channel.id, { status: channel.status === "disabled" ? "healthy" : "disabled" })}>
          <Badge tone={channel.status}>{channel.status === "disabled" ? "启用" : "禁用"}</Badge>
        </button>
        <button className="danger-button" onClick={() => onDelete(channel.id)} disabled={busy !== ""}>删除</button>
      </div>
    </div>
  );
}

function LogsView({ logs, onCopy }: { logs: RequestLog[]; onCopy: (value: string, label?: string) => void }) {
  const [items, setItems] = useState(logs);
  const [total, setTotal] = useState(logs.length);
  const [page, setPage] = useState(1);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<"all" | "success" | "failed">("all");
  const [selected, setSelected] = useState<RequestLog | null>(null);
  const [loading, setLoading] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const pageSize = 25;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  useEffect(() => {
    setPage(1);
  }, [query, status]);

  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      setLoading(true);
      try {
        const params = new URLSearchParams({
          page: String(page),
          pageSize: String(pageSize),
          status,
          q: query.trim()
        });
        const data = await fetchJson<{ logs: RequestLog[]; total: number }>(`/api/logs?${params}`, {
          signal: controller.signal
        });
        const nextLogs = arrayOf(data.logs);
        setItems(nextLogs);
        setTotal(data.total || 0);
        setSelected((current) => current && nextLogs.some((log) => log.id === current.id) ? current : null);
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setItems([]);
          setTotal(0);
        }
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }, query ? 250 : 0);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [page, query, status]);

  async function selectLog(log: RequestLog) {
    if (selected?.id === log.id) {
      setSelected(null);
      return;
    }
    setSelected(log);
    setDetailLoading(true);
    try {
      const data = await fetchJson<{ log: RequestLog }>(`/api/logs/${encodeURIComponent(log.id)}`);
      setSelected(data.log);
    } catch {
      setSelected(log);
    } finally {
      setDetailLoading(false);
    }
  }

  return (
    <Panel title="调用日志">
      <div className="logs-toolbar">
        <div className="search-box">
          <Icon name="search" />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索请求 ID、用户、Key、模型、渠道或错误码" />
        </div>
        <div className="log-status-filter" role="group" aria-label="日志状态筛选">
          {[
            { value: "all", label: "全部" },
            { value: "success", label: "成功" },
            { value: "failed", label: "失败" }
          ].map((option) => (
            <button
              key={option.value}
              type="button"
              className={status === option.value ? "selected" : ""}
              onClick={() => setStatus(option.value as typeof status)}
            >
              {option.label}
            </button>
          ))}
        </div>
        <span className="muted-inline">{loading ? "加载中" : `共 ${total} 条`}</span>
      </div>
      <div className="logs-layout">
        <div>
          <div className="table">
            <div className="table-head logs-table">
              <span>请求</span>
              <span>模型</span>
              <span>渠道</span>
              <span>输入</span>
              <span>输出</span>
              <span>消耗</span>
              <span>状态</span>
            </div>
            {items.map((log) => (
              <div className="log-entry" key={log.id}>
                <div
                  className={selected?.id === log.id ? "table-row logs-table selected" : "table-row logs-table"}
                  role="button"
                  tabIndex={0}
                  onClick={() => selectLog(log)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") selectLog(log);
                  }}
                >
                  <span>
                    <strong>{logTitle(log)}</strong>
                    <small>{log.id} · {formatDate(log.createdAt)} · {log.latencyMs}ms</small>
                  </span>
                  <span>{logModelText(log)}</span>
                  <span>{logChannelText(log)}</span>
                  <span>{formatTokenCount(log.inputTokens)}</span>
                  <span>{formatTokenCount(log.outputTokens)}</span>
                  <span>{log.cost.toFixed(4)}</span>
                  <Badge tone={log.status}>{statusLabel(log.status)}</Badge>
                </div>
              </div>
            ))}
            {!loading && items.length === 0 && <Empty text={query || status !== "all" ? "没有匹配的日志" : "暂无调用日志"} />}
          </div>
          {totalPages > 1 && (
            <div className="pagination-bar">
              <button className="secondary-button" disabled={page <= 1 || loading} onClick={() => setPage((value) => Math.max(1, value - 1))}>上一页</button>
              <span>{page} / {totalPages}</span>
              <button className="secondary-button" disabled={page >= totalPages || loading} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>下一页</button>
            </div>
          )}
        </div>
        <LogDetail log={selected} loading={detailLoading} onCopy={onCopy} />
      </div>
    </Panel>
  );
}

function LogDetail({ log, loading, onCopy }: { log: RequestLog | null; loading: boolean; onCopy: (value: string, label?: string) => void }) {
  if (!log) {
    return (
      <aside className="log-inspector empty-inspector">
        <span>选择一条日志查看详情</span>
      </aside>
    );
  }
  const inputTokens = Number(log.inputTokens || 0);
  const outputTokens = Number(log.outputTokens || 0);
  const attempts = typeof log.attempts === "number" ? Math.max(0, log.attempts) : null;
  const details = [
    ["请求 ID", log.id],
    ["状态", statusLabel(log.status)],
    ["时间", formatFullDate(log.createdAt)],
    ["用户 ID", log.userId || "未识别"],
    ["API Key", log.apiKeyPrefix ? `${log.apiKeyPrefix}***` : "未识别"],
    ["模型", log.model || "未提供"],
    ["渠道", log.channel || "未选择"],
    ["响应耗时", `${log.latencyMs} ms`],
    ["尝试次数", attempts === null ? "未记录" : String(attempts)],
    ["是否重试", attempts === null ? "未记录" : attempts > 1 ? "是" : "否"],
    ["输入 Tokens", formatTokenCount(inputTokens)],
    ["输出 Tokens", formatTokenCount(outputTokens)],
    ["总 Tokens", formatTokenCount(inputTokens + outputTokens)],
    ["扣费", log.cost.toFixed(4)],
    ["错误码", log.errorCode || "无"]
  ];
  return (
    <aside className="log-inspector">
      <header>
        <div>
          <span>{loading ? "加载中" : "日志详情"}</span>
          <strong>{logTitle(log)}</strong>
        </div>
        <Badge tone={log.status}>{statusLabel(log.status)}</Badge>
      </header>
      <div className="log-detail">
        {details.map(([label, value]) => (
          <div key={label}>
            <span>{label}</span>
            <strong title={value}>{value}</strong>
          </div>
        ))}
      </div>
      <div className="log-actions">
        <button type="button" className="secondary-button compact-button" onClick={() => onCopy(log.id, "请求 ID 已复制")}>复制请求 ID</button>
        {log.errorCode && <button type="button" className="secondary-button compact-button" onClick={() => onCopy(log.errorCode || "", "错误码已复制")}>复制错误码</button>}
      </div>
    </aside>
  );
}

function SettingsView({ models, channels }: { models: ModelItem[]; channels: Channel[] }) {
  const [discord, setDiscord] = useState<DiscordSettings | null>(null);
  const [registrationEnabled, setRegistrationEnabled] = useState<boolean | null>(null);
  const [registrationMode, setRegistrationMode] = useState<RegistrationMode>("username");
  const [defaultBalance, setDefaultBalance] = useState("0");
  const [maintenance, setMaintenance] = useState<MaintenanceSettings>({
    logRetentionDays: 30,
    maxLogs: 10000,
    maxQuotaEntries: 20000
  });
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
  const settingsTabs = [
    { value: "system", label: "系统", description: "运行概况" },
    { value: "auth", label: "注册", description: "开放方式" },
    { value: "admin", label: "管理员", description: "账号绑定" },
    { value: "discord", label: "Discord", description: "登录限制" },
    { value: "maintenance", label: "维护", description: "日志保留" },
    { value: "backup", label: "备份", description: "导出恢复" }
  ];
  const activeSettingsTab = settingsTabs.find((tab) => tab.value === settingsTab) || settingsTabs[0];

  useEffect(() => {
    Promise.all([
      fetchJson<{ discord: DiscordSettings }>("/api/settings/discord"),
      fetchJson<{ auth: AuthSettings }>("/api/settings/auth"),
      fetchJson<{ account: AccountProfile; user: User }>("/api/account/me"),
      fetchJson<{ maintenance: MaintenanceSettings }>("/api/settings/maintenance")
    ])
      .then(([discordData, authData, accountData, maintenanceData]) => {
        setDiscord(withBrowserDiscordDefaults(discordData.discord));
        setRegistrationEnabled(authData.auth.registrationEnabled);
        setRegistrationMode(normalizeRegistrationMode(authData.auth.registrationMode));
        setDefaultBalance(String(authData.auth.defaultBalance || 0));
        setAccount(accountData.account);
        setAccountUsername(accountData.account?.username || "");
        setAccountDisplayName(accountData.user.name || "");
        setAccountEmail(accountData.account?.email || "");
        setDiscordUserId(accountData.account?.discordUserId || "");
        setMaintenance(maintenanceData.maintenance);
      })
      .catch(() => setMessage("设置加载失败"));
  }, []);

  async function saveAuthSettings(nextEnabled = registrationEnabled, nextMode = registrationMode, nextDefaultBalance = Number(defaultBalance)) {
    if (nextEnabled === null) return;
    try {
      const data = await fetchJson<{ auth: AuthSettings }>("/api/settings/auth", {
        method: "PATCH",
        body: JSON.stringify({ registrationEnabled: nextEnabled, registrationMode: nextMode, defaultBalance: nextDefaultBalance })
      });
      setRegistrationEnabled(data.auth.registrationEnabled);
      setRegistrationMode(normalizeRegistrationMode(data.auth.registrationMode));
      setDefaultBalance(String(data.auth.defaultBalance || 0));
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

  async function saveMaintenanceSettings() {
    setSaving(true);
    setMessage("");
    try {
      const data = await fetchJson<{ maintenance: MaintenanceSettings }>("/api/settings/maintenance", {
        method: "PATCH",
        body: JSON.stringify(maintenance)
      });
      setMaintenance(data.maintenance);
      setMessage("维护设置已保存，历史数据已按新规则清理");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "维护设置保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function downloadBackup() {
    setSaving(true);
    setMessage("");
    try {
      const response = await fetch("/api/backup", { credentials: "include" });
      if (!response.ok) throw new Error("备份导出失败");
      const blob = await response.blob();
      const disposition = response.headers.get("Content-Disposition") || "";
      const filename = disposition.match(/filename="([^"]+)"/)?.[1] || "catieapi-backup.json";
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = filename;
      anchor.click();
      URL.revokeObjectURL(url);
      setMessage("备份已导出，请妥善保管");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "备份导出失败");
    } finally {
      setSaving(false);
    }
  }

  async function restoreBackup(file: File) {
    if (!window.confirm("恢复会覆盖当前全部数据，并退出现有登录会话。确定继续？")) return;
    setSaving(true);
    setMessage("");
    try {
      const response = await fetch("/api/restore", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: file
      });
      const payload = await response.json().catch(() => null);
      if (!response.ok) {
        throw new Error(payload?.error?.message || "备份恢复失败");
      }
      setMessage(`已恢复 ${payload.users} 个用户、${payload.channels} 条渠道和 ${payload.models} 个模型，请重新登录`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "备份恢复失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="settings-layout">
      <div className="settings-tabs">
        {settingsTabs.map((tab) => (
          <button key={tab.value} type="button" className={settingsTab === tab.value ? "selected" : ""} onClick={() => setSettingsTab(tab.value)}>
            <strong>{tab.label}</strong>
            <small>{tab.description}</small>
          </button>
        ))}
      </div>
      <div className="settings-tab-note">
        <strong>{activeSettingsTab.label}</strong>
        <span>{activeSettingsTab.description}</span>
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

      {settingsTab === "maintenance" && (
      <Panel title="维护设置">
        <div className="settings-group">
          <div className="setting">
            <span>日志保留天数</span>
            <div className="setting-value maintenance-control">
              <input
                type="number"
                min="1"
                max="3650"
                value={maintenance.logRetentionDays}
                onChange={(event) => setMaintenance((current) => ({ ...current, logRetentionDays: Number(event.target.value) }))}
              />
            </div>
          </div>
          <div className="setting">
            <span>日志最大条数</span>
            <div className="setting-value maintenance-control">
              <input
                type="number"
                min="100"
                max="1000000"
                step="100"
                value={maintenance.maxLogs}
                onChange={(event) => setMaintenance((current) => ({ ...current, maxLogs: Number(event.target.value) }))}
              />
            </div>
          </div>
          <div className="setting">
            <span>额度流水最大条数</span>
            <div className="setting-value maintenance-control">
              <input
                type="number"
                min="100"
                max="2000000"
                step="100"
                value={maintenance.maxQuotaEntries}
                onChange={(event) => setMaintenance((current) => ({ ...current, maxQuotaEntries: Number(event.target.value) }))}
              />
            </div>
          </div>
          <div className="settings-save-row">
            <span role="status">{message}</span>
            <button type="button" className="primary-button" disabled={saving} onClick={saveMaintenanceSettings}>
              {saving ? "保存中" : "保存维护设置"}
            </button>
          </div>
        </div>
      </Panel>
      )}

      {settingsTab === "backup" && (
      <Panel title="备份与恢复">
        <div className="settings-group">
          <div className="setting">
            <span>
              备份与恢复
              <small>包含账号哈希和加密后的上游密钥，恢复时需要相同的 SECRET_KEY</small>
            </span>
            <div className="setting-value backup-actions">
              <button type="button" className="secondary-button" disabled={saving} onClick={downloadBackup}>导出备份</button>
              <label className="secondary-button">
                恢复备份
                <input
                  type="file"
                  accept="application/json,.json"
                  disabled={saving}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (file) restoreBackup(file);
                    event.target.value = "";
                  }}
                />
              </label>
            </div>
          </div>
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
          <div className="setting">
            <span>
              新用户初始额度
              <small>注册完成后自动发放，仅影响之后的新用户</small>
            </span>
            <div className="setting-value auth-default-balance">
              <input
                type="number"
                min="0"
                step="0.01"
                value={defaultBalance}
                onChange={(event) => setDefaultBalance(event.target.value)}
                aria-label="新用户初始额度"
              />
              <button
                type="button"
                className="secondary-button"
                onClick={() => saveAuthSettings(registrationEnabled, registrationMode, Number(defaultBalance))}
              >
                保存
              </button>
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
