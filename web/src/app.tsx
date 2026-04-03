import { Routes, Route, Navigate } from "react-router-dom";
import { AppLayout } from "@/layouts/app-layout";
import { ImmersiveLayout } from "@/layouts/immersive-layout";
import { lazy, Suspense } from "react";

const Login = lazy(() => import("@/pages/auth/login"));
const Signup = lazy(() => import("@/pages/auth/signup"));
const ForgotPassword = lazy(() => import("@/pages/auth/forgot-password"));
const ResetPassword = lazy(() => import("@/pages/auth/reset-password"));
const VerifyEmail = lazy(() => import("@/pages/auth/verify-email"));
const Home = lazy(() => import("@/pages/home"));
const SessionConfig = lazy(() => import("@/pages/session-config"));
const Interview = lazy(() => import("@/pages/interview"));
const SessionLayout = lazy(() => import("@/pages/session/layout"));
const Overview = lazy(() => import("@/pages/session/overview"));
const TranscriptPage = lazy(() => import("@/pages/session/transcript"));
const DeepDive = lazy(() => import("@/pages/session/deep-dive"));
const History = lazy(() => import("@/pages/history"));
const Settings = lazy(() => import("@/pages/settings"));

function Loading() {
  return <div className="flex h-screen items-center justify-center text-muted-foreground">Loading...</div>;
}

export function App() {
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        {/* Auth — standalone layout */}
        <Route path="/login" element={<Login />} />
        <Route path="/signup" element={<Signup />} />
        <Route path="/forgot-password" element={<ForgotPassword />} />
        <Route path="/reset-password" element={<ResetPassword />} />
        <Route path="/verify-email" element={<VerifyEmail />} />

        {/* App — top bar layout */}
        <Route element={<AppLayout />}>
          <Route path="/" element={<Home />} />
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
      </Routes>
    </Suspense>
  );
}
