import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { useResetPassword } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { AuthLayout } from "./auth-layout";

export default function ResetPassword() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get("token") ?? "";

  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const resetPassword = useResetPassword();

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (password !== confirm) {
      setError("Passwords don't match.");
      return;
    }

    if (!token) {
      setError("Missing reset token. Please request a new reset link.");
      return;
    }

    resetPassword.mutate(
      { token, password },
      {
        onSuccess: () => setDone(true),
        onError: () => setError("The reset link may have expired. Please request a new one."),
      },
    );
  }

  const isPending = resetPassword.isPending;

  if (done) {
    return (
      <AuthLayout>
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-muted-foreground text-center">
              Your password has been reset.
            </p>
          </CardContent>
          <CardFooter className="text-sm text-muted-foreground">
            <Link to="/login" className="hover:text-foreground">
              Sign in
            </Link>
          </CardFooter>
        </Card>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <Card>
        <form onSubmit={handleSubmit}>
          <CardContent className="pt-6 space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="password">New password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm">Confirm password</Label>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
              />
            </div>
            {error && (
              <p className="text-sm text-orange-500">{error}</p>
            )}
            <Button type="submit" className="w-full" disabled={isPending}>
              {isPending ? "Resetting…" : "Reset password"}
            </Button>
          </CardContent>
          <CardFooter className="text-sm text-muted-foreground">
            <Link to="/login" className="hover:text-foreground">
              Back to sign in
            </Link>
          </CardFooter>
        </form>
      </Card>
    </AuthLayout>
  );
}
