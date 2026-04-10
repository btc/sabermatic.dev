import { useState } from "react";
import { Link } from "react-router-dom";

import { useForgotPassword } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { AuthLayout } from "./auth-layout";

export default function ForgotPassword() {
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);
  const forgotPassword = useForgotPassword();

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    forgotPassword.mutate(
      { email },
      {
        onSuccess: () => setSent(true),
        onError: () => setError("Something went wrong. Please try again."),
      },
    );
  }

  const isPending = forgotPassword.isPending;

  if (sent) {
    return (
      <AuthLayout>
        <Card>
          <CardContent className="pt-6">
            <p className="text-sm text-muted-foreground text-center">
              Check your email for a reset link.
            </p>
          </CardContent>
          <CardFooter className="text-sm text-muted-foreground">
            <Link to="/login" className="hover:text-foreground">
              Back to sign in
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
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            {error && (
              <p className="text-sm text-orange-500">{error}</p>
            )}
            <Button type="submit" className="w-full" disabled={isPending}>
              {isPending ? "Sending…" : "Send reset link"}
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
