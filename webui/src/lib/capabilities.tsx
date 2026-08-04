import { createContext, use, useEffect, useState, type FC, type ReactNode } from "react";
import { fetchCapabilities, MOCK_CAPABILITIES, type Capabilities } from "@/lib/api";

const CapabilitiesContext = createContext<Capabilities>(MOCK_CAPABILITIES);

export const CapabilitiesProvider: FC<{ children: ReactNode }> = ({ children }) => {
  const [capabilities, setCapabilities] = useState<Capabilities>(MOCK_CAPABILITIES);

  useEffect(() => {
    let stale = false;
    void fetchCapabilities().then((caps) => {
      if (!stale) setCapabilities(caps);
    });
    return () => {
      stale = true;
    };
  }, []);

  return <CapabilitiesContext value={capabilities}>{children}</CapabilitiesContext>;
};

export const useCapabilities = () => use(CapabilitiesContext);
