import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { buildIntegrationExamples } from "../src/integration.js";

const config = { notifyUrl: "https://notify.example.com/api/v1/notify", ingressToken: "example-token" };

function curlArguments(command: string): string[] {
  // Override curl with a shell function to inspect parsing without any network IO.
  const output = execFileSync("/bin/sh", ["-c", `curl() { printf '%s\\0' "$@"; }\n${command}`]);
  return output.toString().split("\0").slice(0, -1);
}

test("默认路由生成三种可直接使用的文本示例，202 文案仅表示入队", () => {
  const examples = buildIntegrationExamples(config, "", "text");
  const shoutrrr = new URL(examples.shoutrrr);
  assert.equal(shoutrrr.protocol, "generic:");
  assert.equal(shoutrrr.host, "notify.example.com");
  assert.equal(shoutrrr.pathname, "/api/v1/notify");
  assert.equal(shoutrrr.searchParams.get("template"), "json");
  assert.equal(shoutrrr.searchParams.get("@Authorization"), `Bearer ${config.ingressToken}`);
  assert.equal(shoutrrr.searchParams.has("caller"), false);
  assert.equal(shoutrrr.searchParams.has("$format"), false);
  assert.equal(shoutrrr.searchParams.has("disabletls"), false);
  const args = curlArguments(examples.curl);
  assert.deepEqual(args, [
    "--request", "POST", config.notifyUrl,
    "--header", `Authorization: Bearer ${config.ingressToken}`,
    "--header", "Content-Type: application/json",
    "--data", JSON.stringify({ title: "接入测试", message: "Posthaste 接入测试消息", format: "text" }),
  ]);
  assert.match(examples.go, /^package main\n/);
  assert.match(examples.go, /"github\.com\/real-uangi\/posthaste\/sdk\/go"/);
  assert.match(examples.go, /BaseURL: "https:\/\/notify\.example\.com"/);
  assert.match(examples.go, /Caller:  ""/);
  assert.match(examples.go, /Format:  posthaste\.FormatText/);
  assert.match(examples.go, /fmt\.Println\("已入队", accepted\.MessageID\)/);
  assert.equal(examples.go.match(/os\.Exit\(1\)/g)?.length, 2);
  assert.equal(examples.go.includes("送达"), false);
});

test("caller 和 Token 原样编码，保留空格、大小写及 URL 特殊字符", () => {
  const caller = " Beszel /运维?&+=#% ";
  const token = "token /?&+=#%:汉字";
  const examples = buildIntegrationExamples({ ...config, ingressToken: token }, caller, "text");
  const shoutrrr = new URL(examples.shoutrrr);
  assert.equal(shoutrrr.searchParams.get("caller"), caller);
  assert.equal(shoutrrr.searchParams.get("@Authorization"), `Bearer ${token}`);
  assert.ok(examples.shoutrrr.includes(`caller=${encodeURIComponent(caller)}`));
  assert.ok(examples.shoutrrr.includes(`@Authorization=${encodeURIComponent(`Bearer ${token}`)}`));
  const args = curlArguments(examples.curl);
  assert.equal(new URL(args[2]).searchParams.get("caller"), caller);
  assert.equal(args[4], `Authorization: Bearer ${token}`);
});

test("Markdown 同步映射到 Generic 附加字段、API 正文及 SDK 枚举", () => {
  const examples = buildIntegrationExamples(config, "beszel", "markdown");
  assert.equal(new URL(examples.shoutrrr).searchParams.get("$format"), "markdown");
  const args = curlArguments(examples.curl);
  assert.deepEqual(JSON.parse(args[8]), {
    title: "接入测试", message: "**Posthaste** 接入测试消息", format: "markdown",
  });
  assert.match(examples.go, /Message: "\*\*Posthaste\*\* 接入测试消息"/);
  assert.match(examples.go, /Format:  posthaste\.FormatMarkdown/);
});

test("HTTP、端口和 IPv6 保留请求地址，只对 HTTP 设置 disabletls", () => {
  for (const notifyUrl of [
    "http://localhost:8080/api/v1/notify",
    "http://[::1]:8080/api/v1/notify",
    "https://[2001:db8::1]:8443/api/v1/notify",
  ]) {
    const source = new URL(notifyUrl);
    const examples = buildIntegrationExamples({ ...config, notifyUrl }, "default", "text");
    const shoutrrr = new URL(examples.shoutrrr);
    assert.equal(shoutrrr.host, source.host);
    assert.equal(shoutrrr.searchParams.get("disabletls"), source.protocol === "http:" ? "yes" : null);
    assert.equal(new URL(curlArguments(examples.curl)[2]).origin, source.origin);
    assert.ok(examples.go.includes(`BaseURL: ${JSON.stringify(source.origin)}`));
  }
});

test("POSIX shell 引号防止 Token 中的引号、反引号和命令替换改变参数", () => {
  const token = "a'b\"c\\d`printf backtick`$(printf substitution)\nnext";
  const caller = "caller'\"`$()\\";
  const examples = buildIntegrationExamples({ ...config, ingressToken: token }, caller, "markdown");
  const args = curlArguments(examples.curl);
  assert.equal(args.length, 9);
  assert.equal(args[4], `Authorization: Bearer ${token}`);
  assert.equal(new URL(args[2]).searchParams.get("caller"), caller);
  assert.equal(JSON.parse(args[8]).message, "**Posthaste** 接入测试消息");
});

test("Go 字符串保留引号、反斜杠、Unicode 和控制字符，不产生原始换行", () => {
  const token = "token\"\\\n\r\t\b\f\0\x1b汉字𠮷";
  const caller = "caller\"\\\n\r\t\0";
  const examples = buildIntegrationExamples({ ...config, ingressToken: token }, caller, "text");
  const tokenLiteral = examples.go.match(/^\t\tToken:   (.+),$/m)?.[1];
  const callerLiteral = examples.go.match(/^\t\tCaller:  (.+),$/m)?.[1];
  assert.ok(tokenLiteral);
  assert.ok(callerLiteral);
  assert.equal(JSON.parse(tokenLiteral), token);
  assert.equal(JSON.parse(callerLiteral), caller);
  assert.equal(/[\u0000-\u001f]/.test(tokenLiteral), false);
  assert.equal(/[\u0000-\u001f]/.test(callerLiteral), false);
});
