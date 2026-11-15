import { useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Dialog, Notice } from "./components.js";
import type { Account, Format, Target } from "./types.js";

export default function TestDialog({ target, account, onClose, onUnauthorized }: {
  target: Target;
  account: Account;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const [title, setTitle] = useState("Posthaste 测试消息");
  const [message, setMessage] = useState("这是一条来自 Posthaste 的测试消息。");
  const [format, setFormat] = useState<Format>("text");
  const [sending, setSending] = useState(false);
  const [accepted, setAccepted] = useState("");
  const [error, setError] = useState("");
  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setAccepted("");
    setSending(true);
    try {
      const result = await api.testTarget({ id: target.id, title, message, format });
      setAccepted(result.messageId);
    } catch (error) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setError(errorMessage(error));
    } finally {
      setSending(false);
    }
  }
  return (
    <Dialog title="测试群组出口" onClose={onClose} busy={sending}>
      <p className="dialog-description">通过 {account.name} 发送到 <strong>{target.name}</strong></p>
      <form onSubmit={send} className="form-stack dialog-content">
        {error && <Notice tone="error">{error}</Notice>}
        {accepted && <Notice><strong>测试消息已入队，请在目标平台确认。</strong><span className="accepted-id">消息编号：{accepted}</span></Notice>}
        <fieldset disabled={sending}>
          <label className="field"><span>标题 <small>可选</small></span><input value={title} onChange={(e) => setTitle(e.target.value)} autoFocus /></label>
          <label className="field"><span>正文</span><textarea required rows={5} value={message} onChange={(e) => setMessage(e.target.value)} /></label>
          <label className="field"><span>消息格式</span><select value={format} onChange={(e) => setFormat(e.target.value as Format)}><option value="text">纯文本</option><option value="markdown">Markdown</option></select></label>
          <div className="form-actions"><button className="button button-primary" type="submit">{sending ? "正在提交…" : "发送测试消息"}</button><button className="button" type="button" onClick={onClose}>关闭</button></div>
        </fieldset>
      </form>
    </Dialog>
  );
}
