export type Platform = "feishu" | "feishu_app" | "telegram" | "bark";
export type Format = "text" | "markdown";
export type BarkLevel = "active" | "passive" | "timeSensitive" | "critical";

export const platformLabels: Record<Platform, string> = {
  feishu: "飞书群Webhook",
  feishu_app: "飞书应用机器人",
  telegram: "Telegram Bot",
  bark: "Bark",
};

export const barkLevelLabels: Record<BarkLevel, string> = {
  active: "普通通知",
  passive: "静默通知",
  timeSensitive: "时效性通知",
  critical: "重要警告",
};

export interface Account {
  id: string;
  name: string;
  platform: Platform;
  enabled: boolean;
  credentialsConfigured: boolean;
}

export interface AccountInput {
  id?: string;
  name: string;
  platform: Platform;
  enabled: boolean;
  credentials?: {
    webhookUrl?: string;
    signingSecret?: string;
    botToken?: string;
    appId?: string;
    appSecret?: string;
    serverUrl?: string;
    deviceKey?: string;
    basicAuth?: { username: string; password: string };
  };
}

export interface AccountChat {
  chatId: string;
  name: string;
}

export interface AccountChatPage {
  items: AccountChat[];
  hasMore: boolean;
  nextPageToken: string;
}

export interface TargetConfig {
  chatId?: string;
  messageThreadId?: number;
  group?: string;
  level?: BarkLevel;
  sound?: string;
  call?: boolean;
  url?: string;
}

export interface Target {
  id: string;
  name: string;
  accountId: string;
  enabled: boolean;
  config: TargetConfig;
}

export interface TargetGroup {
  id: string;
  name: string;
  caller: string;
  targetIds: string[];
}

export interface Session {
  authenticated: true;
  expiresAt: string;
}

export interface Acceptance {
  messageId: string;
}

export interface TargetInput {
  id?: string;
  name: string;
  accountId: string;
  enabled: boolean;
  config: TargetConfig;
}

export interface GroupInput {
  id?: string;
  name: string;
  caller: string;
  targetIds: string[];
}

export interface TestInput {
  id: string;
  title?: string;
  message: string;
  format: Format;
}
