import { useEffect, useState } from "preact/hooks";

export type Route = {
  path: string;
  segments: string[];
  query: URLSearchParams;
  search: string;
};

export const ROUTE_EVENT = "rookery:route-changed";

/**
 * currentRoute reads the browser's pathname, query string, and trailing-slash-trimmed path into a
 * Route, falling back to "/" when the path is empty.
 */
export function currentRoute(): Route {
  const path = window.location.pathname.replace(/\/+$/, "") || "/";
  return {
    path,
    segments: path.split("/").filter(Boolean),
    query: new URLSearchParams(window.location.search),
    search: window.location.search,
  };
}

/**
 * navigate applies a same-origin path to the browser history and dispatches ROUTE_EVENT so route
 * consumers re-render; it returns without side effects when the target equals the current URL.
 */
export function navigate(path: string, replace = false): void {
  const target = path.startsWith("/") ? path : `/${path}`;
  const current = `${window.location.pathname}${window.location.search}`;
  if (current === target) {
    return;
  }
  if (replace) {
    window.history.replaceState(null, "", target);
  } else {
    window.history.pushState(null, "", target);
  }
  window.dispatchEvent(new CustomEvent(ROUTE_EVENT));
}

/**
 * useRoute returns the current Route and re-renders the caller whenever the browser emits popstate
 * or another component dispatches ROUTE_EVENT, removing both listeners on unmount.
 */
export function useRoute(): Route {
  const [route, setRoute] = useState(currentRoute);
  useEffect(() => {
    const update = () => setRoute(currentRoute());
    window.addEventListener("popstate", update);
    window.addEventListener(ROUTE_EVENT, update);
    return () => {
      window.removeEventListener("popstate", update);
      window.removeEventListener(ROUTE_EVENT, update);
    };
  }, []);
  return route;
}
