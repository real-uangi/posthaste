import assert from "node:assert/strict";
import test from "node:test";
import { accountDraft, buildAccountInput, buildGroupInput, buildTargetInput, feishuAccountOccupied, targetDraft } from "../src/forms.js";
import type { Account, Target, TargetGroup } from "../src/types.js";

const telegram: Account = {
  id: "telegram-account",
  name: "运维机器人",
  platform: "telegram",
  enabled: true,
  credentialsConfigured: true,
};
const feishu: Account = { ...telegram, id: "feishu-account", platform: "feishu" };
const target: Target = {
  id: "target-id",
  name: "运维群",
  accountId: telegram.id,
  enabled: true,
  config: { chatId: "-10098765", messageThreadId: 7 },
};

test("编辑账号默认保留凭证，停用 false 不改写子出口配置", () => {
  const draft = accountDraft(telegram);
  draft.enabled = false;
  const input = buildAccountInput(draft, telegram);
  assert.equal(draft.replaceCredentials, false);
  assert.equal(Object.hasOwn(input, "credentials"), false);
  assert.equal(Object.hasOwn(input, "config"), false);
  assert.equal(input.enabled, false);
  assert.equal(input.id, telegram.id);
});

test("显式更换账号凭证整体替换，已创建账号的平台不可改变", () => {
  const draft = accountDraft(telegram);
  draft.replaceCredentials = true;
  draft.botToken = "replacement-token";
  draft.platform = "feishu";
  const input = buildAccountInput(draft, telegram);
  assert.equal(input.platform, "telegram");
  assert.deepEqual(input.credentials, { botToken: "replacement-token" });
});

test("创建飞书账号提交Webhook凭证，不再把凭证放到出口配置", () => {
  const draft = accountDraft();
  draft.name = "运维飞书群";
  draft.platform = "feishu";
  draft.webhookUrl = "https://open.feishu.cn/open-apis/bot/v2/hook/example";
  assert.equal(draft.replaceCredentials, true);
  assert.deepEqual(buildAccountInput(draft), {
    name: "运维飞书群",
    platform: "feishu",
    enabled: true,
    credentials: { webhookUrl: draft.webhookUrl },
  });
});

test("全局新建出口显式选账号，从账号进入时预选，编辑保留原账号", () => {
  assert.equal(targetDraft().accountId, "");
  assert.equal(targetDraft(undefined, telegram.id).accountId, telegram.id);
  assert.equal(targetDraft(target, feishu.id).accountId, telegram.id);
  assert.throws(() => buildTargetInput(targetDraft(), undefined), /请选择/);
});

test("出口只提交accountId和完整config，停用false不遗漏且无平台或凭证字段", () => {
  const draft = targetDraft(target);
  draft.enabled = false;
  assert.deepEqual(buildTargetInput(draft, telegram, target), {
    id: target.id,
    name: target.name,
    accountId: telegram.id,
    enabled: false,
    config: target.config,
  });
});

test("出口更换账号按新平台构造config，不遗留旧账号的平台配置", () => {
  const draft = targetDraft(target);
  draft.accountId = feishu.id;
  assert.deepEqual(buildTargetInput(draft, feishu, target), {
    id: target.id,
    name: target.name,
    accountId: feishu.id,
    enabled: true,
    config: {},
  });
  draft.accountId = telegram.id;
  draft.chatId = "-10045678";
  assert.deepEqual(buildTargetInput(draft, telegram, target).config, { chatId: "-10045678", messageThreadId: 7 });
});

test("移除可选话题 ID 不遗留旧值，非法话题不静默修正", () => {
  const draft = targetDraft(target);
  draft.messageThreadId = "";
  assert.deepEqual(buildTargetInput(draft, telegram, target).config, { chatId: "-10098765" });
  draft.messageThreadId = "1.2";
  assert.throws(() => buildTargetInput(draft, telegram, target), /正整数/);
});

test("停用账号仍允许保存出口且不改写出口开关", () => {
  const draft = targetDraft(target);
  const input = buildTargetInput(draft, { ...telegram, enabled: false }, target);
  assert.equal(input.enabled, true);
  assert.equal(input.accountId, telegram.id);
});

test("飞书Webhook一账号一出口，停用出口也占位，编辑自身不占用第二个名额", () => {
  const child = { ...target, accountId: feishu.id, enabled: false, config: {} };
  assert.equal(feishuAccountOccupied(feishu, [child]), true);
  assert.equal(feishuAccountOccupied(feishu, [child], child.id), false);
  assert.equal(feishuAccountOccupied(telegram, [target]), false);
});

test("空路由组显式提交空列表，caller原样保留，成员去重", () => {
  assert.deepEqual(buildGroupInput({ name: "空组", caller: " Beszel ", targetIds: [] }), {
    name: "空组",
    caller: " Beszel ",
    targetIds: [],
  });
  assert.deepEqual(buildGroupInput({ name: "分组", caller: "beszel", targetIds: ["a", "a", "b"] }).targetIds, ["a", "b"]);
});

test("默认路由组编辑保护caller，但允许清空绑定", () => {
  const group: TargetGroup = { id: "group-id", name: "默认组", caller: "default", targetIds: [target.id] };
  assert.deepEqual(buildGroupInput({ name: "默认", caller: "changed", targetIds: [] }, group), {
    id: "group-id",
    name: "默认",
    caller: "default",
    targetIds: [],
  });
});
