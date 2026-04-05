import { useState, useRef, KeyboardEvent } from "react";
import { useLocation, useNavigate, Link } from "react-router-dom";
import { toast } from "sonner";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { getMe, getUsage, updateProfile, exportData } from "@/pb/drill/v1/user-UserService_connectquery";
import { UserPlan } from "@/pb/drill/v1/user_pb";
import {
  useLogout, useDeleteAccount,
  useCheckout, usePortal, type CheckoutRequest,
} from "@/api/queries";
import { ApiError } from "@/api/client";
import { ConnectError } from "@connectrpc/connect";
import { useTheme } from "@/hooks/use-theme";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Section wrapper
// ---------------------------------------------------------------------------

interface SectionProps {
  title: string;
  description?: string;
  children: React.ReactNode;
}

function Section({ title, description, children }: SectionProps) {
  return (
    <section className="space-y-3">
      <div>
        <h2 className="text-sm font-medium text-foreground">{title}</h2>
        {description && (
          <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
        )}
      </div>
      {children}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Nav tabs
// ---------------------------------------------------------------------------

interface NavTabsProps {
  isBilling: boolean;
}

function NavTabs({ isBilling }: NavTabsProps) {
  return (
    <div className="flex items-center rounded-lg border border-border bg-muted/40 p-0.5 gap-0.5 w-fit">
      <Link
        to="/settings"
        className={cn(
          "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
          !isBilling
            ? "bg-background text-foreground shadow-sm"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        Account
      </Link>
      <Link
        to="/settings/billing"
        className={cn(
          "rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
          isBilling
            ? "bg-background text-foreground shadow-sm"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        Billing
      </Link>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Theme toggle
// ---------------------------------------------------------------------------

type Theme = "light" | "dark" | "system";
const THEME_OPTIONS: { value: Theme; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];

function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  return (
    <div className="flex items-center rounded-lg border border-border bg-muted/40 p-0.5 gap-0.5 w-fit">
      {THEME_OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          onClick={() => setTheme(opt.value)}
          className={cn(
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors cursor-pointer",
            theme === opt.value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Profile section
// ---------------------------------------------------------------------------

function ProfileSection() {
  const { data: meData } = useQuery(getMe, {});
  const user = meData?.user;
  // M-8: Track local edits separately. When localEdit is null, display the server value.
  const [localEdit, setLocalEdit] = useState<string | null>(null);
  const displayName = localEdit ?? user?.displayName ?? "";
  const [saveError, setSaveError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const updateProfileMut = useMutation(updateProfile);

  function handleChange(value: string) {
    setLocalEdit(value);
  }

  function saveName() {
    if (!displayName.trim() || displayName === user?.displayName) {
      setLocalEdit(null);
      return;
    }
    setSaveError(null);
    updateProfileMut.mutate(
      { user: { displayName: displayName.trim() }, updateMask: { paths: ["display_name"] } },
      {
        onSuccess: () => {
          setLocalEdit(null);
          toast.success("Profile saved");
        },
        onError: (err) => {
          const msg = err instanceof ConnectError ? `Save failed (${err.code})` : "Save failed";
          setSaveError(msg);
          toast.error("Save failed");
          setLocalEdit(null);
        },
      },
    );
  }

  const saving = updateProfileMut.isPending;

  function handleKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter") {
      e.currentTarget.blur();
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-1.5">
          <label className="block text-xs font-medium text-muted-foreground" htmlFor="display-name">
            Display name
          </label>
          <Input
            id="display-name"
            ref={inputRef}
            value={displayName}
            onChange={(e) => handleChange(e.target.value)}
            onBlur={saveName}
            onKeyDown={handleKeyDown}
            disabled={saving}
            className="max-w-sm"
            placeholder="Your name"
          />
          {saveError && <p className="text-xs text-orange-500">{saveError}</p>}
        </div>

        <div className="space-y-1.5">
          <label className="text-xs font-medium text-muted-foreground">
            Email
          </label>
          <div className="flex items-center gap-2">
            <span className="text-sm text-foreground">{user?.email}</span>
            {user?.emailVerified ? (
              <Badge variant="secondary" className="text-xs">Verified</Badge>
            ) : (
              <Badge variant="outline" className="text-xs text-muted-foreground">Unverified</Badge>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Linked accounts section
// ---------------------------------------------------------------------------

function LinkedAccountsSection() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Linked accounts</CardTitle>
        <CardDescription>Connect third-party accounts for faster sign-in.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">Google</span>
          </div>
          <a href="/api/auth/oauth/google" className={cn(
            "inline-flex shrink-0 items-center justify-center rounded-lg border border-border bg-background px-2.5 h-8 text-sm font-medium transition-colors hover:bg-muted",
          )}>
            Connect
          </a>
        </div>

        <Separator />

        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">GitHub</span>
          </div>
          <a href="/api/auth/oauth/github" className={cn(
            "inline-flex shrink-0 items-center justify-center rounded-lg border border-border bg-background px-2.5 h-8 text-sm font-medium transition-colors hover:bg-muted",
          )}>
            Connect
          </a>
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Preferences section
// ---------------------------------------------------------------------------

function PreferencesSection() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Preferences</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-medium">Theme</p>
            <p className="text-xs text-muted-foreground">Choose how the interface appears.</p>
          </div>
          <ThemeToggle />
        </div>
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Data export section
// ---------------------------------------------------------------------------

function DataExportSection() {
  const exportDataMut = useMutation(exportData);
  const status = exportDataMut.isIdle ? "idle" : exportDataMut.isPending ? "loading" : exportDataMut.isSuccess ? "done" : "error";
  const errorMsg = exportDataMut.error instanceof ConnectError
    ? `Export request failed (${exportDataMut.error.code})`
    : exportDataMut.isError
      ? "Export request failed. Please try again."
      : null;

  function handleExport() {
    exportDataMut.mutate({}, {
      onSuccess: () => toast.success("Export requested — check your email"),
    });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Data export</CardTitle>
        <CardDescription>Download a copy of all your sessions, transcripts, and evaluations.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {status === "done" ? (
          <p className="text-sm text-muted-foreground">
            We'll email you a download link when your export is ready.
          </p>
        ) : (
          <>
            <Button
              variant="outline"
              onClick={handleExport}
              disabled={status === "loading"}
            >
              {status === "loading" ? "Requesting..." : "Export my data"}
            </Button>
            {status === "error" && errorMsg && (
              <p className="text-xs text-orange-500">{errorMsg}</p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Account deletion section
// ---------------------------------------------------------------------------

function AccountDeletionSection() {
  const [open, setOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const deleteAccount = useDeleteAccount();
  const logout = useLogout();
  const navigate = useNavigate();

  function handleDelete() {
    setDeleteError(null);
    deleteAccount.mutate(undefined, {
      onSuccess: () => {
        logout.mutate(undefined, {
          onSuccess: () => navigate("/login"),
          onError: () => navigate("/login"),
        });
      },
      onError: (err) => {
        const msg = err instanceof ApiError
          ? `Deletion failed (${err.status})`
          : "Deletion failed. Please try again.";
        setDeleteError(msg);
        toast.error(msg);
      },
    });
  }

  const deleting = deleteAccount.isPending;

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Danger zone</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Deleting your account begins a 30-day grace period. Your data remains
            recoverable during that window by contacting support.
          </p>
          <Button
            variant="outline"
            className="text-destructive border-destructive/30 hover:bg-destructive/10"
            onClick={() => setOpen(true)}
          >
            Delete account
          </Button>
        </CardContent>
      </Card>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent showCloseButton={!deleting}>
          <DialogHeader>
            <DialogTitle>Delete account?</DialogTitle>
            <DialogDescription>
              This starts a 30-day grace period. During that time, your sessions,
              transcripts, and evaluations are preserved and recoverable. After 30 days,
              all data is permanently deleted and cannot be undone.
            </DialogDescription>
          </DialogHeader>

          {deleteError && (
            <p className="text-xs text-orange-500 px-0.5">{deleteError}</p>
          )}

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setOpen(false)}
              disabled={deleting}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={deleting}
            >
              {deleting ? "Deleting..." : "Yes, delete my account"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

// ---------------------------------------------------------------------------
// Account settings page
// ---------------------------------------------------------------------------

function AccountSettings() {
  return (
    <div className="space-y-6">
      <ProfileSection />
      <LinkedAccountsSection />
      <PreferencesSection />
      <DataExportSection />
      <AccountDeletionSection />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Minute balance display
// ---------------------------------------------------------------------------

interface MinuteBalanceProps {
  total: number;
  free: number;
  paid: number;
}

function MinuteBalance({ total, free, paid }: MinuteBalanceProps) {
  const isCritical = total < 10;
  const isWarning = total < 30;

  return (
    <div className="space-y-2">
      <div className="flex items-baseline gap-1.5">
        <span
          className={cn(
            "text-2xl font-semibold tabular-nums",
            isCritical ? "text-destructive" : isWarning ? "text-amber-500" : "text-foreground",
          )}
        >
          {total}
        </span>
        <span className="text-sm text-muted-foreground">minutes available</span>
      </div>
      {(free > 0 || paid > 0) && (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {free > 0 && <span>{free} free</span>}
          {free > 0 && paid > 0 && <span className="text-border">·</span>}
          {paid > 0 && <span>{paid} purchased</span>}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Free plan limits table
// ---------------------------------------------------------------------------

const FREE_LIMITS = [
  { feature: "Practice sessions", limit: "60 min free grant" },
  { feature: "Session duration", limit: "Up to 30 min" },
  { feature: "Evaluation & scoring", limit: "Included" },
  { feature: "Educator analysis", limit: "1 full, then preview" },
  { feature: "Coach analysis", limit: "3 sessions minimum" },
  { feature: "Audio playback", limit: "Included" },
];

function FreeLimitsTable() {
  return (
    <div className="rounded-lg border border-border overflow-hidden">
      {FREE_LIMITS.map((row, i) => (
        <div
          key={row.feature}
          className={cn(
            "flex items-center justify-between px-4 py-2.5 text-sm",
            i !== 0 && "border-t border-border",
          )}
        >
          <span className="text-muted-foreground">{row.feature}</span>
          <span className="font-medium">{row.limit}</span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Minute packs
// ---------------------------------------------------------------------------

const MINUTE_PACKS: { minutes: number; label: string }[] = [
  { minutes: 120, label: "120 min" },
  { minutes: 300, label: "300 min" },
  { minutes: 600, label: "600 min" },
];

// ---------------------------------------------------------------------------
// Billing page
// ---------------------------------------------------------------------------

function BillingSettings() {
  const { data: meData } = useQuery(getMe, {});
  const user = meData?.user;
  const { data: usage } = useQuery(getUsage, {});
  const checkout = useCheckout();
  const portal = usePortal();

  const isPro = user?.plan === UserPlan.PRO;

  // Track which pack (if any) is being purchased, so we can show per-button loading.
  const [pendingPack, setPendingPack] = useState<number | null>(null);

  const checkoutLoading = checkout.isPending;
  const checkoutError = checkout.isError
    ? checkout.error instanceof ApiError
      ? `Could not start checkout (${checkout.error.status}). Please try again.`
      : "Could not start checkout. Please try again."
    : null;

  const portalLoading = portal.isPending;
  const portalError = portal.isError
    ? portal.error instanceof ApiError
      ? `Could not open billing portal (${portal.error.status}). Please try again.`
      : "Could not open billing portal. Please try again."
    : null;

  function handleCheckout(body: CheckoutRequest) {
    if (body.type === "pack") {
      setPendingPack(body.minutes);
    }
    checkout.mutate(body, {
      onSuccess: (res) => {
        setPendingPack(null);
        if (res?.url) {
          window.location.href = res.url;
        }
      },
      onError: () => setPendingPack(null),
    });
  }

  function handlePortal() {
    portal.mutate(undefined, {
      onSuccess: (res) => {
        if (res?.url) {
          window.open(res.url, "_blank", "noopener,noreferrer");
        }
      },
    });
  }

  return (
    <div className="space-y-6">
      {/* Current plan + balance */}
      <Section title="Current plan">
        <Card>
          <CardContent className="space-y-4">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium capitalize">
                {isPro ? "Pro" : "Free"} plan
              </span>
              {isPro && (
                <Badge variant="default" className="text-xs">Pro</Badge>
              )}
            </div>

            {usage != null && (
              <MinuteBalance
                total={usage.totalBalance}
                free={usage.freeBalance}
                paid={usage.paidBalance}
              />
            )}
          </CardContent>
        </Card>
      </Section>

      {/* Minute packs — available to all users */}
      <Section
        title="Buy minutes"
        description="Top up your balance any time. Minutes never expire while your account is active."
      >
        <Card>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap gap-2">
              {MINUTE_PACKS.map(({ minutes, label }) => {
                const isLoading = checkoutLoading && pendingPack === minutes;
                return (
                  <Button
                    key={minutes}
                    variant="outline"
                    onClick={() => handleCheckout({ type: "pack", minutes })}
                    disabled={checkoutLoading}
                    className="min-w-[96px]"
                  >
                    {isLoading ? "Redirecting..." : label}
                  </Button>
                );
              })}
            </div>
            {checkoutError && pendingPack !== null && (
              <p className="text-xs text-orange-500">{checkoutError}</p>
            )}
          </CardContent>
        </Card>
      </Section>

      {/* Free tier info + upgrade */}
      {!isPro && (
        <Section
          title="Upgrade to Pro"
          description="Get a monthly minute refill and priority support."
        >
          <Card>
            <CardContent className="space-y-4">
              <FreeLimitsTable />

              <div className="space-y-2">
                <Button
                  onClick={() => handleCheckout({ type: "subscription", plan: "pro" })}
                  disabled={checkoutLoading}
                  className="w-full sm:w-auto"
                >
                  {checkoutLoading && pendingPack === null ? "Redirecting..." : "Upgrade to Pro"}
                </Button>
                {checkoutError && pendingPack === null && (
                  <p className="text-xs text-orange-500">{checkoutError}</p>
                )}
                <p className="text-xs text-muted-foreground">
                  You're in control of your plan — cancel any time from your billing portal.
                </p>
              </div>
            </CardContent>
          </Card>
        </Section>
      )}

      {/* Pro subscription management */}
      {isPro && (
        <Section title="Subscription">
          <Card>
            <CardContent className="space-y-3">
              <p className="text-sm text-muted-foreground">
                Manage your payment method, invoices, and cancellation from the Stripe billing portal.
              </p>
              <div className="space-y-2">
                <Button
                  variant="outline"
                  onClick={handlePortal}
                  disabled={portalLoading}
                >
                  {portalLoading ? "Opening..." : "Manage subscription"}
                </Button>
                {portalError && (
                  <p className="text-xs text-orange-500">{portalError}</p>
                )}
              </div>
            </CardContent>
          </Card>
        </Section>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Settings root
// ---------------------------------------------------------------------------

export default function Settings() {
  const location = useLocation();
  const isBilling = location.pathname === "/settings/billing";

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-base font-medium">Settings</h1>
      </div>

      <NavTabs isBilling={isBilling} />

      {isBilling ? <BillingSettings /> : <AccountSettings />}
    </div>
  );
}
