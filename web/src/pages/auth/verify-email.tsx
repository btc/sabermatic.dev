import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AuthLayout } from "./auth-layout";
import { apiClient } from "@/api/client";
import { Card, CardContent, CardFooter } from "@/components/ui/card";

export default function VerifyEmail() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get("token") ?? "";

  const [status, setStatus] = useState<"pending" | "success" | "error">("pending");

  useEffect(() => {
    if (!token) {
      setStatus("error");
      return;
    }
    apiClient
      .post("/api/auth/verify-email", { token })
      .then(() => setStatus("success"))
      .catch(() => setStatus("error"));
  }, [token]);

  return (
    <AuthLayout>
      <Card>
        <CardContent className="pt-6">
          {status === "pending" && (
            <p className="text-sm text-muted-foreground text-center">Verifying…</p>
          )}
          {status === "success" && (
            <p className="text-sm text-muted-foreground text-center">
              Your email has been verified.
            </p>
          )}
          {status === "error" && (
            <p className="text-sm text-orange-500 text-center">
              The verification link is invalid or has expired.
            </p>
          )}
        </CardContent>
        {status !== "pending" && (
          <CardFooter className="text-sm text-muted-foreground">
            <Link to="/login" className="hover:text-foreground">
              Sign in
            </Link>
          </CardFooter>
        )}
      </Card>
    </AuthLayout>
  );
}
