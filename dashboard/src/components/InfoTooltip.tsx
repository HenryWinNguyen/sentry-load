import { Info } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

// A small hover-triggered "what does this mean" affordance — chart
// captions used to sit permanently under every card title, which is
// exactly the kind of thing that reads as helpful once and as clutter
// forever after for anyone who's used the page more than a couple of
// times. Keeping the explanation one hover away instead of always-on
// serves both a first-time visitor and a returning one.
export default function InfoTooltip({ children }: { children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger
        aria-label="What does this mean?"
        className="inline-flex shrink-0 cursor-help text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none"
      >
        <Info className="h-3.5 w-3.5" />
      </TooltipTrigger>
      <TooltipContent side="top" align="start" className="max-w-64 text-left">
        {children}
      </TooltipContent>
    </Tooltip>
  );
}
