import { MoonIcon, SunIcon } from "lucide-react";
import type { FC } from "react";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { useTheme } from "@/lib/theme";

export const ThemeToggle: FC = () => {
  const { resolved, toggle } = useTheme();
  const nextLabel = resolved === "dark" ? "Switch to light" : "Switch to dark";

  return (
    <TooltipIconButton
      variant="ghost"
      size="icon"
      tooltip={nextLabel}
      side="bottom"
      onClick={toggle}
      className="size-7"
      aria-label={nextLabel}
    >
      {resolved === "dark" ? (
        <SunIcon className="size-[17px]" />
      ) : (
        <MoonIcon className="size-[17px]" />
      )}
    </TooltipIconButton>
  );
};
