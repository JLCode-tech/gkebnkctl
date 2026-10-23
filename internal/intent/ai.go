package intent

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultSyntheticImage is the default container image used for simulated vLLM inference.
const DefaultSyntheticImage = "ghcr.io/llm-d/llm-d-inference-sim:v0.11.2"

// AISpec is the top-level opt-in AI inference block in cluster.yaml.
// When absent (nil) or Enabled=false, all AI-related phases are skipped.
type AISpec struct {
	Synthetic *SyntheticSpec `yaml:"synthetic,omitempty"`
}

// SyntheticSpec configures the lightweight synthetic/simulated vLLM endpoint
// using llm-d-inference-sim. Runs on standard CPU nodes without requiring
// physical GPUs, HuggingFace tokens, or model weight downloads.
type SyntheticSpec struct {
	// Enabled is the master switch for synthetic inference simulation.
	Enabled bool `yaml:"enabled"`
	// Image is the container image to run (default: DefaultSyntheticImage).
	Image string `yaml:"image,omitempty"`
	// Profile is the simulated model size (default: DefaultSyntheticProfile).
	Profile string `yaml:"profile,omitempty"`
	// Model is the Hugging Face repo the simulator serves and tokenizes with; endpoint
	// pickers match its KV events against it (default: the profile's ungated repo).
	Model string `yaml:"model,omitempty"`
	// ServedModelName is an alias the simulator also answers to (default: "llama3").
	ServedModelName string `yaml:"servedModelName,omitempty"`
	// Replicas is the number of simulator pods (default: 1).
	Replicas int `yaml:"replicas,omitempty"`
	// TTFTBaseMs is the fixed prefill overhead in ms, before per-token prefill.
	TTFTBaseMs int `yaml:"ttftBaseMs,omitempty"`
	// PrefillUsPerToken is the prefill time per uncached prompt token in µs.
	PrefillUsPerToken int `yaml:"prefillUsPerToken,omitempty"`
	// ITLMs is the Inter-Token Latency per output token in ms.
	ITLMs float64 `yaml:"itlMs,omitempty"`
	// MaxNumSeqs is the number of requests served at once; the rest queue.
	MaxNumSeqs int `yaml:"maxNumSeqs,omitempty"`
	// TimeFactorUnderLoad is the slowdown when all MaxNumSeqs slots are busy (>= 1).
	TimeFactorUnderLoad float64 `yaml:"timeFactorUnderLoad,omitempty"`
	// MaxModelLen is the context window in tokens.
	MaxModelLen int `yaml:"maxModelLen,omitempty"`
	// KVCacheBlocks is the KV cache size in 64-token blocks.
	KVCacheBlocks int `yaml:"kvCacheBlocks,omitempty"`
}

// DefaultSyntheticProfile is the model size simulated when ai.synthetic.profile is unset.
const DefaultSyntheticProfile = "llama-3.1-8b"

// SyntheticProfiles holds the timing of each simulated model size.
var SyntheticProfiles = map[string]SyntheticSpec{
	"llama-3.1-8b": {
		Model: "NousResearch/Meta-Llama-3.1-8B-Instruct", TTFTBaseMs: 7, PrefillUsPerToken: 18, ITLMs: 4.6,
		MaxNumSeqs: 256, TimeFactorUnderLoad: 2.5, MaxModelLen: 131072, KVCacheBlocks: 7000,
	},
	"llama-3.3-70b": {
		Model: "unsloth/Llama-3.3-70B-Instruct", TTFTBaseMs: 14, PrefillUsPerToken: 45, ITLMs: 13.8,
		MaxNumSeqs: 256, TimeFactorUnderLoad: 2.0, MaxModelLen: 131072, KVCacheBlocks: 12000,
	},
	"llama-3.1-405b": {
		Model: "NousResearch/Hermes-3-Llama-3.1-405B", TTFTBaseMs: 30, PrefillUsPerToken: 120, ITLMs: 38,
		MaxNumSeqs: 128, TimeFactorUnderLoad: 2.0, MaxModelLen: 131072, KVCacheBlocks: 7000,
	},
}

// SyntheticAIEnabled reports whether synthetic AI inference simulation is enabled.
func (c *Cluster) SyntheticAIEnabled() bool {
	return c != nil && c.AI != nil && c.AI.Synthetic != nil && c.AI.Synthetic.Enabled
}

// UsesSyntheticAI reports whether the AI scenario runs the simulator: ai.synthetic is
// enabled, or the cluster has node pools and none of them has GPUs.
func (c *Cluster) UsesSyntheticAI() bool {
	if c == nil {
		return false
	}
	if c.SyntheticAIEnabled() {
		return true
	}
	if c.ClusterSpec.DefaultPool.MachineType == "" && c.ClusterSpec.TmmPool.MachineType == "" {
		return false
	}

	mt := strings.ToLower(c.ClusterSpec.DefaultPool.MachineType)
	tmmMt := strings.ToLower(c.ClusterSpec.TmmPool.MachineType)
	if strings.HasPrefix(mt, "a2-") || strings.HasPrefix(mt, "g2-") || strings.HasPrefix(mt, "a3-") ||
		strings.HasPrefix(tmmMt, "a2-") || strings.HasPrefix(tmmMt, "g2-") || strings.HasPrefix(tmmMt, "a3-") {
		return false
	}
	return true
}

// SimulatorTags labels benchmark results with what the simulator stands in for.
func (c *Cluster) SimulatorTags() map[string]string {
	if !c.UsesSyntheticAI() {
		return nil
	}
	s := c.ResolvedSynthetic()
	return map[string]string{"model_server": "llm-d-inference-sim", "sim_profile": s.Profile, "sim_model": s.Model}
}

// ResolvedSynthetic returns ai.synthetic with every default applied.
func (c *Cluster) ResolvedSynthetic() SyntheticSpec {
	var s SyntheticSpec
	if c != nil && c.AI != nil && c.AI.Synthetic != nil {
		s = *c.AI.Synthetic
	}
	applySyntheticDefaults(&s)
	return s
}

// validateSynthetic checks the ai.synthetic block constraints.
func validateSynthetic(s *SyntheticSpec) error {
	if s == nil || !s.Enabled {
		return nil
	}
	if s.Profile != "" {
		if _, ok := SyntheticProfiles[s.Profile]; !ok {
			return fmt.Errorf("ai.synthetic.profile %q is not one of %s", s.Profile, strings.Join(SyntheticProfileNames(), ", "))
		}
	}
	for name, v := range map[string]float64{
		"replicas": float64(s.Replicas), "ttftBaseMs": float64(s.TTFTBaseMs), "prefillUsPerToken": float64(s.PrefillUsPerToken),
		"itlMs": s.ITLMs, "maxNumSeqs": float64(s.MaxNumSeqs), "maxModelLen": float64(s.MaxModelLen), "kvCacheBlocks": float64(s.KVCacheBlocks),
	} {
		if v < 0 {
			return fmt.Errorf("ai.synthetic.%s must be >= 0, got %v", name, v)
		}
	}
	if s.TimeFactorUnderLoad != 0 && s.TimeFactorUnderLoad < 1 {
		return fmt.Errorf("ai.synthetic.timeFactorUnderLoad must be >= 1, got %v", s.TimeFactorUnderLoad)
	}
	return nil
}

// SyntheticProfileNames lists the known ai.synthetic.profile values, sorted.
func SyntheticProfileNames() []string {
	names := make([]string, 0, len(SyntheticProfiles))
	for n := range SyntheticProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// applySyntheticDefaults fills zero-value synthetic inference fields from the profile.
func applySyntheticDefaults(s *SyntheticSpec) {
	if s.Image == "" {
		s.Image = DefaultSyntheticImage
	}
	if s.Profile == "" {
		s.Profile = DefaultSyntheticProfile
	}
	p := SyntheticProfiles[s.Profile]
	if s.Model == "" {
		s.Model = p.Model
	}
	if s.ServedModelName == "" {
		s.ServedModelName = "llama3"
	}
	if s.Replicas <= 0 {
		s.Replicas = 1
	}
	if s.TTFTBaseMs <= 0 {
		s.TTFTBaseMs = p.TTFTBaseMs
	}
	if s.PrefillUsPerToken <= 0 {
		s.PrefillUsPerToken = p.PrefillUsPerToken
	}
	if s.ITLMs <= 0 {
		s.ITLMs = p.ITLMs
	}
	if s.MaxNumSeqs <= 0 {
		s.MaxNumSeqs = p.MaxNumSeqs
	}
	if s.TimeFactorUnderLoad <= 0 {
		s.TimeFactorUnderLoad = p.TimeFactorUnderLoad
	}
	if s.MaxModelLen <= 0 {
		s.MaxModelLen = p.MaxModelLen
	}
	if s.KVCacheBlocks <= 0 {
		s.KVCacheBlocks = p.KVCacheBlocks
	}
}
