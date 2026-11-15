import { useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Notice } from "./components.js";
import { buildGroupInput } from "./forms.js";
import { targetAvailability } from "./forms.js";
import { platformLabels } from "./types.js";
import type { Account, Target, TargetGroup } from "./types.js";

export default function GroupEditor({
  group,
  targets,
  accounts,
  onSaved,
  onCancel,
  onUnauthorized,
}: {
  group?: TargetGroup;
  targets: Target[];
  accounts: Account[];
  onSaved: () => Promise<void>;
  onCancel: () => void;
  onUnauthorized: () => void;
}) {
  const [name, setName] = useState(group?.name ?? "");
  const [caller, setCaller] = useState(group?.caller ?? "");
  const [targetIds, setTargetIds] = useState(group?.targetIds ?? []);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const isDefault = group?.caller === "default";
  const activeCount = targets.filter((target) => targetIds.includes(target.id) && targetAvailability(target, accounts.find((account) => account.id === target.accountId)).enabled).length;
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSaving(true);
    try {
      await api.saveGroup(buildGroupInput({ name, caller, targetIds }, group));
      await onSaved();
    } catch (error) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setError(errorMessage(error));
    } finally {
      setSaving(false);
    }
  }

  return (
    <aside className="editor" aria-label={group ? "编辑路由组" : "新建路由组"}>
      <div className="editor-heading">
        <div>
          <p className="eyebrow">路由组</p>
          <h2>{isDefault ? "默认路由组配置" : group ? "编辑路由组" : "添加路由组"}</h2>
        </div>
        <button className="text-button" type="button" onClick={onCancel} disabled={saving}>取消</button>
      </div>
      <form onSubmit={save} className="form-stack">
        {error && <Notice tone="error">{error}</Notice>}
        <fieldset disabled={saving}>
          <label className="field">
            <span>路由组名称</span>
            <input required autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：Beszel 告警" />
          </label>
          <label className="field">
            <span>Caller</span>
            <input required value={caller} disabled={isDefault} onChange={(e) => setCaller(e.target.value)} placeholder="例如：beszel" autoCapitalize="none" autoComplete="off" spellCheck={false} />
          </label>
          <p className="field-note">{isDefault ? "未指定或未匹配 Caller 的消息使用此组。" : "与消息请求中的 caller 精确匹配，区分大小写。"}</p>
          <div className="form-section">
            <div className="section-caption"><h3>绑定群组出口</h3><span className="subtle-label">已选 {targetIds.length} 个</span></div>
            {targets.length === 0 ? (
              <p className="field-note">还没有群组出口，可以先保存空路由组，再添加绑定。</p>
            ) : (
              <div className="target-options">
                {targets.map((target) => {
                  const account = accounts.find((item) => item.id === target.accountId);
                  const state = targetAvailability(target, account);
                  return (
                  <label className="target-option" key={target.id}>
                    <input type="checkbox" checked={targetIds.includes(target.id)} onChange={(event) => setTargetIds((current) => event.target.checked ? [...current, target.id] : current.filter((id) => id !== target.id))} />
                    <span className="option-name">{target.name}<small>{account ? `${account.name} · ${platformLabels[account.platform]}` : "账号不可用，请刷新配置"}</small></span>
                    {!state.enabled && <span className="subtle-label">{state.label}</span>}
                  </label>
                );})}
              </div>
            )}
            {activeCount === 0 && <p className="field-note warning-text">当前没有可用的绑定出口，发往此路由组的消息将不被受理。</p>}
          </div>
          <div className="form-actions">
            <button className="button button-primary" type="submit">{saving ? "正在保存…" : "保存路由组"}</button>
            <button className="button" type="button" onClick={onCancel}>取消</button>
          </div>
        </fieldset>
      </form>
    </aside>
  );
}
