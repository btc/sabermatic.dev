import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router-dom";

import { PublicHeader } from "@/components/public-header";
import { useOptionalAuth } from "@/hooks/use-auth";
import { AppLayout } from "@/layouts/app-layout";
import { ImmersiveLayout } from "@/layouts/immersive-layout";

const Login = lazy(() => import("@/pages/auth/login"));
const Signup = lazy(() => import("@/pages/auth/signup"));
const ForgotPassword = lazy(() => import("@/pages/auth/forgot-password"));
const ResetPassword = lazy(() => import("@/pages/auth/reset-password"));
const VerifyEmail = lazy(() => import("@/pages/auth/verify-email"));
const Home = lazy(() => import("@/pages/home"));
const Landing = lazy(() => import("@/pages/landing"));
const SampleSession = lazy(() => import("@/pages/sample"));
const SessionConfig = lazy(() => import("@/pages/session-config"));
const Interview = lazy(() => import("@/pages/interview"));
const SessionLayout = lazy(() => import("@/pages/session/layout"));
const Overview = lazy(() => import("@/pages/session/overview"));
const TranscriptPage = lazy(() => import("@/pages/session/transcript"));
const DeepDive = lazy(() => import("@/pages/session/deep-dive"));
const History = lazy(() => import("@/pages/history"));
const Settings = lazy(() => import("@/pages/settings"));
const NotFound = lazy(() => import("@/pages/not-found"));

function Loading() {
  return <div className="flex h-screen items-center justify-center text-muted-foreground">Loading...</div>;
}

function ConditionalHome() {
  const { isAuthenticated, isLoading, isAuthError } = useOptionalAuth();
  if (isLoading) return <Loading />;
  if (isAuthenticated) {
    return (
      <AppLayout>
        <Home />
      </AppLayout>
    );
  }
  if (isAuthError) {
    return <><PublicHeader /><Landing /></>;
  }
  return (
    <div className="flex min-h-screen items-center justify-center">
      <p className="text-sm text-muted-foreground">Something went wrong. Please try again later.</p>
    </div>
  );
}

export function App() {
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        {/* Public — no layout */}
        <Route path="/login" element={<Login />} />
        <Route path="/signup" element={<Signup />} />
        <Route path="/forgot-password" element={<ForgotPassword />} />
        <Route path="/reset-password" element={<ResetPassword />} />
        <Route path="/verify-email" element={<VerifyEmail />} />
        <Route path="/about" element={<><PublicHeader /><Landing /></>} />
        <Route path="/sample" element={<SampleSession />}>
          <Route index element={<Overview />} />
          <Route path="transcript" element={<TranscriptPage />} />
          <Route path="deep-dive" element={<DeepDive />} />
        </Route>

        {/* Root — conditional: landing (unauth) or app layout (auth) */}
        <Route path="/" element={<ConditionalHome />} />

        {/* App — top bar layout (all require auth) */}
        <Route element={<AppLayout />}>
          <Route path="/sessions/new" element={<SessionConfig />} />
          <Route path="/sessions/:id" element={<SessionLayout />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<Overview />} />
            <Route path="transcript" element={<TranscriptPage />} />
            <Route path="deep-dive" element={<DeepDive />} />
          </Route>
          <Route path="/history" element={<History />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/settings/billing" element={<Settings />} />
        </Route>

        {/* Interview — immersive layout */}
        <Route element={<ImmersiveLayout />}>
          <Route path="/sessions/:id/interview" element={<Interview />} />
        </Route>

        {/* Catch-all — 404 */}
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Suspense>
  );
}
