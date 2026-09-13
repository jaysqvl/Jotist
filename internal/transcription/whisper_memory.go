package transcription

import (
	"encoding/json"
	"fmt"
	"math"
)

func whisperVariantMemoryEstimates() string {
	rows := make([]map[string]string, 0)
	for _, checkpoint := range []struct {
		model      string
		parameters float64
	}{
		{"tiny", .039}, {"tiny.en", .039}, {"base", .074}, {"base.en", .074},
		{"small", .244}, {"small.en", .244}, {"medium", .769}, {"medium.en", .769},
		{"large", 1.55}, {"large-v1", 1.55}, {"large-v2", 1.55}, {"large-v3", 1.55},
	} {
		estimate := func(bytes float64) string {
			return fmt.Sprintf("%.0f–%.0f", math.Ceil(checkpoint.parameters*bytes+2), math.Ceil(checkpoint.parameters*bytes+6))
		}
		cpu := estimate(4)
		if checkpoint.parameters == 1.55 {
			cpu = "10–16"
		}
		rows = append(rows, map[string]string{
			"model":               checkpoint.model,
			"cpu_float32_ram_gb":  cpu,
			"gpu_float16_vram_gb": estimate(2),
			"gpu_float32_vram_gb": estimate(4),
			"notes":               "Unmeasured batch-1, short-chunk planning range using checkpoint parameter count and runtime headroom. Quantization can use less; alignment, speaker models and long recordings can require more.",
		})
	}
	encoded, _ := json.Marshal(rows)
	return string(encoded)
}
