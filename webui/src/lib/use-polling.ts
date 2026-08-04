import { useEffect, useRef } from "react";

/**
 * Runs fn now and on an interval, but only while the tab is visible — an
 * unattended background tab polling the agent forever is rude, and every
 * operational view needs the same behaviour.
 */
export const usePolling = (fn: () => void, ms: number) => {
  const saved = useRef(fn);
  saved.current = fn;

  useEffect(() => {
    const tick = () => {
      if (document.visibilityState === "visible") saved.current();
    };
    tick();
    const timer = setInterval(tick, ms);
    document.addEventListener("visibilitychange", tick);
    return () => {
      clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [ms]);
};
