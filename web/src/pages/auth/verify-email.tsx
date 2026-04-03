import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AuthLayout } from "./auth-layout";
import { useVerifyEmail } from "@/api/queries";
import { Card, CardContent, CardFooter } from "@/components/ui/card";

export default function VerifyEmail() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get("token") ?? "";
  const verifyEmail = useVerifyEmail();

  const [status, setStatus] = useState<"pending" | "success" | "error">(
    token ? "pending" : "error",
  );
  // Guard against React StrictMode double-invoke (mounts component twice in dev).
  const calledRef = useRef(false);

  useEffect(() => {
    if (!token || calledRef.current) return;
    calledRef.current = true;
    verifyEmail.mutate(
      { token },
      {
        onSuccess: () => setStatus("success"),
        onError: () => setStatus("error"),
      },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only run once on mount
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
