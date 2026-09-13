package adapters

// VoxtralAdapter preserves the legacy adapter identifier for saved profiles.
// New profiles select the exact downloadable checkpoint through LocalASRModels.
type VoxtralAdapter = LocalASRAdapter

func NewVoxtralAdapter(envPath string) *VoxtralAdapter {
	adapter, err := NewLocalASRAdapter(envPath, "mistralai/Voxtral-Mini-3B-2507")
	if err != nil {
		panic(err) // The embedded catalog is a build-time invariant.
	}
	adapter.capabilities.Metadata["legacy_alias"] = "true"
	return adapter
}
