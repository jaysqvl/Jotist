package api

import (
	"encoding/json"
	"testing"
)

func TestTranscriptResponseAddsProjectionAndPreservesRawFields(t *testing.T) {
	raw := `{"text":"hello there","segments":[{"start":1,"end":2,"text":"hello","speaker":"2"},{"start":2,"end":3,"text":"there","speaker":"2"}],"word_segments":[{"start":1,"end":2,"word":"hello","speaker":"2"},{"start":2,"end":3,"word":"there","speaker":"2"}],"model_used":"future/model","vendor_extension":{"original":true}}`
	got, err := parseTranscriptPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	object := got.(map[string]interface{})
	if object["presentation"] == nil {
		t.Fatal("no projection for API/CLI readers")
	}
	delete(object, "presentation")
	var before interface{}
	json.Unmarshal([]byte(raw), &before)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(object)
	if string(a) != string(b) {
		t.Fatal("response replaced raw output or extension fields")
	}
	legacy, err := parseTranscriptPayload(`"old text transcript"`)
	if err != nil || legacy != "old text transcript" {
		t.Fatal("legacy text payload changed")
	}
}
