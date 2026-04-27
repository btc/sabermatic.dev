const ERROR_MESSAGES: Record<string, string> = {
  oauth_failed: "Sign-in didn't complete. Please try again.",
  internal: "Something went wrong on our end. Please try again.",
};

interface OAuthErrorProps {
  code: string | null;
}

export function OAuthError({ code }: OAuthErrorProps) {
  if (!code) return null;
  const message = ERROR_MESSAGES[code] ?? ERROR_MESSAGES.oauth_failed;
  return (
    <p role="alert" className="text-sm text-orange-500">
      {message}
    </p>
  );
}
