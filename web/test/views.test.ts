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

test("账号编辑默认不重新展示凭证，也不提供未实现的应用机器人选项", () => {
  const html = renderToStaticMarkup(createElement(AccountEditor, { ...callbacks, account }));
  assert.match(html, /本次保存将保留现有凭证/);
  assert.equal(html.includes('type="password"'), false);
  assert.equal(html.includes("应用机器人"), false);
});
