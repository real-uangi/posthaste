import { useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Notice } from "./components.js";
import { accountDraft, buildAccountInput } from "./forms.js";
import type { AccountDraft } from "./forms.js";
import type { Account, Platform } from "./types.js";

export default function AccountEditor({ account, onSaved, onCancel, onUnauthorized }: {
  account?: Account;
  onSaved: () => Promise<void>;
  onCancel: () => void;
  onUnauthorized: () => void;
}) {
  const [draft, setDraft] = useState(() => accountDraft(account));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  function set<K extends keyof AccountDraft>(key: K, value: AccountDraft[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSaving(true);
    try {
      await api.saveAccount(buildAccountInput(draft, account));
      await onSaved();
    } catch (error) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setError(errorMessage(error));
    } finally {
      setSaving(false);
    }
  }

  return (
    <aside className="editor" aria-label={account ? "编辑账号" : "新建账号"}>
      <div className="editor-heading">
        <div><p className="eyebrow">账号</p><h2>{account ? "编辑账号" : "添加账号"}</h2></div>
        <button className="text-button" type="button" onClick={onCancel} disabled={saving}>取消</button>
      </div>
      <form onSubmit={save} className="form-stack">
        {error && <Notice tone="error">{error}</Notice>}
        <fieldset disabled={saving}>
          <label className="field"><span>账号名称</span><input required autoFocus value={draft.name} onChange={(e) => set("name", e.target.value)} placeholder="例如：运维通知机器人" /></label>
          <label className="field">
            <span>平台类型</span>
            <select value={draft.platform} disabled={Boolean(account)} onChange={(e) => set("platform", e.target.value as Platform)}>
              <option value="telegram">Telegram Bot</option>
              <option value="feishu">飞书群Webhook</option>
              <option value="feishu_app">飞书应用机器人</option>
            </select>
          </label>
          {draft.platform === "feishu" && <p className="field-note">一个 Webhook 固定一个飞书群，每个账号只能配置一个群组出口。</p>}
          {draft.platform === "feishu_app" && <p className="field-note">支持国内飞书企业自建应用。先保存账号，再从机器人已加入的群中选择群组出口。</p>}
          <div className="form-section">
            <div className="section-caption"><h3>平台凭证</h3>{account?.credentialsConfigured && <span className="subtle-label">已配置</span>}</div>
            {account?.credentialsConfigured && <label className="check-line"><input type="checkbox" checked={draft.replaceCredentials} onChange={(e) => set("replaceCredentials", e.target.checked)} /><span>更换已保存的凭证</span></label>}
            {!draft.replaceCredentials && <p className="field-note">本次保存将保留现有凭证。</p>}
            {draft.replaceCredentials && draft.platform === "telegram" && <label className="field"><span>Bot Token</span><input required type="password" autoComplete="new-password" value={draft.botToken} onChange={(e) => set("botToken", e.target.value)} placeholder="填写 BotFather 提供的 Token" spellCheck={false} /></label>}
            {draft.replaceCredentials && draft.platform === "feishu" && <>
              <label className="field"><span>机器人 Webhook</span><input required type="password" autoComplete="new-password" value={draft.webhookUrl} onChange={(e) => set("webhookUrl", e.target.value)} placeholder="粘贴完整的 Webhook 地址" spellCheck={false} /></label>
              <label className="field"><span>签名密钥 <small>可选</small></span><input type="password" autoComplete="new-password" value={draft.signingSecret} onChange={(e) => set("signingSecret", e.target.value)} placeholder="机器人开启签名校验时填写" spellCheck={false} /></label>
              {account && <p className="field-note">凭证将整体替换；签名密钥留空会移除原签名配置。</p>}
            </>}
            {draft.replaceCredentials && draft.platform === "feishu_app" && <>
              <label className="field"><span>App ID</span><input required autoComplete="off" value={draft.appId} onChange={(e) => set("appId", e.target.value)} placeholder="填写企业自建应用的 App ID" spellCheck={false} /></label>
              <label className="field"><span>App Secret</span><input required type="password" autoComplete="new-password" value={draft.appSecret} onChange={(e) => set("appSecret", e.target.value)} placeholder="填写应用密钥" spellCheck={false} /></label>
              {account && <p className="field-note">App ID 和 App Secret 将整体替换，请填写完整凭证。</p>}
            </>}
            {account && draft.replaceCredentials && <p className="field-note warning-text">保存后，此账号下的所有群组出口都将使用新凭证。</p>}
          </div>
          <label className="check-line enabled-line"><input type="checkbox" checked={draft.enabled} onChange={(e) => set("enabled", e.target.checked)} /><span>启用这个账号</span></label>
          {account && !draft.enabled && <p className="field-note">停用账号会暂停其所有群组出口，不改变各出口自己的开关。</p>}
          <div className="form-actions"><button className="button button-primary" type="submit">{saving ? "正在保存…" : "保存账号"}</button><button className="button" type="button" onClick={onCancel}>取消</button></div>
        </fieldset>
      </form>
    </aside>
  );
}
