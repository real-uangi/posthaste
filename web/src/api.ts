import type {
  Acceptance,
  Account,
  AccountChatPage,
  AccountInput,
  GroupInput,
  IntegrationConfig,
  Session,
  Target,
  TargetGroup,
  TargetInput,
  TestInput,
} from "./types.js";

export class APIError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "APIError";
  }
}

function sessionValue(value: Session): Session {
  if (!value || value.authenticated !== true || typeof value.expiresAt !== "string" || !value.expiresAt) {
    throw new Error("服务未返回有效的会话信息，请重新登录。");
  }
  return value;
}

async function request<T>(
  path: string,
  method: "GET" | "POST",
  body?: unknown,
  signal?: AbortSignal,
  expectedStatus = 200,
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/v1/admin${path}`, {
      method,
      credentials: "same-origin",
      ...(body === undefined
        ? {}
        : {
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          }),
      signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw error;
    }
    throw new Error("无法连接服务，请检查网络后重试。");
  }

  let value: unknown;
  try {
    value = await response.json();
  } catch {
    throw new Error("服务响应格式不正确，请稍后重试。");
  }
  if (
    typeof value !== "object" ||
    value === null ||
    !("code" in value) ||
    value.code !== response.status ||
    !("data" in value)
  ) {
    throw new Error("服务响应格式不正确，请稍后重试。");
  }
  if (!response.ok) {
    const message =
      "message" in value && typeof value.message === "string"
        ? value.message
        : "操作失败，请稍后重试。";
    throw new APIError(response.status, message);
  }
  if (response.status !== expectedStatus) {
    throw new Error("服务响应状态不正确，请稍后重试。");
  }
  return value.data as T;
}

export const api = {
  session: async (signal?: AbortSignal) =>
    sessionValue(await request<Session>("/auth/session", "GET", undefined, signal)),
  login: async (password: string) =>
    sessionValue(await request<Session>("/auth/login", "POST", { password })),
  logout: () => request<null>("/auth/logout", "POST"),
  integrationConfig: async (signal?: AbortSignal) => {
    const value = await request<IntegrationConfig>("/integration/config", "GET", undefined, signal);
    if (!value || typeof value.notifyUrl !== "string" || typeof value.ingressToken !== "string" || !value.ingressToken) {
      throw new Error("服务未返回有效的接入配置，请重新加载。");
    }
    // 地址在响应边界验证一次，示例生成器只处理可信的 HTTP(S) 地址。
    let endpoint: URL;
    try {
      endpoint = new URL(value.notifyUrl);
    } catch {
      throw new Error("服务未返回有效的接入配置，请重新加载。");
    }
    if ((endpoint.protocol !== "https:" && endpoint.protocol !== "http:") || !endpoint.hostname || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) {
      throw new Error("服务未返回有效的接入配置，请重新加载。");
    }
    return value;
  },
  accounts: async (signal?: AbortSignal) => {
    const value = await request<{ items: Account[] }>("/accounts/list", "GET", undefined, signal);
    if (!value || !Array.isArray(value.items)) {
      throw new Error("服务未返回有效的账号列表，请重新加载。");
    }
    return value;
  },
  saveAccount: (account: AccountInput) =>
    request<Account>("/accounts/save", "POST", account),
  accountChats: async (accountId: string, pageToken?: string, signal?: AbortSignal) => {
    const query = new URLSearchParams({ accountId });
    if (pageToken) query.set("pageToken", pageToken);
    const value = await request<AccountChatPage>(`/accounts/chats/list?${query}`, "GET", undefined, signal);
    if (
      !value || !Array.isArray(value.items) ||
      value.items.some((item) => !item || typeof item.chatId !== "string" || !item.chatId || typeof item.name !== "string") ||
      typeof value.hasMore !== "boolean" || typeof value.nextPageToken !== "string" ||
      (value.hasMore && !value.nextPageToken)
    ) {
      throw new Error("服务未返回有效的飞书群列表，请重新加载。");
    }
    return value;
  },
  deleteAccount: (id: string) =>
    request<null>("/accounts/delete", "POST", { id }),
  targets: async (signal?: AbortSignal) => {
    const value = await request<{ items: Target[] }>("/targets/list", "GET", undefined, signal);
    if (!value || !Array.isArray(value.items)) {
      throw new Error("服务未返回有效的出口列表，请重新加载。");
    }
    return value;
  },
  saveTarget: (target: TargetInput) =>
    request<Target>("/targets/save", "POST", target),
  deleteTarget: (id: string) =>
    request<null>("/targets/delete", "POST", { id }),
  testTarget: async (input: TestInput): Promise<Acceptance> => {
    const value = await request<Acceptance>(
      "/targets/test",
      "POST",
      input,
      undefined,
      202,
    );
    if (!value || typeof value.messageId !== "string" || !value.messageId) {
      throw new Error("服务未返回有效的受理编号，请先在目标平台确认。");
    }
    return value;
  },
  groups: async (signal?: AbortSignal) => {
    const value = await request<{ items: TargetGroup[] }>("/groups/list", "GET", undefined, signal);
    if (!value || !Array.isArray(value.items)) {
      throw new Error("服务未返回有效的路由组列表，请重新加载。");
    }
    return value;
  },
  saveGroup: (group: GroupInput) =>
    request<TargetGroup>("/groups/save", "POST", group),
  deleteGroup: (id: string) =>
    request<null>("/groups/delete", "POST", { id }),
};

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "操作失败，请稍后重试。";
}
