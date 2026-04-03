import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { AuthLayout } from "./auth-layout";
import { useLogin } from "@/api/queries";
import { ApiError } from "@/api/client";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";

export default function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const login = useLogin();

  const redirect = searchParams.get("redirect") ?? "/";

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    login.mutate(
      { email, password },
      {
        onSuccess: () => navigate(redirect, { replace: true }),
        onError: (err) => {
          if (err instanceof ApiError && err.status === 401) {
            setError("Invalid email or password.");
          } else {
            setError("Something went wrong. Please try again.");
          }
        },
      },
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
            <div className="space-y-1.5">
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            {error && (
              <p className="text-sm text-orange-500">{error}</p>
            )}
            <Button type="submit" className="w-full" disabled={login.isPending}>
              {login.isPending ? "Signing in…" : "Sign in"}
            </Button>

            <div className="relative flex items-center gap-2">
              <Separator className="flex-1" />
              <span className="text-xs text-muted-foreground">or</span>
              <Separator className="flex-1" />
            </div>

            <a
              href="/api/auth/oauth/google"
              className={buttonVariants({ variant: "outline", className: "w-full" })}
            >
              Sign in with Google
            </a>
            <a
              href="/api/auth/oauth/github"
              className={buttonVariants({ variant: "outline", className: "w-full" })}
            >
              Sign in with GitHub
            </a>
          </CardContent>
          <CardFooter className="flex flex-col gap-2 text-sm text-muted-foreground">
            <Link to="/signup" className="hover:text-foreground">
              Don't have an account? Sign up
            </Link>
            <Link to="/forgot-password" className="hover:text-foreground">
              Forgot password?
            </Link>
          </CardFooter>
        </form>
      </Card>
    </AuthLayout>
  );
}
