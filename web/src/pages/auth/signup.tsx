import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { AuthLayout } from "./auth-layout";
import { useSignup } from "@/api/queries";
import { ConnectError, Code } from "@connectrpc/connect";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { GoogleIcon, GitHubIcon } from "@/components/oauth-icons";

export default function Signup() {
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();
  const signup = useSignup();

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    signup.mutate(
      { displayName, email, password },
      {
        onSuccess: () => navigate("/?verified=pending", { replace: true }),
        onError: (err) => {
          if (err instanceof ConnectError && err.code === Code.AlreadyExists) {
            setError("An account with that email already exists.");
          } else if (err instanceof ConnectError && err.code === Code.InvalidArgument) {
            setError("Please check your information and try again.");
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
          <CardContent className="pt-6 pb-2 space-y-4">
            <a
              href="/api/auth/oauth/google"
              className={buttonVariants({ variant: "outline", className: "w-full" })}
            >
              <GoogleIcon className="mr-2 h-4 w-4" />
              Sign up with Google
            </a>
            <a
              href="/api/auth/oauth/github"
              className={buttonVariants({ variant: "outline", className: "w-full" })}
            >
              <GitHubIcon className="mr-2 h-4 w-4" />
              Sign up with GitHub
            </a>

            <div className="relative flex items-center gap-2">
              <Separator className="flex-1" />
              <span className="text-xs text-muted-foreground">or</span>
              <Separator className="flex-1" />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="display-name">Name</Label>
              <Input
                id="display-name"
                type="text"
                autoComplete="name"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                required
              />
            </div>
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
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            {error && (
              <p className="text-sm text-orange-500">{error}</p>
            )}
            <Button type="submit" className="w-full" disabled={signup.isPending}>
              {signup.isPending ? "Creating account…" : "Create account"}
            </Button>
          </CardContent>
          <CardFooter className="text-sm text-muted-foreground">
            <Link to="/login" className="hover:text-foreground">
              Already have an account? Sign in
            </Link>
          </CardFooter>
        </form>
      </Card>
    </AuthLayout>
  );
}
