"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { QRCodeSVG } from "qrcode.react";
import {
  AlertTriangle,
  Check,
  Copy,
  Download,
  Loader2,
  ShieldCheck,
  ShieldOff,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { authClient } from "@/lib/auth-client";

const TOTP_RE = /^\d{6}$/;

function describeError(err: unknown): string {
  if (typeof err === "object" && err !== null) {
    const e = err as { message?: string; error?: { message?: string } };
    return e.error?.message ?? e.message ?? "Something went wrong. Try again.";
  }
  return "Something went wrong. Try again.";
}

/** The shared secret from a totp URI, for manual entry when QR scanning fails. */
function secretFromUri(totpURI: string): string | null {
  try {
    return new URL(totpURI).searchParams.get("secret");
  } catch {
    return null;
  }
}

function downloadCodes(codes: string[]) {
  const blob = new Blob(
    [
      "Dropway two-factor backup codes\n",
      "Each code can be used once. Store them somewhere safe.\n\n",
      codes.join("\n"),
      "\n",
    ],
    { type: "text/plain" },
  );
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "dropway-backup-codes.txt";
  a.click();
  URL.revokeObjectURL(url);
}

/** One-time backup code list with copy + download, shown exactly once. */
function BackupCodes({ codes }: { codes: string[] }) {
  const [copied, setCopied] = React.useState(false);

  async function onCopy() {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard can be unavailable (permissions); download still works.
    }
  }

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-2 rounded-md border border-border bg-muted/50 p-4 font-mono text-sm sm:grid-cols-3">
        {codes.map((code) => (
          <span key={code} className="tracking-wide">
            {code}
          </span>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCopy}>
          {copied ? (
            <Check className="size-4" aria-hidden />
          ) : (
            <Copy className="size-4" aria-hidden />
          )}
          {copied ? "Copied" : "Copy"}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => downloadCodes(codes)}
        >
          <Download className="size-4" aria-hidden />
          Download
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        Each code works once. This is the only time they are shown, so store
        them somewhere safe (a password manager is ideal).
      </p>
    </div>
  );
}

type EnrollStep =
  | { step: "idle" }
  | { step: "password" }
  | { step: "scan"; totpURI: string; backupCodes: string[] }
  | { step: "codes"; backupCodes: string[] };

/**
 * The enrollment flow, from "Enable" through scan-verify to the one-time backup
 * code reveal. Also used standalone by the mandatory setup page (org-enforced
 * MFA), where `onEnrolled` navigates into the app instead of refreshing.
 */
export function TwoFactorEnroll({
  requiresPassword,
  onEnrolled,
  autoStart = false,
}: {
  requiresPassword: boolean;
  onEnrolled?: () => void;
  autoStart?: boolean;
}) {
  const [state, setState] = React.useState<EnrollStep>({ step: "idle" });
  const [password, setPassword] = React.useState("");
  const [code, setCode] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const started = React.useRef(false);

  async function begin(pw: string) {
    setPending(true);
    setError(null);
    try {
      const { data, error: err } = await authClient.twoFactor.enable({
        password: pw,
      });
      if (err) throw err;
      if (!data?.totpURI) throw new Error("No TOTP URI returned.");
      setPassword("");
      setState({
        step: "scan",
        totpURI: data.totpURI,
        backupCodes: data.backupCodes ?? [],
      });
    } catch (err) {
      setError(describeError(err));
      if (!requiresPassword) setState({ step: "idle" });
    } finally {
      setPending(false);
    }
  }

  function onStart() {
    setError(null);
    if (requiresPassword) {
      setState({ step: "password" });
      return;
    }
    void begin("");
  }

  // Mandatory-setup mode: skip the introductory button press. Passwordless
  // users go straight to the QR; password users straight to the prompt.
  React.useEffect(() => {
    if (autoStart && !started.current) {
      started.current = true;
      onStart();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoStart]);

  async function onVerify(e: React.FormEvent) {
    e.preventDefault();
    if (state.step !== "scan") return;
    setError(null);
    if (!TOTP_RE.test(code.trim())) {
      setError("Enter the 6-digit code from your authenticator app.");
      return;
    }
    setPending(true);
    try {
      const { error: err } = await authClient.twoFactor.verifyTotp({
        code: code.trim(),
      });
      if (err) throw err;
      setCode("");
      setState({ step: "codes", backupCodes: state.backupCodes });
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  if (state.step === "idle") {
    return (
      <div className="space-y-3">
        <Button type="button" onClick={onStart} disabled={pending}>
          {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
          Enable two-factor authentication
        </Button>
        {error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
      </div>
    );
  }

  if (state.step === "password") {
    return (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!password) {
            setError("Enter your password to continue.");
            return;
          }
          void begin(password);
        }}
        className="max-w-sm space-y-3"
      >
        <div className="space-y-2">
          <Label htmlFor="tf-enable-password">Confirm your password</Label>
          <Input
            id="tf-enable-password"
            type="password"
            autoComplete="current-password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            disabled={pending}
          />
        </div>
        {error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {error}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Continue
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setPassword("");
              setError(null);
              setState({ step: "idle" });
            }}
            disabled={pending}
          >
            Cancel
          </Button>
        </div>
      </form>
    );
  }

  if (state.step === "scan") {
    const secret = secretFromUri(state.totpURI);
    return (
      <div className="space-y-4">
        <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
          <li>Open your authenticator app and scan the QR code.</li>
          <li>Enter the 6-digit code it shows to confirm it works.</li>
        </ol>
        <div className="flex flex-col items-start gap-4 sm:flex-row">
          <div className="rounded-lg border border-border bg-white p-3">
            <QRCodeSVG value={state.totpURI} size={168} aria-label="TOTP QR code" />
          </div>
          {secret && (
            <div className="space-y-1 text-sm">
              <p className="text-muted-foreground">
                Can&rsquo;t scan? Enter this key manually:
              </p>
              <code className="block break-all rounded-md bg-muted px-2 py-1 font-mono text-xs">
                {secret}
              </code>
            </div>
          )}
        </div>
        <form onSubmit={onVerify} className="max-w-sm space-y-3">
          <div className="space-y-2">
            <Label htmlFor="tf-verify-code">Authentication code</Label>
            <Input
              id="tf-verify-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="123456"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              disabled={pending}
              className="font-mono tracking-widest"
            />
          </div>
          {error && (
            <p
              role="alert"
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            >
              {error}
            </p>
          )}
          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
            Verify and enable
          </Button>
        </form>
      </div>
    );
  }

  // step === "codes": enrollment is complete; the one-time backup code reveal.
  return (
    <div className="space-y-4">
      <p className="flex items-center gap-2 text-sm font-medium text-foreground">
        <ShieldCheck
          className="size-4 text-emerald-600 dark:text-emerald-400"
          aria-hidden
        />
        Two-factor authentication is on. Save your backup codes.
      </p>
      <BackupCodes codes={state.backupCodes} />
      <Button type="button" onClick={() => onEnrolled?.()}>
        I saved my backup codes
      </Button>
    </div>
  );
}

/**
 * The full two-factor section on the account security page: the enroll flow
 * when off; status, backup-code regeneration, and the off switch when on.
 */
export function TwoFactorSettings({
  enabled,
  requiresPassword,
}: {
  enabled: boolean;
  requiresPassword: boolean;
}) {
  const router = useRouter();

  const [password, setPassword] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [regenOpen, setRegenOpen] = React.useState(false);
  const [disableOpen, setDisableOpen] = React.useState(false);
  const [newCodes, setNewCodes] = React.useState<string[] | null>(null);

  function closeDialogs() {
    setRegenOpen(false);
    setDisableOpen(false);
    setPassword("");
    setError(null);
  }

  async function onRegenerate(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    setError(null);
    try {
      const { data, error: err } =
        await authClient.twoFactor.generateBackupCodes({ password });
      if (err) throw err;
      setNewCodes(data?.backupCodes ?? []);
      closeDialogs();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  async function onDisable(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    setError(null);
    try {
      const { error: err } = await authClient.twoFactor.disable({ password });
      if (err) throw err;
      closeDialogs();
      setNewCodes(null);
      router.refresh();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setPending(false);
    }
  }

  if (!enabled) {
    return (
      <TwoFactorEnroll
        requiresPassword={requiresPassword}
        onEnrolled={() => router.refresh()}
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Badge variant="success" className="font-normal">
          Enabled
        </Badge>
        <span className="text-sm text-muted-foreground">
          Sign-in requires a code from your authenticator app.
        </span>
      </div>

      {newCodes && (
        <div className="space-y-2 rounded-md border border-border p-4">
          <p className="text-sm font-medium text-foreground">
            Your new backup codes
          </p>
          <BackupCodes codes={newCodes} />
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={() => {
            setNewCodes(null);
            setRegenOpen(true);
          }}
        >
          Regenerate backup codes
        </Button>
        <Button
          type="button"
          variant="destructive"
          onClick={() => setDisableOpen(true)}
        >
          <ShieldOff className="size-4" aria-hidden />
          Disable two-factor
        </Button>
      </div>

      {/* Regenerate backup codes: invalidates every previous code. */}
      <Dialog
        open={regenOpen}
        onOpenChange={(next) => {
          if (!next) closeDialogs();
        }}
      >
        <DialogHeader>
          <DialogTitle>Regenerate backup codes?</DialogTitle>
          <DialogDescription>
            Your existing backup codes stop working immediately and a fresh set
            is shown once. {requiresPassword && "Confirm your password to continue."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onRegenerate}>
          <DialogBody>
            {requiresPassword && (
              <div className="space-y-2">
                <Label htmlFor="tf-regen-password">Password</Label>
                <Input
                  id="tf-regen-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={pending}
                />
              </div>
            )}
            {error && (
              <p
                role="alert"
                className="mt-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </p>
            )}
          </DialogBody>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={closeDialogs}
              disabled={pending}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={pending} aria-busy={pending}>
              {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Regenerate codes
            </Button>
          </DialogFooter>
        </form>
      </Dialog>

      {/* Disable: drops the second factor entirely. */}
      <Dialog
        open={disableOpen}
        onOpenChange={(next) => {
          if (!next) closeDialogs();
        }}
      >
        <DialogHeader>
          <div className="mb-1 grid size-10 place-items-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <AlertTriangle className="size-5" aria-hidden />
          </div>
          <DialogTitle>Disable two-factor authentication?</DialogTitle>
          <DialogDescription>
            Your account goes back to single-step sign-in and your backup codes
            are discarded. If your organization requires two-factor, you will be
            asked to set it up again at your next visit.
            {requiresPassword && " Confirm your password to continue."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onDisable}>
          <DialogBody>
            {requiresPassword && (
              <div className="space-y-2">
                <Label htmlFor="tf-disable-password">Password</Label>
                <Input
                  id="tf-disable-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={pending}
                />
              </div>
            )}
            {error && (
              <p
                role="alert"
                className="mt-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </p>
            )}
          </DialogBody>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={closeDialogs}
              disabled={pending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={pending}
              aria-busy={pending}
            >
              {pending ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Disable two-factor
            </Button>
          </DialogFooter>
        </form>
      </Dialog>
    </div>
  );
}
