import { render } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";

type ToastData = {
  id: number;
  message: string;
  error: boolean;
};

type ToastListener = (toast: ToastData) => void;

let listener: ToastListener | null = null;
let pending: ToastData[] = [];
let nextID = 1;

/**
 * showToast queues a transient message for the mounted ToastHost, buffering it when no host is
 * listening so calls made before startup are not lost.
 */
export function showToast(message: string, isError = false): void {
  const toast = { id: nextID++, message, error: isError };
  if (listener) {
    listener(toast);
  } else {
    pending.push(toast);
  }
}

/**
 * ToastHost renders the single active toast for 3.5 seconds, replacing any current one and
 * flushing messages queued before it mounted.
 */
function ToastHost() {
  const [toast, setToast] = useState<ToastData | null>(null);
  const timer = useRef<number | null>(null);

  useEffect(() => {
    const show = (next: ToastData) => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current);
      }
      setToast(next);
      timer.current = window.setTimeout(() => {
        setToast(null);
        timer.current = null;
      }, 3500);
    };

    listener = show;
    if (pending.length > 0) {
      const queued = pending;
      pending = [];
      for (const item of queued) {
        show(item);
      }
    }

    return () => {
      listener = null;
      if (timer.current !== null) {
        window.clearTimeout(timer.current);
        timer.current = null;
      }
    };
  }, []);

  if (!toast) {
    return null;
  }

  return (
    <div class={toast.error ? "toast error" : "toast"} role="status" aria-live="polite">
      {toast.message}
    </div>
  );
}

/**
 * mountToastHost renders the toast host into the given element, usually once at app startup.
 */
export function mountToastHost(root: Element): void {
  render(<ToastHost />, root);
}
