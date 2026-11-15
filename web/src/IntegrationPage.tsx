import { useEffect, useId, useState } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Notice } from "./components.js";
import { buildIntegrationExamples } from "./integration.js";
import type { Format, IntegrationConfig, TargetGroup } from "./types.js";

const methods = [
  { key: "shoutrrr", label: "Shoutrrr", kind: "配置 URL", description: "适用于 Beszel 等支持 Shoutrrr Generic 的服务。复制到通知 URL，标题与正文由接入方提供。" },
  { key: "curl", label: "cURL", kind: "HTTP API", description: "在终端通过统一 API 提交示例消息，使用入口 Bearer Token 鉴权。命令适用于 POSIX shell。" },
  { key: "go", label: "Go SDK", kind: "Go", description: "使用项目现有的独立 Go SDK 提交消息，客户端不自动重试。" },
] as const;

export default function IntegrationPage({ groups, onUnauthorized }: {
  groups: TargetGroup[];
  onUnauthorized: () => void;
}) {
  const [config, setConfig] = useState<IntegrationConfig | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setError("");
    async function load() {
      try {
        const value = await api.integrationConfig(controller.signal);
        if (!controller.signal.aborted) setConfig(value);
      } catch (error) {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && error.status === 401) onUnauthorized();
        else setError(errorMessage(error));
      }
    }
    void load();
    // 离开此页即取消读取；Token 仅存在于组件内存，不持久化到浏览器。
    return () => controller.abort();
  }, [attempt, onUnauthorized]);

  return (
    <div className="integration-page">
      {error ? <Notice tone="error">{error}<button className="text-button" type="button" onClick={() => setAttempt((value) => value + 1)}>重新加载</button></Notice>
        : !config && <p className="muted" role="status">正在读取接入配置…</p>}
      <IntegrationExamples config={config} groups={groups} />
    </div>
  );
}

export function IntegrationExamples({ config, groups }: {
  config: IntegrationConfig | null;
  groups: TargetGroup[];
}) {
  const [caller, setCaller] = useState("");
  const [format, setFormat] = useState<Format>("text");
  const [showToken, setShowToken] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(null);
  const groupListId = useId();
  const examples = config ? buildIntegrationExamples(config, caller, format) : null;
  // 预览与复制共用生成器，分别传入占位符和真实密钥，避免编码后遮罩遗漏。
  const preview = config ? buildIntegrationExamples({ ...config, ingressToken: showToken ? config.ingressToken : "YOUR_TOKEN" }, caller, format) : null;

  async function copy(text: string, label: string) {
    setNotice(null);
    try {
      await navigator.clipboard.writeText(text);
      setNotice({ text: `${label} 示例已复制，包含入口密钥。` });
    } catch {
      setNotice({ text: "复制失败，请显示密钥后手动选择代码复制。", error: true });
    }
  }

  return (
    <>
      <section className="integration-settings" aria-label="示例配置">
        <div className="integration-fields">
          <label className="field"><span>Caller <small>可选</small></span><input list={groupListId} value={caller} onChange={(event) => { setCaller(event.target.value); setNotice(null); }} placeholder="留空使用默认路由组" autoComplete="off" spellCheck={false} /><datalist id={groupListId}>{groups.map((group) => <option key={group.id} value={group.caller}>{group.name}</option>)}</datalist></label>
          <label className="field"><span>消息格式</span><select value={format} onChange={(event) => { setFormat(event.target.value as Format); setNotice(null); }}><option value="text">纯文本</option><option value="markdown">Markdown</option></select></label>
        </div>
        <p className="muted">Caller 区分大小写，按输入原文匹配。留空或未匹配时使用默认组；命中组没有启用的账号与出口时返回 503。</p>
        <div className="integration-token-control"><label className="check-line"><input type="checkbox" checked={showToken} disabled={!config} onChange={(event) => setShowToken(event.target.checked)} /><span>显示密钥</span></label><p className="muted">预览默认隐藏密钥，复制内容包含真实入口密钥。</p></div>
      </section>
      {notice && <Notice tone={notice.error ? "error" : "info"} onDismiss={() => setNotice(null)}>{notice.text}</Notice>}
      <div className="integration-methods">
        {methods.map((method) => <section className="integration-method" aria-labelledby={`${groupListId}-${method.key}`} key={method.key}>
          <div className="integration-method-heading"><div><p className="eyebrow">{method.kind}</p><h2 id={`${groupListId}-${method.key}`}>{method.label}</h2></div><button className="button" type="button" disabled={!examples} onClick={examples ? () => void copy(examples[method.key], method.label) : undefined}>复制{method.key === "shoutrrr" ? "配置" : method.key === "curl" ? "命令" : "代码"}（含入口密钥）</button></div>
          <p className="muted">{method.description}</p>
          {method.key === "shoutrrr" && <p className="muted">Generic 将 @Authorization 转为请求头，入口不支持 query Token。<a className="integration-link" href="https://beszel.dev/guide/notifications/generic" target="_blank" rel="noreferrer">查看 Beszel 接入说明</a></p>}
          {method.key === "go" && <p className="muted">SDK 要求 Go 1.26.0 或更新版本，目前尚无已发布版本标签；同仓开发请通过本地 replace 引用 sdk/go 模块。</p>}
          {preview ? <pre className="integration-code" tabIndex={0} aria-label={`${method.label} 示例`}><code>{preview[method.key]}</code></pre>
            : <div className="integration-code integration-code-loading" aria-label={`${method.label} 示例待加载`}><div className="skeleton h-4 w-56" /><div className="skeleton h-4 w-40 mt-3" /></div>}
        </section>)}
      </div>
      <Notice><strong>202 仅表示已入队。</strong> 消息进入当前进程的内存队列，不代表平台或用户已收到。没有自动重试或投递状态查询，重复提交可能重复推送。</Notice>
    </>
  );
}
