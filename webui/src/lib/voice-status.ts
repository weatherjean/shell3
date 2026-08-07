import { useSyncExternalStore } from "react";

// What the voice pipeline is doing right now. Both halves of it have a step
// where nothing visible happens — the upload that transcribes a recording, and
// the model call that produces speech — and a control that looks idle while it
// waits reads as broken.

export type VoiceStatus =
  | "idle"
  | "recording"
  | "transcribing"
  | "generating"
  | "speaking";

let current: VoiceStatus = "idle";
const listeners = new Set<() => void>();

export const setVoiceStatus = (next: VoiceStatus) => {
  if (next === current) return;
  current = next;
  listeners.forEach((notify) => notify());
};

/** Clears the status, but only if it is still the one the caller set. */
export const clearVoiceStatus = (ifStill: VoiceStatus) => {
  if (current === ifStill) setVoiceStatus("idle");
};

const subscribe = (notify: () => void) => {
  listeners.add(notify);
  return () => listeners.delete(notify);
};

export const useVoiceStatus = (): VoiceStatus =>
  useSyncExternalStore(
    subscribe,
    () => current,
    () => "idle" as const,
  );

export const VOICE_LABELS: Record<VoiceStatus, string> = {
  idle: "",
  recording: "Listening…",
  transcribing: "Transcribing…",
  generating: "Generating voice…",
  speaking: "Playing",
};
