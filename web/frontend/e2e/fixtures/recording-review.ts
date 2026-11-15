export const recordingPath = "/audio/recording-review";
const id = "recording-review";
export const recordingApi = `/api/v1/transcription/${id}`;
const createdAt = "2026-11-14T17:00:00Z";

export const sampleLines = [
    "Let's go over the plan for the library opening.",
    "The first tour starts at ten on Saturday.",
    "We will meet by the entrance fifteen minutes early.",
    "I checked the signs for the reading room.",
    "The blue sign points toward the courtyard.",
    "Can we put the welcome table beside the stairs?",
    "Yes, there is room for two volunteers there.",
    "I'll bring the printed schedule and the floor map.",
    "The second tour takes the elevator to the archive.",
    "We have enough headsets for both groups.",
    "Let's leave a few spare sets at the desk.",
    "Who can show the volunteers how to pair them?",
    "Morgan can do a short demonstration before opening.",
    "We should also test the microphone in the hall.",
    "I'll send a reminder with the updated schedule.",
    "Thanks, that covers everything for Saturday.",
];
export const secondsPerLine = 4;
const duration = sampleLines.length * secondsPerLine;
const primarySegments = sampleLines.map((text, index) => ({
    text,
    start: index * secondsPerLine,
    end: index * secondsPerLine + 3.6,
    speaker: index % 2 === 0 ? "SPEAKER_00" : "SPEAKER_01",
}));
const alternateLines: Record<number, string> = {
    1: "The first tour starts at noon on Saturday.",
    4: "The blue sign points toward the corridor.",
    7: "I'll bring the printed schedule and the flyer.",
    11: "Who can show the visitors how to pair them?",
    13: "We should also test the microphone in the lobby.",
};
const comparisonSegments = primarySegments.map((segment, index) => ({
    ...segment,
    start: Math.max(0, segment.start - 0.65),
    text: alternateLines[index] ?? segment.text,
}));
const transcript = (segments: typeof primarySegments) => ({
    available: true,
    transcript: { text: segments.map(({ text }) => text).join(" "), segments },
});
const parameters = { model: "small", diarize: true, device: "cuda", diarize_model: "diarizen" };
const runs = ["primary", "comparison"].map((runId, index) => ({
    id: runId,
    transcription_job_id: id,
    run_number: index + 1,
    status: "completed",
    created_at: createdAt,
    started_at: createdAt,
    completed_at: createdAt,
    processing_duration: 19000 + index * 8500,
    has_transcript: true,
    actual_parameters: index === 0 ? parameters : { ...parameters, model: "medium" },
}));

export const fixtureToken = `fixture.${Buffer.from(JSON.stringify({ exp: 4_000_000_000 })).toString("base64url")}.synthetic`;

export function pcmWave(pcm: Buffer, sampleRate = 16000): Buffer {
    const header = Buffer.alloc(44);
    header.write("RIFF", 0);
    header.writeUInt32LE(pcm.length + 36, 4);
    header.write("WAVEfmt ", 8);
    header.writeUInt32LE(16, 16);
    header.writeUInt16LE(1, 20);
    header.writeUInt16LE(1, 22);
    header.writeUInt32LE(sampleRate, 24);
    header.writeUInt32LE(sampleRate * 2, 28);
    header.writeUInt16LE(2, 32);
    header.writeUInt16LE(16, 34);
    header.write("data", 36);
    header.writeUInt32LE(pcm.length, 40);
    return Buffer.concat([header, pcm]);
}

type SpeakerName = { original_speaker: string; custom_name: string };
type Response = { status?: number; json?: unknown; body?: Buffer | string; headers?: Record<string, string> };

export function createRecordingFixture(audio = pcmWave(Buffer.alloc(duration * 32000))) {
    let names: SpeakerName[] = [
        { original_speaker: "SPEAKER_01", custom_name: "Morgan" },
        { original_speaker: "SPEAKER_07", custom_name: "Guest from an earlier run" },
    ];
    const requests = { queue: 0, speakerReads: 0, saves: [] as SpeakerName[][] };

    function respond(method: string, pathname: string, range?: string, payload?: { mappings: SpeakerName[] }): Response {
        const route = pathname.replace(/\/$/, "");
        if (route === "/health") return { json: { status: "healthy", version: "preview" } };
        if (route === "/api/v1/auth/registration-status") return { json: { registration_enabled: false } };
        if (route === "/api/v1/auth/login" || route === "/api/v1/auth/refresh") {
            return { json: { token: fixtureToken, user: { id: 1, username: "preview" } } };
        }
        if (route === recordingApi + "/speakers" && method === "POST" && Array.isArray(payload?.mappings)) {
            names = payload.mappings;
            requests.saves.push(names);
            return { json: names };
        }
        if (method !== "GET" && method !== "HEAD") return { status: 405, json: {} };
        if (route === recordingApi) {
            return { json: { id, title: "Library opening · sample recording", status: "completed", created_at: createdAt, audio_path: "sample.wav", parameters } };
        }
        if (route === recordingApi + "/runs") return { json: { job_id: id, active_run_id: "primary", runs } };
        if (route === recordingApi + "/transcript" || route === recordingApi + "/runs/primary/transcript") return { json: transcript(primarySegments) };
        if (route === recordingApi + "/runs/comparison/transcript") return { json: transcript(comparisonSegments) };
        if (route === recordingApi + "/queue") {
            requests.queue++;
            return { json: { items: [], active_item: null, job_id: id } };
        }
        if (route === recordingApi + "/speakers") {
            requests.speakerReads++;
            return { json: names };
        }
        if (route === recordingApi + "/audio") {
            const headers: Record<string, string> = { "content-type": "audio/wav", "accept-ranges": "bytes" };
            const match = range?.match(/^bytes=(\d+)-(\d*)$/);
            if (!match) return { body: audio, headers };
            const start = Number(match[1]);
            const end = Math.min(match[2] ? Number(match[2]) : audio.length - 1, audio.length - 1);
            if (start > end) return { status: 416, headers: { "content-range": `bytes */${audio.length}` } };
            return {
                status: 206,
                body: audio.subarray(start, end + 1),
                headers: { ...headers, "content-range": `bytes ${start}-${end}/${audio.length}` },
            };
        }
        if (route === recordingApi + "/notes" || route === "/api/v1/profiles") return { json: [] };
        if (route === "/api/v1/admin/queue/activity") return { json: { workers: 1, busy_workers: 0, recordings: [] } };
        if (route === "/api/v1/events") return { body: ": sample\n\n", headers: { "content-type": "text/event-stream" } };
        if (route.endsWith("/recovery")) return { json: { available: false } };
        if (route === "/api/v1/llm/config") return { json: { is_active: false } };
        return { status: 404, json: {} };
    }

    return { respond, requests };
}
