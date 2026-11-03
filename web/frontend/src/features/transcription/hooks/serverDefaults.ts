import { applyDeviceSelectionDefaults, type SelectionDefaultParams } from "./selectionDefaults.ts";
import type { TranscriptionModelCapability } from "./modelCapabilities.ts";

export type DefaultDevice = "cpu" | "cuda";

// The server's target determines defaults; the browser may be on another host.
// Older server responses retain the existing GPU-first behavior.
export function serverDefaultDevice(catalog: unknown): DefaultDevice {
    if (!catalog || typeof catalog !== "object") return "cuda";
    const defaults = (catalog as { execution_defaults?: { default_device?: unknown } }).execution_defaults;
    return defaults?.default_device === "cpu" ? "cpu" : "cuda";
}

export function applyServerDraftDefaults<T extends SelectionDefaultParams>(
    params: T, device: DefaultDevice, models: TranscriptionModelCapability[],
    hasInitialParams: boolean, deviceEdited: boolean,
): T {
    if (hasInitialParams || deviceEdited || device !== "cpu") return params;
    return applyDeviceSelectionDefaults(params, "cpu", models);
}
