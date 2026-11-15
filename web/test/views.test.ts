import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import GroupEditor from "../src/GroupEditor.js";
import TargetEditor from "../src/TargetEditor.js";
import AccountEditor from "../src/AccountEditor.js";
import type { Account, Target } from "../src/types.js";

const account: Account = { id: "account-id", name: "运维机器人", platform: "telegram", enabled: false, credentialsConfigured: true };
const target: Target = { id: "target-id", name: "运维群", accountId: account.id, enabled: true, config: { chatId: "-10098765" } };
const callbacks = { onSaved: async () => {}, onCancel: () => {}, onUnauthorized: () => {} };

test("路由绑定界面将停用父账号下的启用出口显示为不可用", () => {
  const html = renderToStaticMarkup(createElement(GroupEditor, {
    ...callbacks,
    accounts: [account],
    targets: [target],
    group: { id: "group-id", name: "告警", caller: "beszel", targetIds: [target.id] },
  }));
  assert.match(html, /账号停用/);
  assert.match(html, /当前没有可用的绑定出口/);
  assert.match(html, /路由组/);
  assert.equal(target.enabled, true);
});

test("停用账号可被预选配置出口，出口表单不包含任何凭证输入", () => {
  const html = renderToStaticMarkup(createElement(TargetEditor, {
    ...callbacks,
    accounts: [account],
    targets: [],
    initialAccountId: account.id,
  }));
  assert.match(html, /<option value="account-id" selected="">/);
  assert.match(html, /可以保存配置/);
  assert.equal(html.includes('type="password"'), false);
  assert.equal(html.includes("Bot Token"), false);
});

test("已有停用出口的飞书Webhook账号不能用于创建第二个出口", () => {
  const feishu: Account = { ...account, platform: "feishu", enabled: true };
  const html = renderToStaticMarkup(createElement(TargetEditor, {
    ...callbacks,
    accounts: [feishu],
    targets: [{ ...target, enabled: false }],
  }));
  assert.match(html, /<option value="account-id" disabled="">/);
  assert.match(html, /飞书群Webhook/);
  assert.match(html, /已有出口/);
});

test("账号编辑默认不重新展示凭证，并提供独立应用机器人类型", () => {
  const html = renderToStaticMarkup(createElement(AccountEditor, { ...callbacks, account }));
  assert.match(html, /本次保存将保留现有凭证/);
  assert.equal(html.includes('type="password"'), false);
  assert.match(html, /<option value="feishu_app">飞书应用机器人<\/option>/);
});

test("飞书应用账号表单要求完整应用凭证并说明先保存再选群", () => {
  const html = renderToStaticMarkup(createElement(AccountEditor, {
    ...callbacks, account: { ...account, platform: "feishu_app", credentialsConfigured: false },
  }));
  assert.match(html, /App ID/);
  assert.match(html, /App Secret/);
  assert.match(html, /先保存账号/);
  assert.match(html, /整体替换/);
  assert.equal(html.includes("Bot Token"), false);
  assert.equal(html.includes("机器人 Webhook"), false);
});

test("应用账号已有出口仍可配置群，群选择不提供手填或默认首项", () => {
  const app: Account = { ...account, platform: "feishu_app" };
  const html = renderToStaticMarkup(createElement(TargetEditor, {
    ...callbacks, accounts: [app], targets: [target], initialAccountId: app.id,
  }));
  assert.match(html, /<option value="account-id" selected="">/);
  assert.match(html, /请选择机器人已加入的群/);
  assert.match(html, /正在加载飞书群/);
  assert.equal(html.includes("已有出口"), false);
  assert.equal(html.includes("话题 ID"), false);
  assert.equal(html.includes("Bot Token"), false);
  assert.match(html, /<button class="button button-primary" type="submit" disabled="">保存出口<\/button>/);
});

test("编辑应用出口保留未加载到的当前群，允许只更新名称或开关", () => {
  const app: Account = { ...account, platform: "feishu_app" };
  const current: Target = { ...target, config: { chatId: "oc_current" } };
  const html = renderToStaticMarkup(createElement(TargetEditor, {
    ...callbacks, accounts: [app], targets: [current], target: current,
  }));
  assert.match(html, /<option value="oc_current" selected="">当前配置（oc_current）<\/option>/);
  assert.match(html, /保存会保留此群配置/);
  assert.match(html, /<button class="button button-primary" type="submit">保存出口<\/button>/);
});
