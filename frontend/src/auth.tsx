import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { hasSessionHint, login as apiLogin, logout as apiLogout, onSession, refreshSession, type Me } from "./api/client";

type AuthState = {
  me: Me | null;
  ready: boolean; // the initial refresh attempt has finished
  can: (perm: string) => boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [ready, setReady] = useState(false);
  const qc = useQueryClient();

  useEffect(() => {
    const off = onSession((s) => {
      setMe(s?.user ?? null);
      if (!s) qc.clear();
    });
    // A page reload keeps the HttpOnly refresh cookie: restore the session.
    if (hasSessionHint()) refreshSession().finally(() => setReady(true));
    else setReady(true);
    return () => {
      off();
    };
  }, [qc]);

  const value: AuthState = {
    me,
    ready,
    can: (perm) => !!me?.permissions.includes(perm),
    login: async (email, password) => {
      await apiLogin(email, password);
    },
    logout: async () => {
      await apiLogout();
    },
  };
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}
