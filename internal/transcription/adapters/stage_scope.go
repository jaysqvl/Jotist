package adapters

// recognitionOnlyParameters implements the shared stage boundary. A request
// can require aligned speakers in its final output without requiring them from
// the recognition subprocess. Native speaker recognition retains its labels.
// Validate the complete request at admission; validate this projection at the
// worker boundary. Never mutate saved profile parameters.
func recognitionOnlyParameters(params map[string]interface{}) map[string]interface{} {
	resolved := copyAdapterParameters(params)
	resolved["align_words"], resolved["no_align"] = false, true
	resolved["external_diarization_requested"] = false
	if resolved["diarize_model"] != "native" {
		resolved["diarize"] = false
	}
	return resolved
}
