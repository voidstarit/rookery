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

export function showToast(message: string, isError = false): void {
  const toast = { id: nextID++, message, error: isError };
  if (listener) {
    listener(toast);
  } else {
    pending.push(toast);
  }
}

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

export function mountToastHost(root: Element): void {
  render(<ToastHost />, root);
}
