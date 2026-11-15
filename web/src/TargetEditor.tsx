import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Notice } from "./components.js";
import { buildTargetInput, feishuAccountOccupied, targetDraft } from "./forms.js";
import type { TargetDraft } from "./forms.js";
import { barkLevelLabels, platformLabels } from "./types.js";
import type { Account, AccountChatPage, BarkLevel, Target } from "./types.js";

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
  const appAccountId = account?.platform === "feishu_app" ? account.id : undefined;
  const [chatPage, setChatPage] = useState<AccountChatPage>({ items: [], hasMore: false, nextPageToken: "" });
  const [chatLoading, setChatLoading] = useState(Boolean(appAccountId));
  const [chatError, setChatError] = useState<{ message: string; pageToken?: string } | null>(null);
  const chatRequest = useRef<AbortController | null>(null);

  async function loadChats(accountId: string, pageToken?: string) {
    chatRequest.current?.abort();
    const controller = new AbortController();
    chatRequest.current = controller;
    setChatLoading(true);
    setChatError(null);
    try {
      const page = await api.accountChats(accountId, pageToken, controller.signal);
      if (controller.signal.aborted) return;
      setChatPage((current) => ({
        ...page,
        items: [...new Map((pageToken ? [...current.items, ...page.items] : page.items).map((item) => [item.chatId, item])).values()],
      }));
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setChatError({ message: errorMessage(error), pageToken });
    } finally {
      if (!controller.signal.aborted) setChatLoading(false);
    }
  }
  useEffect(() => {
    if (!appAccountId) return;
    void loadChats(appAccountId);
    return () => chatRequest.current?.abort();
  }, [appAccountId]);

  function changeAccount(accountId: string) {
    // 账号决定可选群；清空目的地并取消旧请求，避免异步结果混入新账号。
    chatRequest.current?.abort();
    setChatPage({ items: [], hasMore: false, nextPageToken: "" });
    setChatError(null);
    setChatLoading(accounts.some((item) => item.id === accountId && item.platform === "feishu_app"));
    setDraft((current) => ({ ...targetDraft(undefined, accountId), name: current.name, enabled: current.enabled }));
  }
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
            <select required value={draft.accountId} onChange={(e) => changeAccount(e.target.value)}>
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
          {account?.platform === "bark" && <div className="form-section">
            <div className="section-caption"><h3>Bark 提醒方式</h3></div>
            <label className="field"><span>通知分组 <small>可选</small></span><input value={draft.barkGroup} onChange={(e) => set("barkGroup", e.target.value)} placeholder="例如：运维告警" /></label>
            <label className="field"><span>通知等级</span><select value={draft.barkLevel} onChange={(e) => set("barkLevel", e.target.value as BarkLevel)}>{Object.entries(barkLevelLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
            {draft.barkLevel === "critical" && <p className="field-note">重要警告需要在手机端为 Bark 授权「重要警告」。</p>}
            <label className="field"><span>铃声 <small>可选</small></span><input value={draft.barkSound} onChange={(e) => set("barkSound", e.target.value)} placeholder="填写 Bark 内的铃声名称，留空使用默认铃声" spellCheck={false} /></label>
            <label className="check-line"><input type="checkbox" checked={draft.barkCall} onChange={(e) => set("barkCall", e.target.checked)} /><span>循环响铃 30 秒</span></label>
            <p className="field-note">循环响铃会重复播放通知提示音，不会拨打电话。</p>
            <label className="field"><span>点击跳转地址 <small>可选</small></span><input value={draft.barkUrl} onChange={(e) => set("barkUrl", e.target.value)} placeholder="https://example.com 或 App URL Scheme" spellCheck={false} /></label>
          </div>}
          {appAccountId && <div className="form-section">
            <div className="section-caption"><h3>飞书群</h3><button className="text-button" type="button" disabled={chatLoading} onClick={() => void loadChats(appAccountId)}>刷新群列表</button></div>
            <label className="field"><span>接收群</span><select required value={draft.chatId} onChange={(e) => set("chatId", e.target.value)}>
              <option value="" disabled>请选择机器人已加入的群</option>
              {/* 已保存群可能位于未加载的页面，列表失败或刷新也不能清空原配置。 */}
              {draft.chatId && !chatPage.items.some((item) => item.chatId === draft.chatId) && <option value={draft.chatId}>当前配置（{draft.chatId}）</option>}
              {chatPage.items.map((item) => <option key={item.chatId} value={item.chatId}>{item.name} · {item.chatId}</option>)}
            </select></label>
            {chatLoading && <p className="field-note" role="status">正在加载飞书群…</p>}
            {chatError && <Notice tone="error">{chatError.message}<button className="text-button" type="button" disabled={chatLoading} onClick={() => void loadChats(appAccountId, chatError.pageToken)}>重试</button></Notice>}
            {!chatLoading && !chatError && chatPage.items.length === 0 && <p className="field-note">机器人尚未加入可用群，请先在飞书中添加机器人，再刷新群列表。</p>}
            {chatPage.hasMore && !chatError && <button className="button" type="button" disabled={chatLoading} onClick={() => void loadChats(appAccountId, chatPage.nextPageToken)}>{chatLoading ? "正在加载…" : "加载更多"}</button>}
            {draft.chatId && !chatPage.items.some((item) => item.chatId === draft.chatId) && <p className="field-note">当前群尚未出现在已加载列表中，保存会保留此群配置。</p>}
          </div>}
          <label className="check-line enabled-line"><input type="checkbox" checked={draft.enabled} onChange={(e) => set("enabled", e.target.checked)} /><span>启用这个群组出口</span></label>
          <div className="form-actions"><button className="button button-primary" type="submit" disabled={!account || occupied(account) || Boolean(appAccountId && !draft.chatId)}>{saving ? "正在保存…" : "保存出口"}</button><button className="button" type="button" onClick={onCancel}>取消</button></div>
        </fieldset>
      </form>
    </aside>
  );
}
