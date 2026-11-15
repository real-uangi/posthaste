import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import IntegrationPage, { IntegrationExamples } from "../src/IntegrationPage.js";

test("接入页加载期间保留三类说明及路由提示，禁止复制未加载的配置", () => {
  const html = renderToStaticMarkup(createElement(IntegrationPage, { groups: [], onUnauthorized: () => {} }));
  assert.match(html, /正在读取接入配置/);
  for (const label of ["Shoutrrr", "cURL", "Go SDK"]) assert.ok(html.includes(label));
  assert.match(html, /默认组/);
  assert.match(html, /返回 503/);
  assert.match(html, /202 仅表示已入队/);
  assert.equal((html.match(/disabled="">复制/g) ?? []).length, 3);
  assert.equal(html.includes("<pre"), false);
});

test("已加载的三类示例默认遮住真实密钥，提供格式与 caller 建议", () => {
  const html = renderToStaticMarkup(createElement(IntegrationExamples, {
    config: { notifyUrl: "https://notify.example.com/api/v1/notify", ingressToken: "server-only-secret+/&中文" },
    groups: [{ id: "group-id", name: "运维监控", caller: "Beszel /运维", targetIds: [] }],
  }));
  assert.equal(html.includes("server-only-secret"), false);
  assert.equal((html.match(/YOUR_TOKEN/g) ?? []).length, 3);
  assert.equal((html.match(/<pre /g) ?? []).length, 3);
  assert.match(html, /显示密钥/);
  assert.match(html, /复制内容包含真实入口密钥/);
  assert.match(html, /<option value="text" selected="">纯文本<\/option>/);
  assert.match(html, /<option value="markdown">Markdown<\/option>/);
  assert.match(html, /<option value="Beszel \/运维">运维监控<\/option>/);
  assert.match(html, /目前尚无已发布版本标签/);
  assert.equal(html.includes("<input type=\"checkbox\" checked="), false);
  assert.equal(html.includes("disabled="), false);
});
