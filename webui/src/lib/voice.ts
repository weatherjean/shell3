// Voice adapters backed by shell3's own media models.
//
// Dictation records with MediaRecorder and transcribes through `media.stt`;
// speech synthesis plays audio returned by `media.tts`. Both fall back to the
// browser's built-in Web Speech APIs when the backend has no media model
// configured, so voice still works against the mock.

import {
  WebSpeechDictationAdapter,
  WebSpeechSynthesisAdapter,
  type DictationAdapter,
  type SpeechSynthesisAdapter,
} from "@assistant-ui/react";
import { synthesize, transcribe } from "@/lib/api";
import { clearVoiceStatus, setVoiceStatus } from "@/lib/voice-status";

type Unsubscribe = () => void;

const pickMimeType = (): string | undefined => {
  const candidates = [
    "audio/webm;codecs=opus",
    "audio/webm",
    "audio/mp4",
    "audio/ogg;codecs=opus",
  ];
  return candidates.find((type) => MediaRecorder.isTypeSupported(type));
};

/**
 * Records a single utterance and transcribes it server-side. There are no
 * interim results — `media.stt` sees the whole clip at once — so the composer
 * fills in when the user stops recording.
 */
class ServerDictationAdapter implements DictationAdapter {
  readonly disableInputDuringDictation = true;

  listen(): DictationAdapter.Session {
    const speechStartCallbacks = new Set<() => void>();
    const speechEndCallbacks = new Set<(r: DictationAdapter.Result) => void>();
    const speechCallbacks = new Set<(r: DictationAdapter.Result) => void>();

    let recorder: MediaRecorder | undefined;
    let stream: MediaStream | undefined;
    let cancelled = false;
    const chunks: Blob[] = [];

    const session: DictationAdapter.Session = {
      status: { type: "starting" },
      stop: async () => {
        if (!recorder || recorder.state === "inactive") return;
        const stopped = new Promise<void>((resolve) => {
          recorder!.addEventListener("stop", () => resolve(), { once: true });
        });
        recorder.stop();
        await stopped;
        releaseMic();

        if (cancelled) return;
        setVoiceStatus("transcribing");
        try {
          const audio = new Blob(chunks, { type: chunks[0]?.type ?? "audio/webm" });
          const transcript = audio.size > 0 ? await transcribe(audio) : "";
          if (cancelled) return;
          session.status = { type: "ended", reason: "stopped" };
          if (transcript) {
            for (const cb of speechCallbacks) cb({ transcript, isFinal: true });
            for (const cb of speechEndCallbacks) cb({ transcript });
          }
        } catch (error) {
          session.status = { type: "ended", reason: "error" };
          console.error("Transcription failed:", error);
        } finally {
          clearVoiceStatus("transcribing");
        }
      },
      cancel: () => {
        cancelled = true;
        session.status = { type: "ended", reason: "cancelled" };
        if (recorder && recorder.state !== "inactive") recorder.stop();
        releaseMic();
        setVoiceStatus("idle");
      },
      onSpeechStart: (callback) => subscribe(speechStartCallbacks, callback),
      onSpeechEnd: (callback) => subscribe(speechEndCallbacks, callback),
      onSpeech: (callback) => subscribe(speechCallbacks, callback),
    };

    const releaseMic = () => {
      stream?.getTracks().forEach((track) => track.stop());
      stream = undefined;
    };

    void (async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        if (cancelled) return releaseMic();

        const mimeType = pickMimeType();
        recorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
        recorder.addEventListener("dataavailable", (event) => {
          if (event.data.size > 0) chunks.push(event.data);
        });
        recorder.start();
        session.status = { type: "running" };
        setVoiceStatus("recording");
        for (const cb of speechStartCallbacks) cb();
      } catch (error) {
        session.status = { type: "ended", reason: "error" };
        setVoiceStatus("idle");
        console.error("Microphone unavailable:", error);
      }
    })();

    return session;
  }
}

/** Plays `media.tts` audio, matching the Web Speech utterance lifecycle. */
class ServerSpeechSynthesisAdapter implements SpeechSynthesisAdapter {
  speak(text: string): SpeechSynthesisAdapter.Utterance {
    const subscribers = new Set<() => void>();
    const controller = new AbortController();
    let audio: HTMLAudioElement | undefined;
    let objectUrl: string | undefined;

    const end = (reason: "finished" | "cancelled" | "error", error?: unknown) => {
      if (utterance.status.type === "ended") return;
      utterance.status = { type: "ended", reason, error };
      if (objectUrl) URL.revokeObjectURL(objectUrl);
      setVoiceStatus("idle");
      subscribers.forEach((handler) => handler());
    };

    const utterance: SpeechSynthesisAdapter.Utterance = {
      status: { type: "starting" },
      cancel: () => {
        controller.abort();
        audio?.pause();
        end("cancelled");
      },
      subscribe: (callback) => {
        if (utterance.status.type === "ended") {
          let unsubscribed = false;
          queueMicrotask(() => {
            if (!unsubscribed) callback();
          });
          return () => {
            unsubscribed = true;
          };
        }
        subscribers.add(callback);
        return () => {
          subscribers.delete(callback);
        };
      },
    };

    setVoiceStatus("generating");
    void (async () => {
      try {
        const blob = await synthesize(text, controller.signal);
        if (controller.signal.aborted) return;
        objectUrl = URL.createObjectURL(blob);
        audio = new Audio(objectUrl);
        audio.addEventListener("ended", () => end("finished"), { once: true });
        audio.addEventListener("error", () => end("error"), { once: true });
        await audio.play();
        utterance.status = { type: "running" };
        setVoiceStatus("speaking");
        subscribers.forEach((handler) => handler());
      } catch (error) {
        if (!controller.signal.aborted) end("error", error);
      }
    })();

    return utterance;
  }
}

const subscribe = <T>(set: Set<T>, callback: T): Unsubscribe => {
  set.add(callback);
  return () => {
    set.delete(callback);
  };
};

/**
 * Picks server-backed voice when the backend has the media models wired, and
 * the browser's own speech APIs otherwise. Returns undefined when neither is
 * available, which hides the mic button.
 */
export const buildVoiceAdapters = (voice: { stt: boolean; tts: boolean }) => {
  const canRecord =
    typeof MediaRecorder !== "undefined" &&
    typeof navigator !== "undefined" &&
    !!navigator.mediaDevices;

  const dictation: DictationAdapter | undefined = voice.stt && canRecord
    ? new ServerDictationAdapter()
    : WebSpeechDictationAdapter.isSupported()
      ? new WebSpeechDictationAdapter()
      : undefined;

  const speech: SpeechSynthesisAdapter | undefined = voice.tts
    ? new ServerSpeechSynthesisAdapter()
    : typeof window !== "undefined" && "speechSynthesis" in window
      ? new WebSpeechSynthesisAdapter()
      : undefined;

  return { dictation, speech };
};
