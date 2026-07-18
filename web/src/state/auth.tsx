import {
  createContext,
  type PropsWithChildren,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { ApiError, apiFetch, apiJson } from "../lib/api";
import type { UserOut } from "../lib/types";

interface LoginInput {
  email: string;
  password: string;
}

interface RegisterInput extends LoginInput {
  display_name?: string;
}

interface AuthContextValue {
  user: UserOut | null;
  loading: boolean;
  login: (input: LoginInput) => Promise<UserOut>;
  register: (input: RegisterInput) => Promise<UserOut>;
  logout: () => Promise<void>;
  refreshUser: () => Promise<UserOut | null>;
  updateProfile: (input: Record<string, unknown>) => Promise<UserOut>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: PropsWithChildren) {
  const [user, setUser] = useState<UserOut | null>(null);
  const [loading, setLoading] = useState(true);

  const refreshUser = useCallback(async () => {
    try {
      const nextUser = await apiFetch<UserOut>("/api/auth/me");
      setUser(nextUser);
      return nextUser;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setUser(null);
        return null;
      }
      throw error;
    }
  }, []);

  useEffect(() => {
    let active = true;
    refreshUser()
      .catch(() => {
        if (active) setUser(null);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [refreshUser]);

  const login = useCallback(async (input: LoginInput) => {
    const nextUser = await apiJson<UserOut>("/api/auth/login", "POST", input);
    setUser(nextUser);
    return nextUser;
  }, []);

  const register = useCallback(async (input: RegisterInput) => {
    const nextUser = await apiJson<UserOut>("/api/auth/register", "POST", input);
    setUser(nextUser);
    return nextUser;
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiJson<void>("/api/auth/logout", "POST");
    } finally {
      setUser(null);
    }
  }, []);

  const updateProfile = useCallback(async (input: Record<string, unknown>) => {
    const nextUser = await apiJson<UserOut>("/api/auth/me", "PATCH", input);
    setUser(nextUser);
    return nextUser;
  }, []);

  const value = useMemo<AuthContextValue>(() => ({
    user,
    loading,
    login,
    register,
    logout,
    refreshUser,
    updateProfile,
  }), [loading, login, logout, refreshUser, register, updateProfile, user]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth, AuthProvider içinde kullanılmalıdır.");
  return value;
}
