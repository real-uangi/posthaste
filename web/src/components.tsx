import { useEffect, useId, useRef } from "react";
import type { ReactNode } from "react";

export function Notice({
  children,
  tone = "info",
  onDismiss,
}: {
  children: ReactNode;
  tone?: "info" | "error";
  onDismiss?: () => void;
}) {
  return (
    <div
      className={`notice ${tone === "error" ? "notice-error" : ""}`}
      role={tone === "error" ? "alert" : "status"}
    >
      <div>{children}</div>
      {onDismiss && (
        <button className="text-button" onClick={onDismiss} type="button">
          关闭
        </button>
      )}
    </div>
  );
}

export function Dialog({
  title,
  children,
  busy = false,
  onClose,
}: {
  title: string;
  children: ReactNode;
  busy?: boolean;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    return () => dialog.close();
  }, []);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
    >
      <div className="dialog-heading">
        <h2 id={titleId}>{title}</h2>
        <button
          type="button"
          className="text-button"
          onClick={onClose}
          disabled={busy}
        >
          关闭
        </button>
      </div>
      {children}
    </dialog>
  );
}

export function EmptyState({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <span className="empty-mark" aria-hidden="true">
        —
      </span>
      <h3>{title}</h3>
      {children && <div className="muted">{children}</div>}
    </div>
  );
}
