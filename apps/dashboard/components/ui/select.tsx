"use client";

import * as React from "react";
import { SelectMenu, type SelectMenuOption } from "@/components/ui/select-menu";
import { cn } from "@/lib/utils";

export type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement> & {
  variant?: "outline" | "ghost";
  align?: "start" | "end";
  onValuePreview?: (value: string) => void;
};

function optionText(children: React.ReactNode): string {
  return React.Children.toArray(children).map((child) =>
    React.isValidElement<{ children?: React.ReactNode }>(child)
      ? optionText(child.props.children)
      : String(child),
  ).join("");
}

function readOptions(children: React.ReactNode, group?: string, disabled = false): SelectMenuOption[] {
  return React.Children.toArray(children).flatMap((child) => {
    if (!React.isValidElement<{
      children?: React.ReactNode; value?: string | number; label?: string; disabled?: boolean;
    }>(child)) return [];
    if (child.type === "optgroup") return readOptions(child.props.children, child.props.label, !!child.props.disabled);
    if (child.type === React.Fragment) return readOptions(child.props.children, group, disabled);
    if (child.type !== "option") return [];
    const text = optionText(child.props.children);
    return [{ value: String(child.props.value ?? text), label: child.props.label ?? text, group, disabled: disabled || child.props.disabled }];
  });
}

/** Shared custom popup, with a hidden select preserving form values and change events. */
const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, variant, align, onValuePreview, id: suppliedId, style, autoFocus, ...props }, ref) => {
    const generatedId = React.useId();
    const id = suppliedId ?? generatedId;
    const native = React.useRef<HTMLSelectElement>(null);
    const options = readOptions(children);
    const [uncontrolled, setUncontrolled] = React.useState(String(props.defaultValue ?? options.find((option) => !option.disabled)?.value ?? ""));
    const [invalid, setInvalid] = React.useState(false);
    const value = props.value === undefined ? uncontrolled : String(props.value);
    // Native selects fall back to the first enabled item when options change.
    const displayedValue = options.some((option) => option.value === value)
      ? value : options.find((option) => !option.disabled)?.value ?? "";

    React.useEffect(() => {
      const form = native.current?.form;
      let timer: ReturnType<typeof setTimeout>;
      const reset = () => {
        timer = setTimeout(() => {
          setUncontrolled(native.current?.value ?? "");
          setInvalid(false);
        }, 0);
      };
      form?.addEventListener("reset", reset);
      return () => { form?.removeEventListener("reset", reset); clearTimeout(timer); };
    }, []);

    return (
      <>
        <select
          {...props}
          id={`${id}-native`}
          ref={(element) => {
            native.current = element;
            if (typeof ref === "function") ref(element);
            else if (ref) ref.current = element;
          }}
          hidden
          aria-hidden="true"
          tabIndex={-1}
          onChange={(event) => {
            setUncontrolled(event.target.value);
            setInvalid(false);
            props.onChange?.(event);
          }}
          onInvalid={(event) => {
            event.preventDefault();
            setInvalid(true);
            document.getElementById(id)?.focus();
            props.onInvalid?.(event);
          }}
        >
          {children}
        </select>
        <SelectMenu
          label={props["aria-label"]}
          value={displayedValue}
          options={options.filter((option) => !(option.disabled && option.value === ""))}
          placeholder={options.find((option) => option.disabled && option.value === "")?.label}
          disabled={props.disabled}
          variant={variant}
          align={align}
          className={cn("h-10 w-full", className)}
          triggerProps={{
            id, style, autoFocus, title: props.title, tabIndex: props.tabIndex,
            "aria-labelledby": props["aria-labelledby"],
            "aria-describedby": props["aria-describedby"],
            "aria-required": props.required,
            "aria-invalid": invalid || props["aria-invalid"],
          }}
          onValuePreview={onValuePreview}
          onValueChange={(next) => {
            if (!native.current) return;
            native.current.value = next;
            native.current.dispatchEvent(new Event("change", { bubbles: true }));
          }}
        />
      </>
    );
  },
);
Select.displayName = "Select";

export { Select };
