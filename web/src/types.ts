export type Platform = "feishu" | "telegram";
export type Format = "text" | "markdown";

export const platformLabels: Record<Platform, string> = {
  feishu: "飞书群Webhook",
  telegram: "Telegram Bot",
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
  };
}

export interface TargetConfig {
  chatId?: string;
  messageThreadId?: number;
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
