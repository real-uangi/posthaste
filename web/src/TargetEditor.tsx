import { useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Notice } from "./components.js";
import { buildTargetInput, feishuAccountOccupied, targetDraft } from "./forms.js";
import type { TargetDraft } from "./forms.js";
import { platformLabels } from "./types.js";
import type { Account, Target } from "./types.js";

export default function TargetEditor({ target, accounts, targets, initialAccountId, onSaved, onCancel, onUnauthorized }: {
  target?: Target;
  accounts: Account[];
  targets: Target[];
  initialAccountId?: string;
  onSaved: () => Promise<void>;
  onCancel: () => void;
  onUnauthorized: () => void;
}) {
  const [draft, setDraft] = useState(() => targetDraft(target, initialAccountId));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const account = accounts.find((item) => item.id === draft.accountId);
  function set<K extends keyof TargetDraft>(key: K, value: TargetDraft[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }
  function occupied(item: Account) {
    return feishuAccountOccupied(item, targets, target?.id);
  }
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSaving(true);
    try {
      await api.saveTarget(buildTargetInput(draft, account, target));
      await onSaved();
    } catch (error) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setError(errorMessage(error));
    } finally {
      setSaving(false);
    }
  }

  return (
    <aside className="editor" aria-label={target ? "编辑群组出口" : "新建群组出口"}>
      <div className="editor-heading">
        <div><p className="eyebrow">群组出口</p><h2>{target ? "编辑出口" : "添加出口"}</h2></div>
        <button className="text-button" type="button" onClick={onCancel} disabled={saving}>取消</button>
      </div>
      <form onSubmit={save} className="form-stack">
        {error && <Notice tone="error">{error}</Notice>}
        <fieldset disabled={saving}>
          <label className="field"><span>出口名称</span><input required autoFocus value={draft.name} onChange={(e) => set("name", e.target.value)} placeholder="例如：运维告警群或通知话题" /></label>
          <label className="field">
            <span>所属账号</span>
            <select required value={draft.accountId} onChange={(e) => set("accountId", e.target.value)}>
              <option value="" disabled>请选择账号</option>
              {accounts.map((item) => <option key={item.id} value={item.id} disabled={occupied(item)}>{item.name} · {platformLabels[item.platform]}{!item.enabled ? " · 账号停用" : ""}{occupied(item) ? " · 已有出口" : ""}</option>)}
            </select>
          </label>
          {account?.platform === "feishu" && <p className="field-note">该出口使用账号 Webhook 固定的飞书群，无需再填写群地址。</p>}
          {account && !account.enabled && <p className="field-note warning-text">所属账号已停用。可以保存配置，启用账号后此出口才可推送。</p>}
          {account?.platform === "telegram" && <div className="form-section">
            <div className="section-caption"><h3>群组与话题</h3></div>
            <label className="field"><span>Chat ID</span><input required value={draft.chatId} onChange={(e) => set("chatId", e.target.value)} placeholder="群组、频道或用户的 Chat ID" spellCheck={false} /></label>
            <label className="field"><span>话题 ID <small>可选</small></span><input type="number" min="1" step="1" value={draft.messageThreadId} onChange={(e) => set("messageThreadId", e.target.value)} placeholder="仅发送到指定话题时填写" /></label>
          </div>}
          <label className="check-line enabled-line"><input type="checkbox" checked={draft.enabled} onChange={(e) => set("enabled", e.target.checked)} /><span>启用这个群组出口</span></label>
          <div className="form-actions"><button className="button button-primary" type="submit" disabled={!account || occupied(account)}>{saving ? "正在保存…" : "保存出口"}</button><button className="button" type="button" onClick={onCancel}>取消</button></div>
        </fieldset>
      </form>
    </aside>
  );
}
