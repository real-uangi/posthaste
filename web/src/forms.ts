import type {
  Account,
  AccountInput,
  GroupInput,
  Platform,
  Target,
  TargetGroup,
  TargetInput,
} from "./types.js";

export interface AccountDraft {
  name: string;
  platform: Platform;
  enabled: boolean;
  replaceCredentials: boolean;
  webhookUrl: string;
  signingSecret: string;
  botToken: string;
  appId: string;
  appSecret: string;
}

export function accountDraft(account?: Account): AccountDraft {
  return {
    name: account?.name ?? "",
    platform: account?.platform ?? "telegram",
    enabled: account?.enabled ?? true,
    replaceCredentials: !account?.credentialsConfigured,
    webhookUrl: "",
    signingSecret: "",
    botToken: "",
    appId: "",
    appSecret: "",
  };
}

export function buildAccountInput(
  draft: AccountDraft,
  account?: Account,
): AccountInput {
  const platform = account?.platform ?? draft.platform;
  const input: AccountInput = {
    ...(account ? { id: account.id } : {}),
    name: draft.name.trim(),
    platform,
    enabled: draft.enabled,
  };

  // 更新未选择更换凭证时必须省略字段，不能用空对象覆盖已存凭证。
  if (!account || draft.replaceCredentials) {
    if (platform === "feishu") {
      input.credentials = {
        webhookUrl: draft.webhookUrl.trim(),
        ...(draft.signingSecret ? { signingSecret: draft.signingSecret } : {}),
      };
    } else if (platform === "feishu_app") {
      input.credentials = { appId: draft.appId.trim(), appSecret: draft.appSecret.trim() };
    } else {
      input.credentials = { botToken: draft.botToken.trim() };
    }
  }
  return input;
}

export interface TargetDraft {
  name: string;
  accountId: string;
  enabled: boolean;
  chatId: string;
  messageThreadId: string;
}

export function targetDraft(target?: Target, initialAccountId?: string): TargetDraft {
  return {
    name: target?.name ?? "",
    accountId: target?.accountId ?? initialAccountId ?? "",
    enabled: target?.enabled ?? true,
    chatId: target?.config.chatId ?? "",
    messageThreadId: target?.config.messageThreadId?.toString() ?? "",
  };
}

export function buildTargetInput(
  draft: TargetDraft,
  account: Account | undefined,
  target?: Target,
): TargetInput {
  if (!account) throw new Error("请选择群组出口所属的账号。");
  const input: TargetInput = {
    ...(target ? { id: target.id } : {}),
    name: draft.name.trim(),
    accountId: account.id,
    enabled: draft.enabled,
    config: {},
  };
  // 出口可更换账号，配置必须按新账号的平台构造，不能沿用旧平台载荷。
  if (account.platform === "feishu_app") {
    if (!draft.chatId) throw new Error("请选择飞书机器人已加入的群。");
    input.config.chatId = draft.chatId;
  } else if (account.platform === "telegram") {
    input.config.chatId = draft.chatId.trim();
    if (draft.messageThreadId !== "") {
      const value = Number(draft.messageThreadId);
      if (!Number.isSafeInteger(value) || value < 1) {
        throw new Error("话题 ID 必须为正整数。");
      }
      input.config.messageThreadId = value;
    }
  }
  return input;
}

export function targetAvailability(target: Target, account: Account | undefined) {
  if (!account) return { enabled: false, label: "账号不可用" };
  if (!account.enabled) return { enabled: false, label: "账号停用" };
  if (!target.enabled) return { enabled: false, label: "出口停用" };
  return { enabled: true, label: "启用" };
}

export function feishuAccountOccupied(account: Account, targets: Target[], excludingTargetId?: string) {
  return account.platform === "feishu" && targets.some((target) => target.accountId === account.id && target.id !== excludingTargetId);
}

export function buildGroupInput(
  fields: { name: string; caller: string; targetIds: string[] },
  group?: TargetGroup,
): GroupInput {
  return {
    ...(group ? { id: group.id } : {}),
    name: fields.name.trim(),
    caller: group?.caller === "default" ? "default" : fields.caller,
    // 空列表代表解除全部绑定，必须保留；集合去重不改变 caller 的精确匹配语义。
    targetIds: [...new Set(fields.targetIds)],
  };
}
