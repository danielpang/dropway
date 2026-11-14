import { notFound } from "next/navigation";
import { RoomsPreview } from "./preview";

export const dynamic = "force-dynamic";
export default function Page() {
  if (process.env.NODE_ENV !== "development") notFound();
  return <RoomsPreview />;
}
