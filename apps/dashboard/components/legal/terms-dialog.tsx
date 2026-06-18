"use client";

import * as React from "react";
import { Download } from "lucide-react";

import { TermsContent } from "@/components/legal/terms-content";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { TERMS_PDF_PATH, TERMS_UPDATED_LABEL } from "@/lib/legal/terms";
import { cn } from "@/lib/utils";

/**
 * An inline "Terms and Conditions" link that opens a modal with the full Terms.
 * The reader can scroll the text or download the PDF copy. Used in the signup
 * consent line; the trigger is a plain button styled as a link so it sits inline
 * in a sentence.
 *
 * The trigger calls preventDefault on click so that, when it is rendered inside a
 * <label> (the consent checkbox), opening the modal does not also toggle the box.
 */
export function TermsDialog({
  label = "Terms and Conditions",
  className,
}: {
  label?: string;
  className?: string;
}) {
  const [open, setOpen] = React.useState(false);

  return (
    <>
      <button
        type="button"
        onClick={(e) => {
          e.preventDefault();
          setOpen(true);
        }}
        className={cn(
          "font-medium text-foreground underline underline-offset-4 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm",
          className,
        )}
      >
        {label}
      </button>

      <Dialog open={open} onOpenChange={setOpen} className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Terms and Conditions</DialogTitle>
          <a
            href={TERMS_PDF_PATH}
            download
            className="inline-flex w-fit items-center gap-1.5 text-xs font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background rounded-sm"
          >
            <Download className="size-3.5" aria-hidden />
            Download PDF (last updated {TERMS_UPDATED_LABEL})
          </a>
        </DialogHeader>
        <DialogBody className="max-h-[60vh] overflow-y-auto">
          <TermsContent />
        </DialogBody>
        <div className="flex justify-end p-6 pt-4">
          <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
            Close
          </Button>
        </div>
      </Dialog>
    </>
  );
}
