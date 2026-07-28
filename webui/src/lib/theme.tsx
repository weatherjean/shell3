import {
  createContext,
  use,
  useCallback,
  useEffect,
  useState,
  type FC,
  type ReactNode,
} from "react";

export type Theme = "light" | "dark" | "system";

const STORAGE_KEY = "shell3.theme";

const prefersDark = () =>
  typeof window !== "undefined" &&
  window.matchMedia("(prefers-color-scheme: dark)").matches;

const resolve = (theme: Theme): "light" | "dark" =>
  theme === "system" ? (prefersDark() ? "dark" : "light") : theme;

const apply = (theme: Theme) => {
  document.documentElement.classList.toggle("dark", resolve(theme) === "dark");
};

type ThemeContextValue = {
  theme: Theme;
  resolved: "light" | "dark";
  setTheme: (theme: Theme) => void;
  toggle: () => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

const readStored = (): Theme => {
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === "light" || stored === "dark" || stored === "system"
    ? stored
    : "system";
};

export const ThemeProvider: FC<{ children: ReactNode }> = ({ children }) => {
  const [theme, setThemeState] = useState<Theme>(readStored);
  const [resolved, setResolved] = useState<"light" | "dark">(() =>
    resolve(readStored()),
  );

  useEffect(() => {
    apply(theme);
    setResolved(resolve(theme));
    localStorage.setItem(STORAGE_KEY, theme);
  }, [theme]);

  // Follow the OS while the preference is "system".
  useEffect(() => {
    if (theme !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => {
      apply("system");
      setResolved(resolve("system"));
    };
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [theme]);

  const setTheme = useCallback((next: Theme) => setThemeState(next), []);
  const toggle = useCallback(
    () => setThemeState(resolve(readStored()) === "dark" ? "light" : "dark"),
    [],
  );

  return (
    <ThemeContext value={{ theme, resolved, setTheme, toggle }}>
      {children}
    </ThemeContext>
  );
};

export const useTheme = (): ThemeContextValue => {
  const ctx = use(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used inside a ThemeProvider");
  return ctx;
};

/**
 * Applies the stored theme before React mounts so a dark-mode reload never
 * flashes a light screen. Called from main.tsx, not a component.
 */
export const initTheme = () => apply(readStored());
