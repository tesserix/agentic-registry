import { createContext, useContext, useEffect, useState } from "react";
import { api, type Session } from "./api";

const anonymousSession: Session = {
  authenticated: false,
  email: "",
  tenant_id: "",
  onboarding_required: false,
  admin: false,
};
const RegistrySessionContext = createContext<Session>(anonymousSession);

export function RegistrySessionProvider({
  children,
  initialSession,
}: {
  children: React.ReactNode;
  initialSession?: Session;
}) {
  const [session, setSession] = useState(initialSession ?? anonymousSession);

  useEffect(() => {
    if (initialSession) return;
    let cancelled = false;
    api.session()
      .then((value) => {
        if (!cancelled) setSession(value);
      })
      .catch(() => {
        if (!cancelled) setSession(anonymousSession);
      });
    return () => {
      cancelled = true;
    };
  }, [initialSession]);

  return <RegistrySessionContext.Provider value={session}>{children}</RegistrySessionContext.Provider>;
}

export function useRegistrySession(): Session {
  return useContext(RegistrySessionContext);
}

export function AdminOnly({ children }: { children: React.ReactNode }) {
  return useRegistrySession().admin ? children : null;
}
