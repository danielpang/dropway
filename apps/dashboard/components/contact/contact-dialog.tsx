"use client";

import * as React from "react";
import { Bug, Lightbulb, Loader2 } from "lucide-react";

import {
  submitContactAction,
  type ContactKind,
} from "@/app/(app)/contact/actions";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

const KINDS: { value: ContactKind; label: string; icon: typeof Bug }[] = [
  { value: "bug", label: "Bug report", icon: Bug },
  { value: "feature", label: "Feature request", icon: Lightbulb },
];

/**
 * A footer "Contact" link that opens a small popup for sending a bug report or a
 * feature request. On submit it calls the submitContactAction server action,
 * which mails the org's support inbox. Renders its own trigger so it can be
 * dropped straight into the footer.
 */
export function ContactDialog({ className }: { className?: string }) {
  const [open, setOpen] = React.useState(false);

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={cn(
          "rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
          className,
        )}
      >
        Contact
      </button>

      <Dialog open={open} onOpenChange={setOpen} className="max-w-md">
        <ContactForm onDone={() => setOpen(false)} />
      </Dialog>
    </>
  );
}

function ContactForm({ onDone }: { onDone: () => void }) {
  const [kind, setKind] = React.useState<ContactKind>("bug");
  const [subject, setSubject] = React.useState("");
  const [message, setMessage] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [sent, setSent] = React.useState(false);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (pending) return;
    setError(null);
    setPending(true);
    try {
      const res = await submitContactAction({ kind, subject, message });
      if (res.ok) {
        setSent(true);
      } else {
        setError(res.message);
      }
    } catch {
      setError("Could not send your message. Please try again.");
    } finally {
      setPending(false);
    }
  }

  if (sent) {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Thanks for the note</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <p className="text-sm text-muted-foreground">
            Your {kind === "bug" ? "bug report" : "feature request"} is on its
            way to our team. We read every one.
          </p>
        </DialogBody>
        <DialogFooter>
          <Button type="button" onClick={onDone}>
            Done
          </Button>
        </DialogFooter>
      </>
    );
  }

  return (
    <form onSubmit={onSubmit}>
      <DialogHeader>
        <DialogTitle>Contact us</DialogTitle>
      </DialogHeader>
      <DialogBody className="space-y-4">
        <div className="space-y-1.5">
          <Label>What is this about?</Label>
          <div
            role="radiogroup"
            aria-label="Message type"
            className="grid grid-cols-2 gap-2"
          >
            {KINDS.map(({ value, label, icon: Icon }) => {
              const active = kind === value;
              return (
                <button
                  key={value}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  onClick={() => setKind(value)}
                  className={cn(
                    "inline-flex items-center justify-center gap-2 rounded-md border px-3 py-2 text-sm font-medium transition-colors",
                    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background",
                    active
                      ? "border-primary bg-primary/[0.06] text-foreground"
                      : "border-border text-muted-foreground hover:text-foreground",
                  )}
                >
                  <Icon className="size-4" aria-hidden />
                  {label}
                </button>
              );
            })}
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="contact-subject">Subject</Label>
          <Input
            id="contact-subject"
            value={subject}
            maxLength={200}
            placeholder={
              kind === "bug" ? "Deploys fail on…" : "It would help if…"
            }
            onChange={(e) => setSubject(e.target.value)}
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="contact-message">Message</Label>
          <textarea
            id="contact-message"
            value={message}
            required
            maxLength={5000}
            rows={5}
            placeholder={
              kind === "bug"
                ? "What happened, and what did you expect?"
                : "What are you trying to do, and how would this help?"
            }
            onChange={(e) => setMessage(e.target.value)}
            className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          />
        </div>

        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </DialogBody>
      <DialogFooter>
        <Button
          type="button"
          variant="secondary"
          onClick={onDone}
          disabled={pending}
        >
          Cancel
        </Button>
        <Button type="submit" disabled={pending || !message.trim()}>
          {pending ? (
            <>
              <Loader2 className="size-4 animate-spin" aria-hidden />
              Sending
            </>
          ) : (
            "Send"
          )}
        </Button>
      </DialogFooter>
    </form>
  );
}
