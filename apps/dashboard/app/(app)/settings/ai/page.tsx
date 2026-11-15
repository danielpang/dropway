import { notFound } from "next/navigation";
import { AIConnections } from "@/components/project-room/ai-connections";
import { collaborationEnabled } from "@/lib/project-room/capabilities";
export const metadata = { title: "Personal AI settings" };
export const dynamic = "force-dynamic";
export default async function Page() {
  if (!(await collaborationEnabled())) notFound();
  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header>
        <h1 className="text-2xl font-semibold">Personal AI settings</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Choose how you work across projects in this organization. Project
          model settings can override your personal default.
        </p>
      </header>
      <AIConnections />
    </div>
  );
}
