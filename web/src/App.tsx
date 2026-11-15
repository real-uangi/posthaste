import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { api, APIError, errorMessage } from "./api.js";
import { Dialog, EmptyState, Notice } from "./components.js";
import GroupEditor from "./GroupEditor.js";
import AccountEditor from "./AccountEditor.js";
import TargetEditor from "./TargetEditor.js";
import TestDialog from "./TestDialog.js";
import { feishuAccountOccupied, targetAvailability } from "./forms.js";
import { platformLabels } from "./types.js";
import type { Account, Session, Target, TargetGroup } from "./types.js";

function Brand() {
  return (
    <div className="brand" aria-label="Posthaste">
      <span className="brand-mark" aria-hidden="true">P<span /></span>
      <span>Posthaste<span className="brand-period">.</span></span>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [checking, setChecking] = useState(true);
  const [initialError, setInitialError] = useState("");
  const [loginNotice, setLoginNotice] = useState("");

  async function checkSession(signal?: AbortSignal) {
    setChecking(true);
    setInitialError("");
    try {
      setSession(await api.session(signal));
    } catch (error) {
      if (signal?.aborted) return;
      if (!(error instanceof APIError && error.status === 401)) {
        setInitialError(errorMessage(error));
      }
    } finally {
      if (!signal?.aborted) setChecking(false);
    }
  }
  useEffect(() => {
    const controller = new AbortController();
    void checkSession(controller.signal);
    return () => controller.abort();
  }, []);

  if (session) {
    return (
      <Console
        onSignOut={() => {
          setSession(null);
          setLoginNotice("");
        }}
        onUnauthorized={() => {
          setSession(null);
          setLoginNotice("会话已过期，请重新登录。");
        }}
      />
    );
  }
  return (
    <div className="auth-page">
      <header className="auth-header"><Brand /><span className="subtle-label">管理控制台</span></header>
      <main className="auth-main">
        {checking ? (
          <div className="auth-card" role="status" aria-label="正在检查会话"><div className="skeleton w-32 h-7" /><div className="skeleton w-full h-12 mt-8" /><p className="muted mt-5">正在检查登录状态…</p></div>
        ) : initialError ? (
          <div className="auth-card"><h1>暂时无法连接</h1><Notice tone="error">{initialError}</Notice><button className="button button-primary mt-5" onClick={() => void checkSession()}>重新连接</button></div>
        ) : (
          <Login notice={loginNotice} onAuthenticated={(value) => { setSession(value); setLoginNotice(""); }} />
        )}
      </main>
    </div>
  );
}

function Login({ notice, onAuthenticated }: { notice: string; onAuthenticated: (session: Session) => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      onAuthenticated(await api.login(password));
    } catch (error) {
      setError(errorMessage(error));
    } finally {
      setPassword("");
      setBusy(false);
    }
  }
  return (
    <div className="auth-card">
      <p className="eyebrow">POSTHASTE ADMIN</p>
      <h1>管理你的推送。</h1>
      <p className="muted auth-intro">使用管理员口令登录，管理账号、群组出口与路由。</p>
      <form onSubmit={login} className="form-stack">
        {notice && <Notice>{notice}</Notice>}
        {error && <Notice tone="error">{error}</Notice>}
        <label className="field"><span>管理员口令</span><input type="password" required autoFocus autoComplete="current-password" value={password} disabled={busy} onChange={(e) => setPassword(e.target.value)} /></label>
        <button className="button button-primary login-button" type="submit" disabled={busy}>{busy ? "正在登录…" : "登录管理后台"}</button>
      </form>
    </div>
  );
}

type Section = "accounts" | "targets" | "groups";
type Editor =
  | { section: "accounts"; account?: Account }
  | { section: "targets"; target?: Target; initialAccountId?: string }
  | { section: "groups"; group?: TargetGroup };
type Deletion =
  | { section: "accounts"; item: Account }
  | { section: "targets"; item: Target }
  | { section: "groups"; item: TargetGroup };

const sections: Record<Section, { label: string; create: string; description: string }> = {
  accounts: { label: "账号", create: "添加账号", description: "统一管理机器人凭证及其群组出口。" },
  targets: { label: "群组出口", create: "添加出口", description: "配置账号下需要接收消息的群组与话题。" },
  groups: { label: "路由组", create: "添加路由组", description: "按 Caller 将消息分发到预设群组出口。" },
};

function Console({ onUnauthorized, onSignOut }: { onUnauthorized: () => void; onSignOut: () => void }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [targets, setTargets] = useState<Target[]>([]);
  const [groups, setGroups] = useState<TargetGroup[]>([]);
  const [section, setSection] = useState<Section>("accounts");
  const [filterAccountId, setFilterAccountId] = useState("");
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  const [refreshing, setRefreshing] = useState(false);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [testing, setTesting] = useState<{ target: Target; account: Account } | null>(null);
  const [deletion, setDeletion] = useState<Deletion | null>(null);
  const [deleteError, setDeleteError] = useState("");
  const [pending, setPending] = useState("");
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(null);

  function failed(error: unknown) {
    if (error instanceof APIError && error.status === 401) onUnauthorized();
    else setNotice({ text: errorMessage(error), error: true });
  }
  async function load(initial = false, signal?: AbortSignal) {
    if (initial) setStatus("loading");
    else setRefreshing(true);
    try {
      const [accountList, targetList, groupList] = await Promise.all([api.accounts(signal), api.targets(signal), api.groups(signal)]);
      setAccounts(accountList.items);
      setTargets(targetList.items);
      setGroups([...groupList.items].sort((a, b) => Number(b.caller === "default") - Number(a.caller === "default")));
      setFilterAccountId((current) => accountList.items.some((item) => item.id === current) ? current : "");
      setStatus("ready");
    } catch (error) {
      if (signal?.aborted) return;
      if (initial) setStatus("failed");
      failed(error);
    } finally {
      if (!signal?.aborted) setRefreshing(false);
    }
  }
  useEffect(() => {
    const controller = new AbortController();
    void load(true, controller.signal);
    return () => controller.abort();
  }, []);

  async function logout() {
    setPending("logout");
    try {
      await api.logout();
      onSignOut();
    } catch (error) {
      failed(error);
    } finally {
      setPending("");
    }
  }
  async function toggleAccount(account: Account) {
    setPending(account.id);
    setNotice(null);
    try {
      // 账号开关和凭证均独立于出口；省略 credentials，不改写任何子出口开关。
      await api.saveAccount({ id: account.id, name: account.name, platform: account.platform, enabled: !account.enabled });
      setNotice({ text: account.enabled ? "账号已停用，其群组出口已暂停推送。" : "账号已启用。" });
      await load();
    } catch (error) {
      failed(error);
    } finally {
      setPending("");
    }
  }
  async function toggleTarget(target: Target) {
    setPending(target.id);
    setNotice(null);
    try {
      await api.saveTarget({ id: target.id, name: target.name, accountId: target.accountId, enabled: !target.enabled, config: target.config });
      setNotice({ text: target.enabled ? "群组出口已停用。" : "群组出口开关已启用；所属账号也需启用才能推送。" });
      await load();
    } catch (error) {
      failed(error);
    } finally {
      setPending("");
    }
  }
  async function deleteItem() {
    if (!deletion) return;
    setPending("delete");
    setDeleteError("");
    try {
      if (deletion.section === "accounts") await api.deleteAccount(deletion.item.id);
      else if (deletion.section === "targets") await api.deleteTarget(deletion.item.id);
      else await api.deleteGroup(deletion.item.id);
      setDeletion(null);
      setNotice({ text: `${sections[deletion.section].label}已删除。` });
      await load();
    } catch (error) {
      if (error instanceof APIError && error.status === 401) onUnauthorized();
      else setDeleteError(errorMessage(error));
    } finally {
      setPending("");
    }
  }
  function confirmDelete(item: Deletion) {
    setDeleteError("");
    setDeletion(item);
  }
  async function saved() {
    setNotice({ text: editor ? `${sections[editor.section].label}已保存。` : "配置已保存。" });
    setEditor(null);
    await load();
  }
  function viewAccountTargets(account: Account) {
    setSection("targets");
    setFilterAccountId(account.id);
    setNotice(null);
  }
  function addTarget(accountId?: string) {
    setSection("targets");
    if (accountId) setFilterAccountId(accountId);
    setEditor({ section: "targets", initialAccountId: accountId });
    setNotice(null);
  }
  function createItem() {
    setNotice(null);
    if (section === "targets") addTarget(filterAccountId || undefined);
    else if (section === "accounts") setEditor({ section: "accounts" });
    else setEditor({ section: "groups" });
  }

  const locked = Boolean(editor) || Boolean(pending) || refreshing;
  const filteredAccount = accounts.find((item) => item.id === filterAccountId);
  const visibleTargets = filterAccountId ? targets.filter((target) => target.accountId === filterAccountId) : targets;
  const count = { accounts: accounts.length, targets: targets.length, groups: groups.length };
  const createBlocked = section === "targets" && (accounts.length === 0 || Boolean(filteredAccount && feishuAccountOccupied(filteredAccount, targets)));
  return (
    <div className="app-shell">
      <header className="app-header">
        <Brand />
        <div className="header-end"><span className="admin-label">管理员</span><button className="button button-quiet" disabled={Boolean(pending)} onClick={() => void logout()}>{pending === "logout" ? "正在退出…" : "退出登录"}</button></div>
      </header>
      <div className="app-body">
        <nav className="side-nav" aria-label="管理导航">
          <p className="nav-caption">配置管理</p>
          {(["accounts", "targets", "groups"] as const).map((key) => <button key={key} aria-current={section === key ? "page" : undefined} disabled={locked && section !== key} onClick={() => { setSection(key); setNotice(null); }}><span>{sections[key].label}</span>{status === "ready" && <span className="nav-count">{count[key]}</span>}</button>)}
          <div className="nav-footnote"><span className="nav-footnote-line" />Posthaste 控制台</div>
        </nav>
        <main className="main-content">
          <div className="page-heading">
            <div><p className="eyebrow">消息推送 / 配置</p><h1>{sections[section].label}</h1><p className="muted">{sections[section].description}</p></div>
            <div className="heading-actions"><button className="button" disabled={locked || status === "loading"} onClick={() => void load(status === "failed")}>{refreshing ? "正在刷新…" : "刷新"}</button><button className="button button-primary" disabled={locked || status !== "ready" || createBlocked} title={createBlocked ? accounts.length === 0 ? "请先添加账号" : "此飞书群Webhook账号已经配置出口" : undefined} onClick={createItem}>{sections[section].create}</button></div>
          </div>
          {notice && <Notice tone={notice.error ? "error" : "info"} onDismiss={() => setNotice(null)}>{notice.text}</Notice>}
          {status === "loading" ? (
            <div className="loading-list" role="status" aria-label="正在加载配置">{[0, 1, 2].map((key) => <div className="loading-row" key={key}><div className="skeleton h-11 w-11" /><div className="grow"><div className="skeleton h-4 w-40" /><div className="skeleton h-3 w-56 mt-3" /></div></div>)}</div>
          ) : status === "failed" ? (
            <EmptyState title="暂时无法加载配置"><p>请检查服务连接后重新加载。</p><button className="button mt-4" onClick={() => void load(true)}>重新加载</button></EmptyState>
          ) : (
            <>
              {section === "targets" && <div className="account-filter"><label htmlFor="account-filter">所属账号</label><select id="account-filter" value={filterAccountId} disabled={locked} onChange={(e) => setFilterAccountId(e.target.value)}><option value="">全部账号</option>{accounts.map((account) => <option key={account.id} value={account.id}>{account.name}{!account.enabled ? " · 账号停用" : ""}</option>)}</select>{filteredAccount && !filteredAccount.enabled && <span className="warning-text">账号已停用，出口开关保持原状。</span>}</div>}
              <div className={`workspace ${editor ? "has-editor" : ""}`}>
                <section className="resource-list" aria-label={`${sections[section].label}列表`}>
                  <div className="list-caption"><span>{section === "targets" && filteredAccount ? `${filteredAccount.name}的出口` : `已配置${sections[section].label}`}</span><span className="mono">{section === "targets" ? visibleTargets.length : count[section]}</span></div>
                  {section === "accounts" && (
                    accounts.length === 0 ? <EmptyState title="还没有账号"><p>添加 Telegram Bot、飞书群Webhook或飞书应用机器人，再配置账号下的群组出口。</p></EmptyState> : accounts.map((account) => {
                      const children = targets.filter((target) => target.accountId === account.id);
                      const selected = editor?.section === "accounts" && editor.account?.id === account.id;
                      return (
                        <article className={`resource-row account-row ${selected ? "is-selected" : ""}`} key={account.id}>
                          <div className="resource-identity"><span className="platform-mark" aria-hidden="true">{account.platform === "telegram" ? "T" : "F"}</span><div className="resource-copy"><div className="resource-title"><h2>{account.name}</h2><span className={`status-tag ${account.enabled ? "is-enabled" : ""}`}>{account.enabled ? "启用" : "停用"}</span></div><p className="resource-meta">{platformLabels[account.platform]}<span>·</span>{children.length} 个群组出口</p><p className="target-detail">{account.credentialsConfigured ? "凭证已配置" : "凭证未配置"}{account.platform === "feishu" ? " · Webhook 固定一个飞书群" : ""}</p></div></div>
                          <div className="row-actions account-actions"><button className="text-button" disabled={locked} onClick={() => viewAccountTargets(account)}>查看出口</button><button className="text-button" disabled={locked || feishuAccountOccupied(account, targets)} title={feishuAccountOccupied(account, targets) ? "此Webhook账号只能配置一个出口" : undefined} onClick={() => addTarget(account.id)}>添加出口</button><button className="text-button" disabled={locked} onClick={() => setEditor({ section: "accounts", account })}>编辑</button><button className="text-button" disabled={locked} onClick={() => void toggleAccount(account)}>{pending === account.id ? "处理中…" : account.enabled ? "停用" : "启用"}</button><button className="text-button danger-text" disabled={locked || children.length > 0} title={children.length > 0 ? "请先删除此账号下的全部出口（含停用出口）" : undefined} onClick={() => confirmDelete({ section: "accounts", item: account })}>删除</button></div>
                        </article>
                      );
                    })
                  )}
                  {section === "targets" && (
                    visibleTargets.length === 0 ? <EmptyState title={filteredAccount ? "此账号还没有群组出口" : "还没有群组出口"}><p>为账号配置接收消息的群组或话题。</p>{accounts.length === 0 && <button className="button mt-4" onClick={() => { setSection("accounts"); setEditor({ section: "accounts" }); }}>添加账号</button>}</EmptyState> : visibleTargets.map((target) => {
                      const account = accounts.find((item) => item.id === target.accountId);
                      const state = targetAvailability(target, account);
                      const boundCount = groups.filter((group) => group.targetIds.includes(target.id)).length;
                      const selected = editor?.section === "targets" && editor.target?.id === target.id;
                      return (
                        <article className={`resource-row ${selected ? "is-selected" : ""}`} key={target.id}>
                          <div className="resource-identity"><span className="platform-mark" aria-hidden="true">{account ? account.platform === "telegram" ? "T" : "F" : "—"}</span><div className="resource-copy"><div className="resource-title"><h2>{target.name}</h2><span className={`status-tag ${state.enabled ? "is-enabled" : ""}`}>{state.label}</span></div><p className="resource-meta">{account?.name ?? "账号不可用，请刷新"}<span>·</span>{boundCount} 个路由组{account && !account.enabled && <><span>·</span>出口开关{target.enabled ? "启用" : "停用"}</>}</p>{(account?.platform === "telegram" || account?.platform === "feishu_app") && <p className="target-detail mono">Chat ID {target.config.chatId}{account.platform === "telegram" && target.config.messageThreadId ? ` / 话题 ${target.config.messageThreadId}` : ""}</p>}</div></div>
                          <div className="row-actions"><button className="text-button" disabled={locked || !state.enabled} title={!state.enabled ? state.label : undefined} onClick={() => { if (account) setTesting({ target, account }); }}>测试</button><button className="text-button" disabled={locked} onClick={() => setEditor({ section: "targets", target })}>编辑</button><button className="text-button" disabled={locked} onClick={() => void toggleTarget(target)}>{pending === target.id ? "处理中…" : target.enabled ? "停用" : "启用"}</button><button className="text-button danger-text" disabled={locked || boundCount > 0} title={boundCount > 0 ? "请先解除路由组绑定" : undefined} onClick={() => confirmDelete({ section: "targets", item: target })}>删除</button></div>
                        </article>
                      );
                    })
                  )}
                  {section === "groups" && (
                    groups.length === 0 ? <EmptyState title="还没有路由组"><p>创建路由组并绑定群组出口。</p></EmptyState> : groups.map((group) => {
                      const members = targets.filter((target) => group.targetIds.includes(target.id));
                      const active = members.filter((target) => targetAvailability(target, accounts.find((account) => account.id === target.accountId)).enabled).length;
                      const selected = editor?.section === "groups" && editor.group?.id === group.id;
                      return (
                        <article className={`resource-row group-row ${selected ? "is-selected" : ""}`} key={group.id}>
                          <div className="resource-copy"><div className="resource-title"><h2>{group.name}</h2>{group.caller === "default" && <span className="default-tag">默认路由</span>}</div><div className="resource-meta"><code className="caller-code">{group.caller}</code><span>·</span><span>{active} / {members.length} 个出口可用</span></div>{group.caller === "default" && <p className="target-detail">未指定或未匹配 Caller 时使用。</p>}<div className="member-tags">{members.length === 0 ? <span className="empty-members">尚未绑定出口</span> : members.map((target) => { const state = targetAvailability(target, accounts.find((account) => account.id === target.accountId)); return <span className={`member-tag ${!state.enabled ? "is-disabled" : ""}`} key={target.id}>{target.name}{!state.enabled && ` · ${state.label}`}</span>; })}</div></div>
                          <div className="row-actions"><button className="text-button" disabled={locked} onClick={() => setEditor({ section: "groups", group })}>编辑</button>{group.caller !== "default" && <button className="text-button danger-text" disabled={locked} onClick={() => confirmDelete({ section: "groups", item: group })}>删除</button>}</div>
                        </article>
                      );
                    })
                  )}
                </section>
                {editor?.section === "accounts" && <AccountEditor key={editor.account?.id ?? "new-account"} account={editor.account} onSaved={saved} onCancel={() => setEditor(null)} onUnauthorized={onUnauthorized} />}
                {editor?.section === "targets" && <TargetEditor key={editor.target?.id ?? "new-target"} target={editor.target} accounts={accounts} targets={targets} initialAccountId={editor.initialAccountId} onSaved={saved} onCancel={() => setEditor(null)} onUnauthorized={onUnauthorized} />}
                {editor?.section === "groups" && <GroupEditor key={editor.group?.id ?? "new-group"} group={editor.group} targets={targets} accounts={accounts} onSaved={saved} onCancel={() => setEditor(null)} onUnauthorized={onUnauthorized} />}
              </div>
            </>
          )}
        </main>
      </div>
      {testing && <TestDialog target={testing.target} account={testing.account} onClose={() => setTesting(null)} onUnauthorized={onUnauthorized} />}
      {deletion && <Dialog title={`删除${sections[deletion.section].label}`} onClose={() => setDeletion(null)} busy={pending === "delete"}><div className="dialog-content"><p>确认删除 <strong>{deletion.item.name}</strong>？</p><p className="muted mt-3">{deletion.section === "groups" ? "删除后，使用该 Caller 的消息将进入默认路由组。" : deletion.section === "accounts" ? "删除账号会移除其保存的凭证。" : "删除后需要重新创建群组出口才能再次使用。"}</p>{deleteError && <div className="mt-4"><Notice tone="error">{deleteError}</Notice></div>}<div className="form-actions"><button className="button button-danger" disabled={pending === "delete"} onClick={() => void deleteItem()}>{pending === "delete" ? "正在删除…" : "确认删除"}</button><button className="button" disabled={pending === "delete"} onClick={() => setDeletion(null)}>取消</button></div></div></Dialog>}
    </div>
  );
}
