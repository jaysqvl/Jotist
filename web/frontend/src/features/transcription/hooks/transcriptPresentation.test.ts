import assert from "node:assert/strict";
import test from "node:test";
import { transcriptDisplaySegments, transcriptRowOffsets, transcriptSpeakerLabel, type PresentedTranscript } from "./transcriptPresentation.ts";
import { formatTranscriptAsSRT, formatTranscriptAsTXT } from "./transcriptDownloadFormatters.ts";

const transcript = {
    text: "It would be a hair.",
    segments: [{start:1.92,end:2.16,text:"It",speaker:"2"},{start:2.16,end:2.16,text:"would"},{start:2.16,end:2.4,text:"be",speaker:"2"},{start:2.4,end:2.4,text:"a"},{start:2.4,end:2.64,text:"hair.",speaker:"2"}],
    word_segments: [{start:1.92,end:2.16,word:"It"},{start:2.16,end:2.16,word:"would"},{start:2.16,end:2.4,word:"be"},{start:2.4,end:2.4,word:"a"},{start:2.4,end:2.64,word:"hair."}],
    presentation: {version:1,grouping:"speaker_turns",segments:[{start:1.92,end:2.64,text:"It would be a hair.",speaker:"2",word_indices:[0,1,2,3,4]}],speaker_labels:{"2":"SPEAKER_2"},inferred_point_speakers:2},
} satisfies PresentedTranscript & {text:string};

test("display, TXT and SRT use server-owned rows and retain custom speaker names",()=>{
    assert.equal(transcriptDisplaySegments(transcript).length,1);
    assert.equal(formatTranscriptAsTXT(transcript,{}, {includeTimestamps:true,includeSpeakerLabels:true}),"[0:01] SPEAKER_2: It would be a hair.");
    assert.equal(formatTranscriptAsSRT(transcript,{"2":"Sam"}),"1\n00:00:01,920 --> 00:00:02,640\nSam: It would be a hair.\n\n");
    assert.equal(transcriptSpeakerLabel("2",transcript,{"2":"Sam"}),"Sam");
    assert.equal(transcript.segments.length,5);
    assert.equal(formatTranscriptAsSRT({text:"End",segments:[{start:59.9996,end:61,text:"End"}]},{}),"1\n00:01:00,000 --> 00:01:01,000\nEnd\n\n");
});

test("equal timestamps still have distinct text offsets and preserve each word's seek time",()=>{
    const row=transcriptDisplaySegments(transcript)[0];
    const offsets=transcriptRowOffsets(row.text,row.word_indices,transcript.word_segments);
    assert.deepEqual(offsets.map(x=>row.text.slice(x.startChar,x.endChar)),["It","would","be","a","hair."]);
    assert.deepEqual(offsets.map(x=>x.startTime),[1.92,2.16,2.16,2.4,2.4]);
});

test("native text remains authoritative and unmatched alignment cannot insert words",()=>{
    const old={segments:[{start:0,end:1,text:"It"},{start:1,end:2,text:"would be"}],word_segments:[{start:1,end:1,word:"would"}]};
    assert.deepEqual(transcriptDisplaySegments(old).map(x=>x.text),["It","would be"]);
    assert.deepEqual(transcriptRowOffsets("there",[0],[{start:0,end:1,word:"he"}]),[]);
    const offsets=transcriptRowOffsets("猫 🐈 works.",[0,1,2],[{start:0,end:1,word:"猫"},{start:1,end:2,word:"🐈"},{start:2,end:3,word:"works."}]);
    assert.deepEqual(offsets.map(x=>[x.startChar,x.endChar]),[[0,1],[2,4],[5,11]]);
});
