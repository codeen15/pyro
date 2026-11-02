import { useEffect, useState } from "react";
import { App as AntApp, ConfigProvider, theme as antTheme, type ThemeConfig } from "antd";
import { applyTheme, useApp } from "./store";

// Ant Design tokens for Pyro. The brand red comes from the logo; everything
// else is stock Ant so the components look and behave as documented.
const shared: ThemeConfig["token"] = {
  colorPrimary: "#cf1920",
  colorLink: "#cf1920",
  colorInfo: "#1677ff",
  borderRadius: 6,
  borderRadiusLG: 8,
  fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif',
  fontFamilyCode: '"JetBrains Mono", ui-monospace, "SF Mono", Menlo, monospace',
};

const components: ThemeConfig["components"] = {
  Tabs: { horizontalItemGutter: 28, horizontalMargin: "0" },
  Layout: { headerHeight: 56, headerPadding: "0 16px" },
};

/** Resolves the theme preference ("system" follows the OS live). */
function useDark() {
  const pref = useApp((s) => s.theme);
  const [systemLight, setSystemLight] = useState(() => window.matchMedia("(prefers-color-scheme: light)").matches);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const on = () => {
      setSystemLight(mq.matches);
      if (useApp.getState().theme === "system") applyTheme("system");
    };
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return pref === "dark" || (pref === "system" && !systemLight);
}

export function ThemeRoot({ children }: { children: React.ReactNode }) {
  const dark = useDark();
  return (
    <ConfigProvider
      theme={{
        algorithm: dark ? antTheme.darkAlgorithm : antTheme.defaultAlgorithm,
        token: { ...shared, ...(dark ? { colorPrimary: "#d32029", colorLink: "#e84749", colorBgLayout: "#0f0f0f" } : { colorBgLayout: "#f5f5f5" }) },
        components,
      }}
    >
      <AntApp className="ant-root">{children}</AntApp>
    </ConfigProvider>
  );
}
