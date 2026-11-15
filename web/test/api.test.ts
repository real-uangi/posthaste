import assert from "node:assert/strict";
import test from "node:test";
import { api, APIError } from "../src/api.js";

function envelope(status: number, data: unknown, message = "安全文案") {
  return new Response(JSON.stringify({ code: status, data, message, time: "2026-11-15T00:00:00Z" }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

test("登录发送同源 Cookie 请求，退出不添加无意义 JSON body", async (t) => {
  const calls: Array<{ input: unknown; init?: RequestInit }> = [];
  t.mock.method(globalThis, "fetch", async (input: unknown, init?: RequestInit) => {
    calls.push({ input, init });
    return envelope(200, input === "/api/v1/admin/auth/login" ? { authenticated: true, expiresAt: "2026-11-15T12:00:00Z" } : null);
  });
  await api.login("password");
  await api.logout();
  assert.equal(calls[0].input, "/api/v1/admin/auth/login");
  assert.equal(calls[0].init?.method, "POST");
  assert.equal(calls[0].init?.credentials, "same-origin");
  assert.deepEqual(JSON.parse(calls[0].init?.body as string), { password: "password" });
  assert.equal(calls[1].init?.body, undefined);
  assert.equal(calls[1].init?.headers, undefined);
});

test("会话与列表错误结构必须报错，不能静默当成登录或空列表", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => envelope(200, null));
  await assert.rejects(api.session(), /有效的会话/);
  fetch.mock.mockImplementation(async () => envelope(200, { items: null }));
  await assert.rejects(api.accounts(), /有效的账号列表/);
  await assert.rejects(api.targets(), /有效的出口列表/);
  await assert.rejects(api.groups(), /有效的路由组列表/);
  fetch.mock.mockImplementation(async () => envelope(200, { items: [] }));
  assert.deepEqual(await api.targets(), { items: [] });
  assert.deepEqual(await api.accounts(), { items: [] });
});

test("账号API独立保存凭证与开关，出口API只保存账号引用和地址配置", async (t) => {
  const calls: Array<{ input: unknown; body: unknown }> = [];
  t.mock.method(globalThis, "fetch", async (input: unknown, init?: RequestInit) => {
    calls.push({ input, body: init?.body ? JSON.parse(init.body as string) : undefined });
    return envelope(200, input === "/api/v1/admin/accounts/list" ? { items: [] } : null);
  });
  await api.accounts();
  await api.saveAccount({ id: "account-id", name: "通知机器人", platform: "telegram", enabled: false });
  await api.saveTarget({ id: "target-id", name: "运维群", accountId: "account-id", enabled: true, config: { chatId: "-10098765" } });
  await api.deleteAccount("account-id");
  assert.deepEqual(calls, [
    { input: "/api/v1/admin/accounts/list", body: undefined },
    { input: "/api/v1/admin/accounts/save", body: { id: "account-id", name: "通知机器人", platform: "telegram", enabled: false } },
    { input: "/api/v1/admin/targets/save", body: { id: "target-id", name: "运维群", accountId: "account-id", enabled: true, config: { chatId: "-10098765" } } },
    { input: "/api/v1/admin/accounts/delete", body: { id: "account-id" } },
  ]);
});

test("父账号停用导致测试503时只报告错误，不自动启用或重试", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => envelope(503, null, "所属账号已停用"));
  await assert.rejects(api.testTarget({ id: "target-id", message: "测试", format: "text" }), (error: unknown) => {
    assert.ok(error instanceof APIError);
    assert.equal(error.status, 503);
    assert.match(error.message, /账号已停用/);
    return true;
  });
  assert.equal(fetch.mock.callCount(), 1);
});

test("401 保留明确状态，使界面可以清空会话并要求重新登录", async (t) => {
  t.mock.method(globalThis, "fetch", async () => envelope(401, null, "会话已过期"));
  await assert.rejects(api.session(), (error: unknown) => {
    assert.ok(error instanceof APIError);
    assert.equal(error.status, 401);
    assert.equal(error.message, "会话已过期");
    return true;
  });
});

test("测试发送仅接受真实 HTTP 202 和有效 messageId", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => envelope(202, { messageId: "accepted-id" }));
  const input = { id: "target-id", message: "测试", format: "text" as const };
  assert.deepEqual(await api.testTarget(input), { messageId: "accepted-id" });
  fetch.mock.mockImplementation(async () => envelope(200, { messageId: "not-accepted" }));
  await assert.rejects(api.testTarget(input), /响应状态不正确/);
  fetch.mock.mockImplementation(async () => envelope(202, {}));
  await assert.rejects(api.testTarget(input), /受理编号/);
});

test("HTTP 与 envelope 状态不一致时不当作成功或业务错误", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({ code: 200, data: { messageId: "wrong" } }), { status: 202 }));
  await assert.rejects(api.testTarget({ id: "target-id", message: "测试", format: "text" }), /响应格式不正确/);
});

test("非 JSON 或网络失败不向界面泄漏原始响应与 URL", async (t) => {
  const fetch = t.mock.method(globalThis, "fetch", async () => new Response("<html>private-token</html>", { status: 502 }));
  await assert.rejects(api.targets(), (error: unknown) => {
    assert.ok(error instanceof Error);
    assert.equal(error.message.includes("private-token"), false);
    return true;
  });
  fetch.mock.mockImplementation(async () => { throw new TypeError("https://private-url/token"); });
  await assert.rejects(api.groups(), (error: unknown) => {
    assert.ok(error instanceof Error);
    assert.equal(error.message.includes("private-url"), false);
    assert.match(error.message, /无法连接/);
    return true;
  });
});

test("取消初始请求保留 AbortError，不显示为连接失败", async (t) => {
  t.mock.method(globalThis, "fetch", async () => { throw new DOMException("Aborted", "AbortError"); });
  await assert.rejects(api.session(), (error: unknown) => {
    assert.ok(error instanceof DOMException);
    assert.equal(error.name, "AbortError");
    return true;
  });
});
